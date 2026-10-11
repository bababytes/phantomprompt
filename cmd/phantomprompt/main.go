package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"phantomprompt/internal/applog"
	"phantomprompt/internal/demo"
	"phantomprompt/internal/extension"
	"phantomprompt/internal/mask"
	"phantomprompt/internal/proxy"
)

// version is set at release build time with -ldflags "-X main.version=<tag>".
var version = "dev"

// buildVersion falls back to the module version Go stamps from the VCS tag when no release version was linked.
func buildVersion() string {
	module := ""
	if info, ok := debug.ReadBuildInfo(); ok {
		module = info.Main.Version
	}
	return chooseVersion(version, module)
}

func chooseVersion(linked, module string) string {
	if linked != "dev" || module == "" || module == "(devel)" {
		return linked
	}
	return module
}

func main() {
	if len(os.Args) == 1 && launchedFromFinder() {
		if err := runOpen(); err != nil {
			reportLaunchFailure(err)
			os.Exit(1)
		}
		return
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "PhantomPrompt:", err)
		os.Exit(1)
	}
}

// launchedFromFinder tells the app icon (no terminal on stdin) from "phantom" typed in a shell.
func launchedFromFinder() bool {
	var state syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), ioctlReadTermios, uintptr(unsafe.Pointer(&state)))
	return errno != 0
}

func reportLaunchFailure(err error) {
	if folder, folderErr := logFolder(); folderErr == nil {
		if appLog, logErr := applog.Open(folder); logErr == nil {
			appLog.Logger().Error("app could not start", "error", err.Error())
			appLog.Close()
		}
	}
	exec.Command("osascript", "-e", `display alert "PhantomPrompt could not start" message "Details are in ~/Library/Logs/PhantomPrompt" as critical`).Run()
}

func listen(address string) (net.Listener, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return nil, errors.New("listen address must be a loopback IP and port")
	}
	return net.Listen("tcp", address)
}

func engineSocket(app *proxy.App, addresses proxy.LocalAddresses, start func(net.Listener, http.Handler) *http.Server) func() {
	socket, err := extension.SocketPath()
	if err != nil {
		fmt.Println("Engine socket: no user configuration directory; protected terminals are unavailable.")
		return func() {}
	}
	listener, err := extension.Listen(socket)
	if errors.Is(err, extension.ErrRunning) {
		fmt.Println("Engine socket: served by another running PhantomPrompt instance.")
		return func() {}
	}
	if err != nil {
		fmt.Println("Engine socket: cannot open it; protected terminals are unavailable.")
		return func() {}
	}
	server := start(listener, app.Extension(addresses))
	return func() { server.Close(); os.Remove(socket) }
}

type banner struct {
	headline, detail string
	ghost            bool
}

var ghostArt = [...][3]string{
	{"       ", " ▄█████▄ ", "       "},
	{"       ", "█████████", "       "},
	{"━ ━━━  ", "█▄▄▄█▄▄▄█", "  ━━━ ━"},
	{"━━ ━━  ", "█████████", "  ━━ ━━"},
	{"━━━ ━  ", "▀█▀▀█▀▀█▀", "  ━ ━━━"},
}

const ghostFloor = "      ━━━━━━━━━━━      "

const ghostWidth = 23

func (b banner) render(color bool) string {
	if !color {
		return "PhantomPrompt: " + b.headline + ". " + strings.ReplaceAll(b.detail, "\n", " ") + "\n"
	}
	headline := "\033[1;32m● \033[1;97mPhantom\033[1;38;5;141mPrompt\033[1;32m · " + b.headline + "\033[0m"
	if !b.ghost {
		return headline + "\n  \033[2m" + b.detail + "\033[0m\n"
	}
	text := []string{"", headline}
	for _, paragraph := range strings.Split(b.detail, "\n") {
		for _, line := range wrapWords(paragraph, 50) {
			text = append(text, "\033[2m"+line+"\033[0m")
		}
	}
	var out strings.Builder
	for row := range max(len(ghostArt)+1, len(text)) {
		switch {
		case row < len(ghostArt):
			art := ghostArt[row]
			out.WriteString("\033[2;32m" + art[0] + "\033[0;97m" + art[1] + "\033[0;2;32m" + art[2] + "\033[0m")
		case row == len(ghostArt):
			out.WriteString("\033[2;32m" + ghostFloor + "\033[0m")
		default:
			out.WriteString(strings.Repeat(" ", ghostWidth))
		}
		if row < len(text) && text[row] != "" {
			out.WriteString("   " + text[row])
		}
		out.WriteString("\n")
	}
	return out.String()
}

func wrapWords(text string, width int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		if line != "" && len([]rune(line))+1+len([]rune(word)) > width {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func (b banner) print() {
	fmt.Fprint(os.Stderr, b.render(colorTerminal(os.Stderr)))
}

func colorTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0 && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}

func insideProtectedShell() bool {
	return os.Getenv("PHANTOMPROMPT_SHELL") == "1"
}

func runShell() error {
	if insideProtectedShell() {
		banner{"This terminal is already protected", "Just type claude, codex or copilot.", false}.print()
		return nil
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	shims, removeShims, err := agentShims()
	if err != nil {
		return errors.New("cannot prepare the protected shell")
	}
	defer removeShims()
	extra := []string{"PHANTOMPROMPT_SHELL=1"}
	if shims != "" {
		extra = append(extra, "PHANTOMPROMPT_SHIMS="+shims, "PATH="+shims+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	notice := banner{"Your terminal is protected", "AI tools you start here go through PhantomPrompt:\nclaude, codex, copilot, …\nType exit to leave.", true}
	return protectedProcess(shell, []string{"-i"}, true, notice, filepath.Base(shell), extra...)
}

// gatewayAgents are started through their gateway so that they get the same protection whether
// they are typed in the protected shell or started with phantom <agent>.
var gatewayAgents = []string{"claude", "codex"}

// agentShims puts small wrappers first on the protected shell's PATH: typing claude or codex
// there runs them through this launcher, like phantom claude / phantom codex.
func agentShims() (string, func(), error) {
	self, err := os.Executable()
	if err != nil {
		return "", func() {}, err
	}
	directory, err := os.MkdirTemp("", "phantomprompt-shims-")
	if err != nil {
		return "", func() {}, err
	}
	remove := func() { os.RemoveAll(directory) }
	for _, agent := range gatewayAgents {
		if _, err := exec.LookPath(agent); err != nil {
			continue
		}
		script := "#!/bin/sh\nexec " + shellQuote(self) + " run " + agent + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(directory, agent), []byte(script), 0700); err != nil {
			remove()
			return "", func() {}, err
		}
	}
	return directory, remove, nil
}

func openProtectedTerminal(folder string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	directory, err := os.MkdirTemp("", "phantomprompt-terminal-")
	if err != nil {
		return err
	}
	script := filepath.Join(directory, "PhantomPrompt.command")
	if err := os.WriteFile(script, []byte(terminalScript(self, folder)), 0700); err != nil {
		os.RemoveAll(directory)
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "open", script).Run(); err != nil {
		os.RemoveAll(directory)
		return err
	}
	return nil
}

func terminalScript(self, folder string) string {
	return "#!/bin/sh\nrm -f -- \"$0\"\nrmdir -- \"${0%/*}\" 2>/dev/null\ncd -- " + shellQuote(folder) + " || exit 1\n[ -t 1 ] && printf '\\033[3J\\033[H\\033[2J'\nexec " + shellQuote(self) + "\n"
}

func shellQuote(text string) string {
	return "'" + strings.ReplaceAll(text, "'", `'"'"'`) + "'"
}

// lookPath finds a command while skipping the protected shell's own wrappers.
func lookPath(name string) (string, error) {
	shims := os.Getenv("PHANTOMPROMPT_SHIMS")
	if shims == "" || strings.Contains(name, "/") {
		return exec.LookPath(name)
	}
	for _, directory := range filepath.SplitList(os.Getenv("PATH")) {
		if directory == shims || directory == "" {
			continue
		}
		candidate := filepath.Join(directory, name)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return candidate, nil
		}
	}
	return "", exec.ErrNotFound
}

func runCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: phantomprompt secure <command> [arguments], for example: phantomprompt secure copilot")
	}
	binary, err := lookPath(args[0])
	if err != nil {
		return fmt.Errorf("command not found: %s", args[0])
	}
	if filepath.Base(binary) == "claude" {
		if err := claudeEndpointError(os.Environ()); err != nil {
			return err
		}
	}
	notice := banner{args[0] + " is protected", "It runs through the local proxy with its own token map; only this process is affected.", false}
	return protectedProcess(binary, args[1:], false, notice, filepath.Base(args[0]))
}

func logFolder() (string, error) {
	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		return filepath.Join(home, "Library", "Logs", "PhantomPrompt"), err
	}
	cache, err := os.UserCacheDir()
	return filepath.Join(cache, "PhantomPrompt"), err
}

// openAppLog opens the app log; without one the app still runs and keeps events in memory.
func openAppLog() *applog.Log {
	folder, err := logFolder()
	if err == nil {
		var appLog *applog.Log
		if appLog, err = applog.Open(folder); err == nil {
			fmt.Printf("Log folder: %s\n", folder)
			return appLog
		}
	}
	fmt.Println("Log folder: cannot open it; events are kept in memory only.")
	return nil
}

func startBackgroundApp(socket string, openPanel bool) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	app := exec.Command(self, "web", "-open="+strconv.FormatBool(openPanel))
	app.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := app.Start(); err != nil {
		return err
	}
	go app.Wait()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := extension.FetchCLISettings(socket); err == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	syscall.Kill(-app.Process.Pid, syscall.SIGTERM)
	folder, _ := logFolder()
	return fmt.Errorf("the PhantomPrompt app did not start in time; see the log in %s", folder)
}

func runOpen() error {
	socket, err := extension.SocketPath()
	if err != nil {
		return err
	}
	panel, err := extension.PanelURL(socket)
	if errors.Is(err, extension.ErrOutdatedEngine) {
		return errors.New("the running PhantomPrompt has no panel; stop it with: phantomprompt kill")
	}
	if err == nil {
		return openURL(panel)
	}
	return startBackgroundApp(socket, true)
}

func runStop() error {
	socket, err := extension.SocketPath()
	if err != nil {
		return err
	}
	if _, err := extension.FetchCLISettings(socket); err != nil {
		fmt.Println("PhantomPrompt is not running.")
		return nil
	}
	if err := extension.Shutdown(socket); errors.Is(err, extension.ErrOutdatedEngine) {
		return errors.New("the running PhantomPrompt is too old to stop this way; use the Stop button in the panel or phantomprompt kill")
	} else if err != nil {
		return err
	}
	fmt.Println("PhantomPrompt stopped.")
	return nil
}

func showInFinder(folder string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "open", folder).Run()
}

func openURL(target string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("automatic browser opening is available on macOS; open " + target + " manually")
	}
	return exec.Command("open", target).Run()
}

func protectedProcess(binary string, args []string, openPanel bool, notice banner, label string, extraEnv ...string) error {
	socket, err := extension.SocketPath()
	if err != nil {
		return err
	}
	settings, err := extension.FetchCLISettings(socket)
	if err != nil {
		fmt.Fprintln(os.Stderr, "PhantomPrompt: starting the app in the background; it keeps running after you leave (stop it from the panel or with: phantomprompt kill)…")
		if err := startBackgroundApp(socket, openPanel); err != nil {
			return err
		}
		settings, err = extension.FetchCLISettings(socket)
		if err != nil {
			return errors.New("PhantomPrompt app started but did not respond")
		}
	}
	folder, _ := os.Getwd()
	session, closeSession, err := extension.OpenCLISession(socket, label, os.Getpid(), folder)
	if errors.Is(err, extension.ErrOutdatedEngine) {
		return errors.New("the running PhantomPrompt is outdated; restart it with: phantomprompt kill")
	}
	if err != nil {
		return errors.New("PhantomPrompt could not start a masking session")
	}
	agent := filepath.Base(binary)
	gateway := settings.Gateway
	if claudeEndpointError(os.Environ()) != nil {
		settings.Gateway = ""
	}
	gatewayOnly := false
	switch agent {
	case "claude":
		gatewayOnly = settings.Gateway != ""
	case "codex":
		if gateway != "" {
			codex := settings
			codex.Gateway = gateway
			args, gatewayOnly = extension.CodexArguments(codex, session, extension.CodexUpstream(os.Environ()), args)
			if !gatewayOnly {
				fmt.Fprintln(os.Stderr, "PhantomPrompt: your own Codex model provider is kept; it runs behind the HTTPS proxy, which rejects AI hosts it cannot inspect.")
			}
		}
	}
	if gatewayOnly {
		notice.detail = "Model requests go to the local PhantomPrompt gateway with their own token map; other network traffic is unchanged."
	}
	caFile, removeCA := "", func() {}
	if !gatewayOnly {
		caFile, removeCA, err = extension.WriteCA(settings, os.Getenv("NODE_EXTRA_CA_CERTS"))
		if err != nil {
			closeSession()
			return errors.New("cannot write the temporary CA certificate")
		}
	}
	keepalive := time.NewTicker(5 * time.Second)
	defer keepalive.Stop()
	done := make(chan struct{})
	var once sync.Once
	cleanup := func() { once.Do(func() { close(done); removeCA(); closeSession() }) }
	defer cleanup()
	if filepath.Base(binary) == "claude" {
		if len(settings.BlockedPaths) > 0 && slices.ContainsFunc(args, func(arg string) bool { return arg == "--settings" || strings.HasPrefix(arg, "--settings=") }) {
			fmt.Fprintln(os.Stderr, "PhantomPrompt: your --settings flag is kept, so blocked folders are only removed from requests, not denied to Claude Code's tools.")
		}
		args = extension.ClaudeDenyArguments(settings.BlockedPaths, args)
	}
	command := exec.Command(binary, args...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	switch {
	case gatewayOnly && agent == "claude":
		command.Env = append(extension.ClaudeEnvironment(os.Environ(), settings, session), extraEnv...)
	case gatewayOnly:
		command.Env = append(extension.AgentEnvironment(os.Environ()), extraEnv...)
	default:
		command.Env = append(extension.ProxyEnvironment(os.Environ(), settings, session, caFile), extraEnv...)
	}
	signals := make(chan os.Signal, 1)
	watched := []os.Signal{os.Interrupt, syscall.SIGTERM}
	if !signal.Ignored(syscall.SIGHUP) {
		watched = append(watched, syscall.SIGHUP)
	}
	signal.Notify(signals, watched...)
	defer signal.Stop(signals)
	if !insideProtectedShell() {
		notice.print()
	}
	if err := command.Start(); err != nil {
		return err
	}
	go func() {
		for received := range signals {
			if received == syscall.SIGTERM || received == syscall.SIGHUP {
				command.Process.Signal(received)
			}
		}
	}()
	var closedInPanel atomic.Bool
	go func() {
		warned := insideProtectedShell()
		for {
			select {
			case <-keepalive.C:
				switch extension.TouchCLISession(socket, session.ID, label, os.Getpid(), folder) {
				case extension.TerminalClosedInPanel:
					closedInPanel.Store(true)
					endProcess(command.Process)
					return
				case extension.TerminalDisconnected:
					if !warned {
						fmt.Fprint(os.Stderr, "\r\nPhantomPrompt: PhantomPrompt was restarted, so this terminal lost its connection and AI requests fail. Close it (or click × in the panel) and open a new protected terminal.\r\n")
					}
					warned = true
				}
			case <-done:
				return
			}
		}
	}()
	err = command.Wait()
	if closedInPanel.Load() {
		fmt.Fprintln(os.Stderr, "PhantomPrompt: this terminal was closed in the panel.")
		return nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		cleanup()
		os.Exit(exit.ExitCode())
	}
	return err
}

func endProcess(process *os.Process) {
	process.Signal(syscall.SIGTERM)
	time.Sleep(2 * time.Second)
	process.Signal(syscall.SIGHUP)
}

// claudeEndpointError refuses Claude Code routing that PhantomPrompt cannot protect: a company
// gateway, a local model proxy, Bedrock, Vertex or Foundry would bypass masking. The default
// endpoint and another PhantomPrompt gateway on this Mac are replaced by this app's gateway.
// The message names the setting, never its value, which may carry a token.
func claudeEndpointError(env []string) error {
	var names []string
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		switch name {
		case "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY":
			if value != "" {
				names = append(names, name)
			}
		case "ANTHROPIC_BASE_URL":
			if value != "" && !replaceableClaudeURL(value) {
				names = append(names, name)
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	slices.Sort(names)
	names = slices.Compact(names)
	return fmt.Errorf("claude was not started: %s sends it to another endpoint, and PhantomPrompt protects only api.anthropic.com.\nTo continue, run: unset %s — or run claude outside PhantomPrompt", strings.Join(names, ", "), strings.Join(names, " "))
}

func replaceableClaudeURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	if parsed.Scheme == "https" && parsed.Host == "api.anthropic.com" && (parsed.Path == "" || parsed.Path == "/") {
		return true
	}
	host := parsed.Hostname()
	loopback := host == "localhost" || net.ParseIP(host).IsLoopback()
	return parsed.Scheme == "http" && loopback && strings.HasPrefix(parsed.Path, proxy.ClaudeRoute)
}

// masGateway is fixed so that a shell profile can keep pointing Claude Code at it.
const masGateway = "127.0.0.1:47820"

func gatewayKey() (string, error) {
	path, err := extension.GatewayKeyPath()
	if err != nil {
		return "", err
	}
	if data, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(data))) >= 32 {
		return strings.TrimSpace(string(data)), nil
	}
	key, err := mask.RandomID()
	if err != nil {
		return "", err
	}
	key += key[:16]
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", errors.New("cannot create the PhantomPrompt data folder")
	}
	if err := os.WriteFile(path, []byte(key+"\n"), 0600); err != nil {
		return "", errors.New("cannot save the gateway key")
	}
	return key, nil
}

// claudeSetupSnippet covers every coding agent the gateway serves: shell lines for Claude Code
// and a Codex model provider (for an API-key login, replace /chatgpt with /openai).
func claudeSetupSnippet(gateway, key string) string {
	return "export ANTHROPIC_BASE_URL=" + proxy.ClaudeKeyURL(gateway, key) +
		"\nexport ENABLE_TOOL_SEARCH=true DISABLE_TELEMETRY=1 DISABLE_ERROR_REPORTING=1 CLAUDE_CODE_GATEWAY_HINT_HEADERS=1" +
		"\nalias codex='codex -c model_provider=\"phantomprompt\" -c '\\''model_providers.phantomprompt={name=\"OpenAI\",base_url=\"" + proxy.CodexKeyURL(gateway, key, "chatgpt") + "\",requires_openai_auth=true,supports_websockets=false}'\\'' -c features.enable_request_compression=false -c analytics.enabled=false'"
}

// appCommands lets the App Store wrapper drive the engine over stdin: "panel" prints a fresh
// one-time panel link; end of input (the app quit) stops the engine with it.
func appCommands(input io.Reader, panelLink func() string, stop func()) {
	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "panel" && panelLink != nil {
			fmt.Println("panel-link: " + panelLink())
		}
	}
	stop()
}

func phantomPromptPIDs() ([]int, error) {
	output, err := exec.Command("ps", "-axo", "pid=,args=").Output()
	if err != nil {
		return nil, err
	}
	self := os.Getpid()
	var binary string
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			binary = resolved
		}
	}
	var pids []int
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid == self || len(fields) < 2 {
			continue
		}
		command := fields[1]
		owned := filepath.Base(command) == "phantomprompt"
		if !owned && binary != "" {
			for _, field := range fields[1:] {
				if field == binary {
					owned = true
					break
				}
			}
		}
		if owned {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

func runKill() error {
	pids, err := phantomPromptPIDs()
	if err != nil {
		return errors.New("cannot list processes")
	}
	if len(pids) == 0 {
		fmt.Println("PhantomPrompt: no running processes found.")
	} else {
		for _, pid := range pids {
			syscall.Kill(pid, syscall.SIGTERM)
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			remaining, _ := phantomPromptPIDs()
			if len(remaining) == 0 {
				break
			}
			time.Sleep(150 * time.Millisecond)
		}
		if remaining, _ := phantomPromptPIDs(); len(remaining) > 0 {
			for _, pid := range remaining {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
		fmt.Printf("PhantomPrompt: stopped %d process(es).\n", len(pids))
	}
	if socket, err := extension.SocketPath(); err == nil {
		os.Remove(socket)
	}
	return nil
}

// runTrust lists the trusted folders, or adds (trust) or removes (untrust) one. Their content is
// sent to models unmasked, so they are meant for fake data such as test fixtures.
func runTrust(args []string, trusted bool) error {
	usage := errors.New("usage: phantom trust [folder] | phantom untrust <folder>")
	if len(args) > 1 || !trusted && len(args) == 0 {
		return usage
	}
	socket, err := extension.SocketPath()
	if err != nil {
		return err
	}
	if _, err := extension.FetchCLISettings(socket); err != nil {
		return errors.New("PhantomPrompt is not running; start it with: phantom open")
	}
	var folders []extension.TrustedFolder
	folder := ""
	if len(args) == 0 {
		folders, err = extension.TrustedFolders(socket)
	} else if folder, err = filepath.Abs(args[0]); err == nil {
		folders, err = extension.SetTrustedFolder(socket, folder, trusted)
	}
	if errors.Is(err, extension.ErrOutdatedEngine) {
		return errors.New("the running PhantomPrompt has no trusted folders; restart it with: phantomprompt kill")
	}
	if err != nil {
		return err
	}
	switch {
	case folder != "" && trusted:
		fmt.Println("Every value found in " + folder + " is now sent to AI models without masking. Keep only fake data there.")
	case folder != "":
		fmt.Println("Values from " + folder + " are masked again.")
	}
	if len(folders) == 0 {
		fmt.Println("No trusted folders.")
	}
	for _, entry := range folders {
		detail := fmt.Sprintf("%d files, %d values passed as they are", entry.Files, entry.Values)
		if entry.Error != "" {
			detail = entry.Error
		}
		fmt.Printf("  %s (%s)\n", entry.Path, detail)
	}
	return nil
}

func run(args []string) (err error) {
	if len(args) > 0 && args[0] == "kill" {
		return runKill()
	}
	if len(args) > 0 && args[0] == "run" {
		return runCommand(args[1:])
	}
	if len(args) > 0 && args[0] == "shell" {
		return runShell()
	}
	if len(args) > 0 && args[0] == "secure" {
		if len(args) == 1 {
			return runShell()
		}
		return runCommand(args[1:])
	}
	if len(args) > 0 && args[0] == "stop" {
		return runStop()
	}
	if len(args) > 0 && (args[0] == "trust" || args[0] == "untrust") {
		return runTrust(args[1:], args[0] == "trust")
	}
	if len(args) > 0 && args[0] == "open" {
		if len(args) > 1 {
			return errors.New("usage: phantomprompt open")
		}
		return runOpen()
	}
	if len(args) == 0 {
		return runShell()
	}
	mode := args[0]
	if mode == "version" {
		fmt.Println("PhantomPrompt " + buildVersion() + "\n" + demo.Author)
		return nil
	}
	if mode != "web" && mode != "demo" && mode != "serve" && mode != "mas" {
		if _, err := exec.LookPath(mode); err == nil && !strings.HasPrefix(mode, "-") {
			return runCommand(args)
		}
		return errors.New("usage: phantom (protected shell), phantom <command> (for example phantom claude), or phantom open | stop | kill | trust | untrust | web | version")
	}
	// mas is the sandboxed App Store edition: a Claude Code gateway on a fixed port and the panel,
	// started and stopped by the app; it spawns no programs and writes only into its container.
	localLab := mode == "web" || mode == "demo" || mode == "mas"
	proxyDefault, gatewayDefault, uiDefault := "127.0.0.1:8080", "127.0.0.1:8081", "127.0.0.1:8090"
	integrationsDefault := ""
	switch mode {
	case "web":
		proxyDefault, gatewayDefault, uiDefault = "127.0.0.1:0", "127.0.0.1:0", "127.0.0.1:0"
		integrationsDefault = strings.Join([]string{proxy.CopilotCLI, proxy.ClaudeCode, proxy.CodexCLI}, ",")
	case "mas":
		proxyDefault, gatewayDefault, uiDefault = "127.0.0.1:0", masGateway, "127.0.0.1:0"
		integrationsDefault = strings.Join([]string{proxy.ClaudeCode, proxy.CodexCLI}, ",")
	}
	flags := flag.NewFlagSet(mode, flag.ContinueOnError)
	proxyAddress := flags.String("proxy-listen", proxyDefault, "Explicit HTTP/HTTPS proxy address")
	uiAddress := flags.String("ui-listen", uiDefault, "Local web workspace address")
	gatewayAddress := flags.String("gateway-listen", gatewayDefault, "Local gateway and session control address")
	openBrowser := flags.Bool("open", mode == "web", "Open the web workspace in the default browser (macOS)")
	configFile := flags.String("config", "", "JSON configuration file (serve mode)")
	caOutput := flags.String("ca-out", "", "Optional public CA PEM output; private key is never written")
	detectors := flags.String("detectors", "vin,guid,email,fields,secrets", "Demo detectors: vin,guid,pesel,email,fields,secrets")
	customFields := flags.String("fields", "", "Additional sensitive field names, comma-separated")
	integrations := flags.String("integrations", integrationsDefault, "Integrations, comma-separated: claude-code, codex-cli, copilot-cli")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	var appLog *applog.Log
	if mode == "web" || mode == "mas" {
		appLog = openAppLog()
		appLog.CaptureCrashes()
		defer appLog.Close()
		defer func() {
			if err != nil {
				appLog.Logger().Error("app stopped with an error", "error", err.Error())
			}
		}()
	}
	access := os.Getenv("PHANTOMPROMPT_TOKEN")
	if access == "" {
		if mode == "serve" {
			return errors.New("set PHANTOMPROMPT_TOKEN to a random secret of at least 32 characters")
		}
		var err error
		access, err = mask.RandomID()
		if err != nil {
			return errors.New("could not create a local access token")
		}
	}
	var config proxy.Config
	var options proxy.Options
	if localLab {
		backend, err := demo.StartBackend()
		if err != nil {
			return fmt.Errorf("start local test server: %w", err)
		}
		defer backend.Server.Close()
		defer backend.Listener.Close()
		config = proxy.Config{Rules: []proxy.Rule{{Origin: demo.Origin, Path: "/echo", Adapter: "demo"}}, GatewayOrigin: demo.Origin, Detectors: strings.Split(*detectors, ",")}
		if *integrations != "" {
			config.Integrations = strings.Split(*integrations, ",")
		}
		options, err = backend.Options(len(config.Integrations) > 0)
		if err != nil {
			return errors.New("cannot load system certificate roots")
		}
		if *customFields != "" {
			config.SensitiveFields = strings.Split(*customFields, ",")
		}
		if mode == "web" || mode == "mas" {
			if options.RulesFile, err = extension.RulesPath(); err != nil {
				return errors.New("cannot locate the masking rules file")
			}
			options.Log = appLog
		}
	} else {
		file, err := os.Open(*configFile)
		if err != nil {
			return errors.New("cannot open configuration file")
		}
		defer file.Close()
		decoder := json.NewDecoder(io.LimitReader(file, 65537))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&config) != nil {
			return errors.New("invalid configuration JSON")
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return errors.New("unexpected configuration content")
		}
	}
	app, err := proxy.New(config, access, options)
	if err != nil {
		return err
	}
	defer app.Close()
	key := os.Getenv("PHANTOMPROMPT_GATEWAY_KEY")
	if key == "" && mode == "mas" {
		if key, err = gatewayKey(); err != nil {
			return err
		}
	}
	if key != "" && len(key) < 32 {
		return errors.New("PHANTOMPROMPT_GATEWAY_KEY must contain at least 32 characters")
	}
	app.SetGatewayKey(key)
	if options.RulesFile != "" {
		fmt.Printf("Masking rules: %s (edit them in the panel; they apply to all protected traffic)\n", options.RulesFile)
	}
	if *caOutput != "" {
		if err := os.MkdirAll(filepath.Dir(*caOutput), 0700); err != nil {
			return errors.New("cannot create certificate directory")
		}
		if err := os.WriteFile(*caOutput, app.CA.PEM, 0600); err != nil {
			return errors.New("cannot write public CA certificate")
		}
	}
	proxyListener, err := listen(*proxyAddress)
	if err != nil {
		return fmt.Errorf("start proxy listener: %w", err)
	}
	tracked := proxy.Track(proxyListener, 512)
	defer tracked.Close()
	errorsChannel := make(chan error, 3)
	start := func(listener net.Listener, handler http.Handler) *http.Server {
		server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10, ErrorLog: log.New(io.Discard, "", 0)}
		go func() {
			err := server.Serve(listener)
			if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
				errorsChannel <- err
			}
		}()
		return server
	}
	proxyServer := start(tracked, app)
	defer proxyServer.Close()
	gatewayListener, err := listen(*gatewayAddress)
	if err != nil {
		return fmt.Errorf("start gateway listener: %w", err)
	}
	gatewayServer := start(gatewayListener, app.Gateway(gatewayListener.Addr().String()))
	defer gatewayServer.Close()
	fmt.Printf("PhantomPrompt %s · %s\nProxy: http://%s\nGateway: http://%s\n", buildVersion(), mode, proxyListener.Addr(), gatewayListener.Addr())
	var uiListener net.Listener
	workspaceURL := ""
	logins := demo.NewLogins()
	var panelLink func() string
	if localLab {
		uiListener, err = listen(*uiAddress)
		if err != nil {
			return fmt.Errorf("start demo listener: %w", err)
		}
		workspaceURL = "http://" + demo.PanelHost(uiListener.Addr().String())
		panelLink = func() string { return workspaceURL + logins.Issue() }
	}
	claudeSetup := ""
	if key != "" {
		claudeSetup = claudeSetupSnippet(gatewayListener.Addr().String(), key)
	}
	closeEngine := func() {}
	if mode != "mas" {
		closeEngine = engineSocket(app, proxy.LocalAddresses{Proxy: proxyListener.Addr().String(), Gateway: gatewayListener.Addr().String(), PanelURL: panelLink}, start)
	}
	defer closeEngine()
	if localLab {
		var openFolder func(string) error
		var showLogs func() error
		if mode != "mas" && runtime.GOOS == "darwin" {
			openFolder = openProtectedTerminal
			if folder := app.LogFolder(); folder != "" {
				showLogs = func() error { return showInFinder(folder) }
			}
		}
		handler, closeClient, err := demo.Handler(app, proxyListener.Addr().String(), uiListener.Addr().String(), access, demo.PanelOptions{OpenTerminal: openFolder, ShowLogs: showLogs, Logins: logins, ClaudeSetup: claudeSetup})
		if err != nil {
			uiListener.Close()
			return err
		}
		defer closeClient()
		uiServer := start(uiListener, handler)
		defer uiServer.Close()
		fmt.Printf("Open workspace: %s (one-time link: %s)\n", workspaceURL, panelLink())
		if mode == "mas" {
			fmt.Printf("Coding agent gateway (Claude Code, Codex): http://%s (copy the setup from the menu or the panel)\n", gatewayListener.Addr())
		} else if openFolder != nil {
			fmt.Println("Coding agents: run phantom in a terminal, or click \"Open protected terminal\" in the workspace.")
		} else {
			fmt.Println("Demo mode. No external API calls.")
		}
		if *openBrowser {
			if err := openURL(panelLink()); err != nil {
				fmt.Println("Could not open the browser. Open the workspace URL manually.")
			}
		}
	}
	fmt.Println("Press Ctrl+C to stop. Session maps and the CA private key remain in process memory.")
	if mode == "mas" {
		fmt.Println("claude-setup: " + strings.ReplaceAll(claudeSetup, "\n", "; "))
		go appCommands(os.Stdin, panelLink, app.RequestShutdown)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	appLog.Logger().Info("app started", "version", buildVersion(), "mode", mode)
	select {
	case <-ctx.Done():
		appLog.Logger().Info("app stopped", "cause", "interrupt or termination signal")
		return nil
	case <-app.ShutdownRequested():
		fmt.Println("PhantomPrompt: stopped (panel Stop button or phantom stop).")
		appLog.Logger().Info("app stopped", "cause", "panel Stop button or phantom stop")
		return nil
	case serveErr := <-errorsChannel:
		return fmt.Errorf("local server stopped unexpectedly: %w", serveErr)
	}
}
