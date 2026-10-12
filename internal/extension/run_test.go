package extension

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"phantomprompt/internal/demo"
)

const syntheticPEM = "-----BEGIN CERTIFICATE-----\nU1lOVEhFVElD\n-----END CERTIFICATE-----\n"

func shortSocket(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "pp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	return filepath.Join(directory, "engine.sock")
}

func TestProxyEnvironmentAffectsOnlyChild(t *testing.T) {
	settings := CLISettings{Proxy: "127.0.0.1:18080", User: "phantomprompt", CA: syntheticPEM}
	session := CLISession{ID: "0123456789abcdef0123456789abcdef", Secret: "synthetic-secret"}
	env := ProxyEnvironment([]string{"PATH=/bin", "https_proxy=http://corporate:3128", "NO_PROXY=*", "NODE_EXTRA_CA_CERTS=/old.pem", "HOME=/synthetic"}, settings, session, "/tmp/ca.pem")
	proxyURL := "http://phantomprompt+0123456789abcdef0123456789abcdef:synthetic-secret@127.0.0.1:18080"
	for _, want := range []string{"PATH=/bin", "HOME=/synthetic", "HTTPS_PROXY=" + proxyURL, "https_proxy=" + proxyURL, "NO_PROXY=localhost,127.0.0.1,::1", "NODE_EXTRA_CA_CERTS=/tmp/ca.pem"} {
		if !slices.Contains(env, want) {
			t.Fatalf("missing %s in %v", want, env)
		}
	}
	for _, unwanted := range []string{"https_proxy=http://corporate:3128", "NO_PROXY=*", "NODE_EXTRA_CA_CERTS=/old.pem"} {
		if slices.Contains(env, unwanted) {
			t.Fatalf("inherited %s", unwanted)
		}
	}
}

func TestWriteCAKeepsExistingBundle(t *testing.T) {
	existing := filepath.Join(t.TempDir(), "corporate.pem")
	os.WriteFile(existing, []byte("CORPORATE"), 0600)
	path, cleanup, err := WriteCA(CLISettings{CA: syntheticPEM}, existing)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	data, _ := os.ReadFile(path)
	if info.Mode().Perm() != 0600 || !strings.Contains(string(data), "BEGIN CERTIFICATE") || !strings.Contains(string(data), "CORPORATE") {
		t.Fatal("temporary CA bundle incomplete or readable by others")
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("temporary CA bundle not removed")
	}
}

func TestFetchCLISettings(t *testing.T) {
	socket := shortSocket(t)
	if _, err := FetchCLISettings(socket); err == nil {
		t.Fatal("settings returned without a running engine")
	}
	listener, err := Listen(socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(CLISettings{Proxy: "127.0.0.1:18080", User: "phantomprompt", CA: syntheticPEM, Integrations: []string{"copilot-cli"}})
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	settings, err := FetchCLISettings(socket)
	if err != nil || settings.Proxy != "127.0.0.1:18080" || settings.Integrations[0] != "copilot-cli" {
		t.Fatalf("settings %+v, err %v", settings, err)
	}
}

func fakeEngine(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	socket := shortSocket(t)
	listener, err := Listen(socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	return socket
}

func TestOpenCLISessionSendsLabelAndReturnsSecret(t *testing.T) {
	var received map[string]any
	closed := make(chan string, 1)
	socket := fakeEngine(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/cli/session":
			json.NewDecoder(r.Body).Decode(&received)
			json.NewEncoder(w).Encode(map[string]string{"session": "0123456789abcdef0123456789abcdef", "secret": "synthetic-secret"})
		case "/v1/cli/close":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			closed <- body["session"]
		}
	})
	session, closeSession, err := OpenCLISession(socket, "claude", 4242, "/work/project", "fedcba9876543210fedcba9876543210")
	if err != nil || session.ID != "0123456789abcdef0123456789abcdef" || session.Secret != "synthetic-secret" {
		t.Fatalf("session %+v err %v", session, err)
	}
	if received["label"] != "claude" || received["pid"] != float64(4242) || received["folder"] != "/work/project" || received["terminal"] != "fedcba9876543210fedcba9876543210" {
		t.Fatalf("engine received %v", received)
	}
	closeSession()
	if <-closed != session.ID {
		t.Fatal("session not closed")
	}
}

func TestOutdatedEngineIsReported(t *testing.T) {
	socket := fakeEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/cli/session" {
			json.NewEncoder(w).Encode(map[string]string{"session": "0123456789abcdef0123456789abcdef"})
			return
		}
		http.NotFound(w, r)
	})
	if _, _, err := OpenCLISession(socket, "claude", 1, "/work", ""); !errors.Is(err, ErrOutdatedEngine) {
		t.Fatalf("session without secret: %v", err)
	}
	if _, err := PanelURL(socket); !errors.Is(err, ErrOutdatedEngine) {
		t.Fatalf("missing panel endpoint: %v", err)
	}
}

func TestPanelURLAcceptsOnlyLoopback(t *testing.T) {
	enginePanel := "http://" + demo.PanelHost("127.0.0.1:18090") + "/?login=synthetic"
	for address, valid := range map[string]bool{enginePanel: true, "http://localhost:18090": true, "http://127.0.0.1:18090": true, "http://[::1]:18090": true, "https://127.0.0.1:18090": false, "http://example.test:18090": false, "http://localhost.example.test:18090": false, "http://phantomprompt.localhost.example.test:18090": false, "http://user:pass@127.0.0.1:18090": false} {
		socket := fakeEngine(t, func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]string{"url": address})
		})
		panel, err := PanelURL(socket)
		if valid != (err == nil) || valid && panel != address {
			t.Errorf("%s: panel %q err %v", address, panel, err)
		}
	}
}

func TestOpenCLISessionRetriesWithoutFolderOnOlderEngine(t *testing.T) {
	var bodies []map[string]any
	socket := fakeEngine(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		if _, hasFolder := body["folder"]; hasFolder {
			http.Error(w, "Invalid extension request", http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"session": "0123456789abcdef0123456789abcdef", "secret": "s"})
	})
	session, _, err := OpenCLISession(socket, "claude", 1, "/work", "")
	if err != nil || session.Secret != "s" || len(bodies) != 2 {
		t.Fatalf("session %+v err %v bodies %v", session, err, bodies)
	}
}

func TestOpenCLISessionKeepsTheFolderOnAnEngineWithoutTerminals(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rejected []string
		sent     int
		kept     []string
	}{
		{"terminals unknown", []string{"terminal"}, 2, []string{"folder"}},
		{"folders and terminals unknown", []string{"terminal", "folder"}, 3, nil},
	} {
		var bodies []map[string]any
		socket := fakeEngine(t, func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			bodies = append(bodies, body)
			for _, field := range tc.rejected {
				if _, sent := body[field]; sent {
					http.Error(w, "Invalid extension request", http.StatusBadRequest)
					return
				}
			}
			json.NewEncoder(w).Encode(map[string]string{"session": "0123456789abcdef0123456789abcdef", "secret": "s"})
		})
		session, _, err := OpenCLISession(socket, "claude", 1, "/work", "fedcba9876543210fedcba9876543210")
		if err != nil || session.Secret != "s" || len(bodies) != tc.sent {
			t.Fatalf("%s: session %+v err %v bodies %v", tc.name, session, err, bodies)
		}
		for _, field := range tc.kept {
			if _, sent := bodies[len(bodies)-1][field]; !sent {
				t.Fatalf("%s: %s dropped: %v", tc.name, field, bodies)
			}
		}
	}
}

func TestShutdownWaitsUntilEngineStops(t *testing.T) {
	stopped := false
	socket := fakeEngine(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/shutdown":
			stopped = true
			w.WriteHeader(http.StatusNoContent)
		case stopped:
			http.Error(w, "stopping", http.StatusServiceUnavailable)
		default:
			json.NewEncoder(w).Encode(CLISettings{Proxy: "127.0.0.1:1", User: "phantomprompt", CA: syntheticPEM})
		}
	})
	if err := Shutdown(socket); err != nil {
		t.Fatal(err)
	}
	if err := Shutdown(shortSocket(t)); err == nil {
		t.Fatal("shutdown without an engine succeeded")
	}
}

func TestClaudeEnvironmentUsesTheGateway(t *testing.T) {
	settings := CLISettings{Proxy: "127.0.0.1:18080", Gateway: "127.0.0.1:18081", User: "phantomprompt"}
	session := CLISession{ID: "0123456789abcdef0123456789abcdef", Secret: "fedcba9876543210fedcba9876543210"}
	env := strings.Join(ClaudeEnvironment([]string{"PATH=/bin", "HTTPS_PROXY=http://corporate:3128", "ANTHROPIC_BASE_URL=https://old.test", "DISABLE_TELEMETRY=0"}, settings, session), "\n")
	want := "ANTHROPIC_BASE_URL=http://127.0.0.1:18081/claude/s/" + session.ID + "/" + session.Secret
	if !strings.Contains(env, want) || strings.Contains(env, "PROXY") || strings.Contains(env, "old.test") || strings.Contains(env, "DISABLE_TELEMETRY=0") || !strings.Contains(env, "DISABLE_TELEMETRY=1") || !strings.Contains(env, "CLAUDE_CODE_GATEWAY_HINT_HEADERS=1") || !strings.Contains(env, "ENABLE_TOOL_SEARCH=true") {
		t.Fatalf("claude environment:\n%s", env)
	}
	if own := strings.Join(ClaudeEnvironment([]string{"ENABLE_TOOL_SEARCH=false"}, settings, session), "\n"); strings.Contains(own, "ENABLE_TOOL_SEARCH=true") || !strings.Contains(own, "ENABLE_TOOL_SEARCH=false") {
		t.Fatalf("user tool search setting overridden:\n%s", own)
	}
	shell := strings.Join(ProxyEnvironment([]string{"PATH=/bin"}, settings, session, "/tmp/ca.pem"), "\n")
	if !strings.Contains(shell, want) || !strings.Contains(shell, "HTTPS_PROXY=") {
		t.Fatalf("protected shell should proxy other tools and send claude to the gateway:\n%s", shell)
	}
	settings.Gateway = ""
	if old := strings.Join(ProxyEnvironment([]string{"PATH=/bin"}, settings, session, "/tmp/ca.pem"), "\n"); strings.Contains(old, "ANTHROPIC_BASE_URL") {
		t.Fatal("engine without a gateway must keep the proxy setup")
	}
}

func TestSocketPathFitsUnixLimit(t *testing.T) {
	t.Setenv("HOME", "/Users/"+strings.Repeat("long-user-name-", 6))
	path, err := SocketPath()
	if err != nil || len(path) > maxSocketPath || !strings.HasSuffix(path, "engine.sock") {
		t.Fatalf("socket path %q (%d bytes): %v", path, len(path), err)
	}
}

func TestClaudeDenyArgumentsForBlockedFolders(t *testing.T) {
	folder := t.TempDir()
	args := ClaudeDenyArguments([]string{folder}, []string{"-p", "hi"})
	if len(args) != 4 || args[0] != "--settings" || !strings.Contains(args[1], `"Read(//`+strings.TrimPrefix(folder, "/")+`/**)"`) || !strings.Contains(args[1], `"Edit(//`) || args[2] != "-p" {
		t.Fatalf("args %q", args)
	}
	if kept := ClaudeDenyArguments([]string{folder}, []string{"--settings", "own.json"}); len(kept) != 2 {
		t.Fatal("user settings flag was overridden")
	}
	if none := ClaudeDenyArguments(nil, []string{"-p"}); len(none) != 1 {
		t.Fatal("arguments changed without blocked folders")
	}
}

func TestCodexArgumentsAddAGatewayProvider(t *testing.T) {
	settings := CLISettings{Gateway: "127.0.0.1:18081"}
	session := CLISession{ID: "0123456789abcdef0123456789abcdef", Secret: "fedcba9876543210fedcba9876543210"}
	args, injected := CodexArguments(settings, session, "chatgpt", []string{"exec", "hi"})
	joined := strings.Join(args, " ")
	if !injected || args[len(args)-2] != "exec" || !strings.Contains(joined, `model_provider="phantomprompt"`) || !strings.Contains(joined, `base_url="http://127.0.0.1:18081/codex/s/`+session.ID+"/"+session.Secret+`/backend-api/codex"`) || !strings.Contains(joined, "requires_openai_auth=true") || !strings.Contains(joined, "supports_websockets=false") || !strings.Contains(joined, "analytics.enabled=false") || !strings.Contains(joined, `name="OpenAI"`) || !strings.Contains(joined, "features.enable_request_compression=false") {
		t.Fatalf("codex arguments: %q", args)
	}
	for _, own := range [][]string{{"-c", "model_provider=mine"}, {"--oss"}, {"--profile", "work"}} {
		if kept, injected := CodexArguments(settings, session, "chatgpt", own); injected || len(kept) != len(own) {
			t.Fatalf("user provider choice %q was overridden", own)
		}
	}
}

func TestCodexUpstreamFollowsTheLogin(t *testing.T) {
	home := t.TempDir()
	codex := filepath.Join(home, ".codex")
	os.MkdirAll(codex, 0700)
	if got := CodexUpstream([]string{"HOME=" + home, "OPENAI_API_KEY=sk-synthetic"}); got != "openai" {
		t.Fatalf("API key without a login file: %s", got)
	}
	os.WriteFile(filepath.Join(codex, "auth.json"), []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"synthetic"}}`), 0600)
	if got := CodexUpstream([]string{"HOME=" + home, "OPENAI_API_KEY=sk-synthetic"}); got != "chatgpt" {
		t.Fatalf("ChatGPT login: %s", got)
	}
	os.WriteFile(filepath.Join(codex, "auth.json"), []byte(`{"auth_mode":"apikey"}`), 0600)
	if got := CodexUpstream([]string{"HOME=" + home}); got != "openai" {
		t.Fatalf("API key login: %s", got)
	}
	if got := CodexUpstream([]string{"HOME=" + t.TempDir()}); got != "chatgpt" {
		t.Fatalf("default: %s", got)
	}
}

func TestAgentEnvironmentDropsProxySettings(t *testing.T) {
	env := strings.Join(AgentEnvironment([]string{"PATH=/bin", "HTTPS_PROXY=http://x", "NODE_EXTRA_CA_CERTS=/ca.pem", "HOME=/h"}), "\n")
	if strings.Contains(env, "PROXY") || strings.Contains(env, "NODE_EXTRA_CA_CERTS") || !strings.Contains(env, "HOME=/h") {
		t.Fatalf("agent environment: %s", env)
	}
}

func TestTrustedFolders(t *testing.T) {
	var sent map[string]any
	socket := fakeEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			json.NewDecoder(r.Body).Decode(&sent)
			if sent["path"] == "relative" {
				http.Error(w, "rejected", http.StatusBadRequest)
				return
			}
		}
		json.NewEncoder(w).Encode([]map[string]any{{"path": "/work/src/test", "trusted": true, "files": 3, "lines": 40, "skipped": 0}})
	})
	folders, err := SetTrustedFolder(socket, "/work/src/test", true)
	if err != nil || len(folders) != 1 || folders[0].Files != 3 || sent["path"] != "/work/src/test" || sent["trusted"] != true {
		t.Fatalf("add: %+v %v sent %v", folders, err, sent)
	}
	if folders, err := TrustedFolders(socket); err != nil || len(folders) != 1 {
		t.Fatalf("list: %+v %v", folders, err)
	}
	if _, err := SetTrustedFolder(socket, "relative", true); !errors.Is(err, ErrTrustedFolder) {
		t.Fatalf("rejected folder: %v", err)
	}
	outdated := fakeEngine(t, http.NotFound)
	if _, err := TrustedFolders(outdated); !errors.Is(err, ErrOutdatedEngine) {
		t.Fatalf("older engine: %v", err)
	}
}

func TestTouchReportsTerminalState(t *testing.T) {
	const session = "0123456789abcdef0123456789abcdef"
	var status atomic.Int32
	var sent atomic.Value
	status.Store(http.StatusNoContent)
	socket := fakeEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/cli/touch" {
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			sent.Store(body)
			w.WriteHeader(int(status.Load()))
		}
	})
	touch := func() TerminalState {
		return TouchCLISession(socket, session, "claude", 4242, "/tmp/work", "fedcba9876543210fedcba9876543210")
	}
	if touch() != TerminalConnected {
		t.Fatal("open terminal not reported as connected")
	}
	if body := sent.Load().(map[string]string); body["session"] != session || body["label"] != "claude" || body["pid"] != "4242" || body["folder"] != "/tmp/work" || body["terminal"] != "fedcba9876543210fedcba9876543210" {
		t.Fatalf("touch does not identify the terminal: %v", body)
	}
	status.Store(http.StatusNotFound)
	if touch() != TerminalDisconnected {
		t.Fatal("terminal unknown to a restarted engine not reported")
	}
	status.Store(http.StatusGone)
	if touch() != TerminalClosedInPanel {
		t.Fatal("terminal closed in the panel not reported")
	}
	if TouchCLISession(filepath.Join(t.TempDir(), "missing.sock"), session, "zsh", 4242, "/tmp/work", "") != TerminalConnected {
		t.Fatal("unreachable engine reported as a closed or restarted one")
	}
}

func TestEngineSocketMustBePrivate(t *testing.T) {
	settings := func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(CLISettings{Proxy: "127.0.0.1:18080", Gateway: "127.0.0.1:18081", User: "phantomprompt", CA: syntheticPEM})
	}
	socket := fakeEngine(t, settings)
	if _, err := FetchCLISettings(socket); err != nil {
		t.Fatalf("private socket rejected: %v", err)
	}
	directory := filepath.Dir(socket)
	os.Chmod(directory, 0o777)
	if _, err := FetchCLISettings(socket); !errors.Is(err, ErrUnsafeSocket) {
		t.Fatalf("a folder others can write to was accepted: %v", err)
	}
	if _, err := Listen(filepath.Join(directory, "other.sock")); !errors.Is(err, ErrUnsafeSocket) {
		t.Fatalf("the engine listened in a folder others can write to: %v", err)
	}
	os.Chmod(directory, 0o700)

	link := filepath.Join(t.TempDir(), "engine.sock")
	if err := os.Symlink(socket, link); err != nil {
		t.Fatal(err)
	}
	if _, err := FetchCLISettings(link); !errors.Is(err, ErrUnsafeSocket) {
		t.Fatalf("a symlinked socket was accepted: %v", err)
	}
	plain := filepath.Join(directory, "plain.sock")
	os.WriteFile(plain, nil, 0o600)
	if _, err := FetchCLISettings(plain); !errors.Is(err, ErrUnsafeSocket) {
		t.Fatalf("a regular file was accepted as the socket: %v", err)
	}
	if os.Getuid() != 0 {
		if err := privatePath("/", true); !errors.Is(err, ErrUnsafeSocket) {
			t.Fatalf("a folder owned by another account was accepted: %v", err)
		}
	}
}

func TestEngineSettingsMustPointAtThisMachine(t *testing.T) {
	for _, c := range []struct {
		proxy, gateway string
		valid          bool
	}{
		{"127.0.0.1:18080", "127.0.0.1:18081", true},
		{"[::1]:18080", "", true},
		{"127.0.0.1:18080", "gateway.example.test:80", false},
		{"10.0.0.5:18080", "127.0.0.1:18081", false},
		{"127.0.0.1", "127.0.0.1:18081", false},
		{"127.0.0.1:18080", "127.0.0.1:18081/path", false},
	} {
		socket := fakeEngine(t, func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(CLISettings{Proxy: c.proxy, Gateway: c.gateway, User: "phantomprompt", CA: syntheticPEM})
		})
		if _, err := FetchCLISettings(socket); c.valid != (err == nil) {
			t.Errorf("proxy %q gateway %q: err %v", c.proxy, c.gateway, err)
		}
	}
}
