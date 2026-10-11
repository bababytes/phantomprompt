package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

type CLISettings struct {
	Proxy        string   `json:"proxy"`
	Gateway      string   `json:"gateway"`
	BlockedPaths []string `json:"blocked_paths"`
	User         string   `json:"user"`
	CA           string   `json:"ca"`
	Integrations []string `json:"integrations"`
}

type CLISession struct {
	ID     string `json:"session"`
	Secret string `json:"secret"`
}

var ErrOutdatedEngine = errors.New("the running PhantomPrompt is outdated")

var proxyVariables = map[string]bool{
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "NO_PROXY": true,
	"http_proxy": true, "https_proxy": true, "all_proxy": true, "no_proxy": true,
	"NODE_EXTRA_CA_CERTS": true,
}

func engineClient(socket string) *http.Client {
	return &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			if err := privateSocket(socket); err != nil {
				return nil, err
			}
			return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", socket)
		},
	}}
}

func loopbackAddress(address string) bool {
	host, port, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	return err == nil && port != "" && !strings.ContainsAny(port, "/?#") && ip != nil && ip.IsLoopback()
}

func OpenCLISession(socket, label string, pid int, folder string) (CLISession, func(), error) {
	client := engineClient(socket)
	open := func(fields map[string]any) (*http.Response, error) {
		request, _ := json.Marshal(fields)
		return client.Post("http://engine/v1/cli/session", "application/json", bytes.NewReader(request))
	}
	response, err := open(map[string]any{"label": label, "pid": pid, "folder": folder})
	if err == nil && response.StatusCode == http.StatusBadRequest {
		response.Body.Close()
		response, err = open(map[string]any{"label": label, "pid": pid})
	}
	if err != nil {
		return CLISession{}, func() {}, err
	}
	defer response.Body.Close()
	var session CLISession
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 4<<10)).Decode(&session) != nil || session.ID == "" {
		return CLISession{}, func() {}, errors.New("session unavailable")
	}
	if session.Secret == "" {
		return CLISession{}, func() {}, ErrOutdatedEngine
	}
	closeSession := func() {
		body, _ := json.Marshal(map[string]string{"session": session.ID})
		if response, err := client.Post("http://engine/v1/cli/close", "application/json", bytes.NewReader(body)); err == nil {
			response.Body.Close()
		}
		client.CloseIdleConnections()
	}
	return session, closeSession, nil
}

type TerminalState int

const (
	TerminalConnected TerminalState = iota
	TerminalDisconnected
	TerminalClosedInPanel
)

func TouchCLISession(socket, session, label string, pid int, folder string) TerminalState {
	client := engineClient(socket)
	defer client.CloseIdleConnections()
	body, _ := json.Marshal(map[string]string{"session": session, "label": label, "pid": strconv.Itoa(pid), "folder": folder})
	response, err := client.Post("http://engine/v1/cli/touch", "application/json", bytes.NewReader(body))
	if err != nil {
		return TerminalConnected
	}
	response.Body.Close()
	switch response.StatusCode {
	case http.StatusGone:
		return TerminalClosedInPanel
	case http.StatusNotFound:
		return TerminalDisconnected
	}
	return TerminalConnected
}

func FetchCLISettings(socket string) (CLISettings, error) {
	client := engineClient(socket)
	defer client.CloseIdleConnections()
	response, err := client.Get("http://engine/v1/cli")
	if err != nil {
		return CLISettings{}, err
	}
	defer response.Body.Close()
	var settings CLISettings
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&settings) != nil {
		return CLISettings{}, errors.New("unexpected engine reply")
	}
	if settings.Proxy == "" || settings.User == "" || !strings.Contains(settings.CA, "BEGIN CERTIFICATE") {
		return CLISettings{}, errors.New("incomplete engine settings")
	}
	if !loopbackAddress(settings.Proxy) || settings.Gateway != "" && !loopbackAddress(settings.Gateway) {
		return CLISettings{}, errors.New("engine settings point outside this machine")
	}
	return settings, nil
}

// TrustedFolder is a folder whose content the app sends to models unmasked.
type TrustedFolder struct {
	Path    string `json:"path"`
	Files   int    `json:"files"`
	Values  int    `json:"values"`
	Skipped int    `json:"skipped"`
	Error   string `json:"error,omitempty"`
}

var ErrTrustedFolder = errors.New("folder rejected: it must be an absolute path that is not a protected folder")

func TrustedFolders(socket string) ([]TrustedFolder, error) {
	client := engineClient(socket)
	defer client.CloseIdleConnections()
	response, err := client.Get("http://engine/v1/trusted")
	if err != nil {
		return nil, err
	}
	return trustedReply(response)
}

func SetTrustedFolder(socket, path string, trusted bool) ([]TrustedFolder, error) {
	client := engineClient(socket)
	defer client.CloseIdleConnections()
	body, _ := json.Marshal(map[string]any{"path": path, "trusted": trusted})
	response, err := client.Post("http://engine/v1/trusted", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	return trustedReply(response)
}

func trustedReply(response *http.Response) ([]TrustedFolder, error) {
	defer response.Body.Close()
	var folders []TrustedFolder
	switch {
	case response.StatusCode == http.StatusNotFound:
		return nil, ErrOutdatedEngine
	case response.StatusCode == http.StatusBadRequest:
		return nil, ErrTrustedFolder
	case response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&folders) != nil:
		return nil, errors.New("unexpected engine reply")
	}
	return folders, nil
}

func Shutdown(socket string) error {
	client := engineClient(socket)
	defer client.CloseIdleConnections()
	response, err := client.Post("http://engine/v1/shutdown", "application/json", nil)
	if err != nil {
		return err
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return ErrOutdatedEngine
	}
	for range 50 {
		if _, err := FetchCLISettings(socket); err != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("PhantomPrompt did not stop in time")
}

func PanelURL(socket string) (string, error) {
	client := engineClient(socket)
	defer client.CloseIdleConnections()
	response, err := client.Get("http://engine/v1/panel")
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var reply struct {
		URL string `json:"url"`
	}
	if response.StatusCode == http.StatusNotFound {
		return "", ErrOutdatedEngine
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 4<<10)).Decode(&reply) != nil {
		return "", errors.New("unexpected engine reply")
	}
	panel, err := url.Parse(reply.URL)
	if err != nil || panel.Scheme != "http" || panel.User != nil || !net.ParseIP(panel.Hostname()).IsLoopback() {
		return "", errors.New("unexpected panel address")
	}
	return panel.String(), nil
}

func WriteCA(settings CLISettings, existing string) (string, func(), error) {
	content := settings.CA
	if existing != "" {
		if data, err := os.ReadFile(existing); err == nil {
			content += "\n" + string(data)
		}
	}
	file, err := os.CreateTemp("", "phantomprompt-ca-*.pem")
	if err != nil {
		return "", func() {}, err
	}
	name := file.Name()
	cleanup := func() { os.Remove(name) }
	if _, err := file.WriteString(content); err != nil {
		file.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return name, cleanup, nil
}

// claudeVariables configure Claude Code for the PhantomPrompt gateway; a user value is replaced.
var claudeVariables = map[string]bool{"ANTHROPIC_BASE_URL": true, "DISABLE_TELEMETRY": true, "DISABLE_ERROR_REPORTING": true, "CLAUDE_CODE_GATEWAY_HINT_HEADERS": true}

// ClaudeEnvironment points Claude Code at the local gateway with the documented ANTHROPIC_BASE_URL.
// Only model requests reach PhantomPrompt; telemetry and error reports are switched off because
// they would bypass it, and gateway hint headers let the panel group requests by prompt.
func ClaudeEnvironment(base []string, settings CLISettings, session CLISession) []string {
	env := make([]string, 0, len(base)+5)
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		if !proxyVariables[name] && !claudeVariables[name] {
			env = append(env, entry)
		}
	}
	return append(env, claudeGatewayVariables(base, settings, session)...)
}

func claudeBaseURL(settings CLISettings, session CLISession) string {
	return "http://" + settings.Gateway + "/claude/s/" + session.ID + "/" + session.Secret
}

// With a custom base URL Claude Code stops deferring tool definitions and sends all of them on
// every request (measured: about 11.7k more input tokens), unless tool search is enabled
// explicitly. A user's own ENABLE_TOOL_SEARCH setting is kept.
func claudeGatewayVariables(base []string, settings CLISettings, session CLISession) []string {
	variables := []string{"ANTHROPIC_BASE_URL=" + claudeBaseURL(settings, session), "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1", "CLAUDE_CODE_GATEWAY_HINT_HEADERS=1"}
	if !slices.ContainsFunc(base, func(entry string) bool { return strings.HasPrefix(entry, "ENABLE_TOOL_SEARCH=") }) {
		variables = append(variables, "ENABLE_TOOL_SEARCH=true")
	}
	return variables
}

// ClaudeDenyArguments turn blocked folders into Claude Code permission rules, so Claude cannot
// read or edit them with its file tools or with cat, head, tail, sed and redirections. They are
// added with --settings, which Claude Code merges with the user's own deny lists.
func ClaudeDenyArguments(blocked []string, args []string) []string {
	if len(blocked) == 0 || slices.Contains(args, "--settings") || slices.ContainsFunc(args, func(arg string) bool { return strings.HasPrefix(arg, "--settings=") }) {
		return args
	}
	var deny []string
	for _, entry := range blocked {
		rule := "//" + strings.TrimPrefix(entry, "/")
		if info, err := os.Stat(entry); err != nil || info.IsDir() {
			rule += "/**"
		}
		deny = append(deny, "Read("+rule+")", "Edit("+rule+")")
	}
	settings, _ := json.Marshal(map[string]any{"permissions": map[string]any{"deny": deny}})
	return append([]string{"--settings", string(settings)}, args...)
}

// CodexArguments add a PhantomPrompt model provider for this run only (-c overrides; nothing is
// written to ~/.codex/config.toml). The provider speaks plain HTTP and SSE, keeps the user's
// ChatGPT or API-key login (requires_openai_auth) and analytics are off because they would
// bypass PhantomPrompt. It returns the arguments unchanged when the user picks a provider.
func CodexArguments(settings CLISettings, session CLISession, upstream string, args []string) ([]string, bool) {
	for _, arg := range args {
		if strings.Contains(arg, "model_provider") || arg == "--oss" || arg == "--profile" || arg == "-p" {
			return args, false
		}
	}
	path := map[string]string{"chatgpt": "backend-api/codex", "openai": "v1"}[upstream]
	base := "http://" + settings.Gateway + "/codex/s/" + session.ID + "/" + session.Secret + "/" + path
	// Codex sends its ChatGPT-backend requests (needed by subscription models) only for a provider
	// named OpenAI; compression is off so the body stays inspectable.
	provider := fmt.Sprintf(`model_providers.phantomprompt={name="OpenAI",base_url=%q,requires_openai_auth=true,supports_websockets=false}`, base)
	return append([]string{"-c", `model_provider="phantomprompt"`, "-c", provider, "-c", "features.enable_request_compression=false", "-c", "analytics.enabled=false"}, args...), true
}

// CodexUpstream picks the model API that matches how Codex is signed in.
func CodexUpstream(environment []string) string {
	codexHome, home := "", ""
	for _, entry := range environment {
		name, value, _ := strings.Cut(entry, "=")
		switch name {
		case "CODEX_HOME":
			codexHome = value
		case "HOME":
			home = value
		}
	}
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	data, err := os.ReadFile(filepath.Join(codexHome, "auth.json"))
	if err == nil {
		var auth struct {
			Mode string `json:"auth_mode"`
		}
		if json.Unmarshal(data, &auth) == nil && strings.EqualFold(auth.Mode, "apikey") {
			return "openai"
		}
		return "chatgpt"
	}
	for _, entry := range environment {
		if value, ok := strings.CutPrefix(entry, "OPENAI_API_KEY="); ok && value != "" {
			return "openai"
		}
	}
	return "chatgpt"
}

// AgentEnvironment is the environment of a coding agent that reaches PhantomPrompt only through
// its gateway: no proxy variables, so its other traffic (npm, pip, web fetches) is unchanged.
func AgentEnvironment(base []string) []string {
	env := make([]string, 0, len(base))
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		if !proxyVariables[name] {
			env = append(env, entry)
		}
	}
	return env
}

func ProxyEnvironment(base []string, settings CLISettings, session CLISession, caFile string) []string {
	env := make([]string, 0, len(base)+8)
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		if !proxyVariables[name] {
			env = append(env, entry)
		}
	}
	proxyURL := (&url.URL{Scheme: "http", User: url.UserPassword(settings.User+"+"+session.ID, session.Secret), Host: settings.Proxy}).String()
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		env = append(env, name+"="+proxyURL)
	}
	env = append(env, "NO_PROXY=localhost,127.0.0.1,::1", "no_proxy=localhost,127.0.0.1,::1", "NODE_EXTRA_CA_CERTS="+caFile)
	if settings.Gateway == "" {
		return env
	}
	filtered := env[:0]
	for _, entry := range env {
		if name, _, _ := strings.Cut(entry, "="); !claudeVariables[name] {
			filtered = append(filtered, entry)
		}
	}
	return append(filtered, claudeGatewayVariables(base, settings, session)...)
}
