package proxy

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCLIRegistryLifecycle(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	registry := cliRegistry{now: func() time.Time { return now }}
	registry.add("a", "secret-a", "claude", 101)
	now = now.Add(time.Second)
	registry.add("b", "secret-b", "rm -rf /; echo", -5)
	registry.record("a", "Claude Code", func(s *CLISession) { s.Prompts++; s.Masked += 2 })
	registry.record("a", "Claude Code", func(s *CLISession) { s.Blocked++ })
	registry.record("unknown", "Claude Code", func(s *CLISession) { s.Prompts++ })

	sessions := registry.snapshot()
	if len(sessions) != 2 || sessions[0].Label != "claude" || sessions[0].Prompts != 1 || sessions[0].Masked != 2 || sessions[0].Blocked != 1 || len(sessions[0].Services) != 1 || !sessions[0].Active {
		t.Fatalf("sessions %+v", sessions)
	}
	if sessions[1].Label != "cli" || sessions[1].PID != 0 {
		t.Fatalf("untrusted label or pid kept: %+v", sessions[1])
	}
	encoded, _ := json.Marshal(sessions)
	for _, private := range []string{"secret-a", "secret-b", `"a"`, `"b"`} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("snapshot exposes %s: %s", private, encoded)
		}
	}

	now = now.Add(2 * time.Minute)
	registry.touch("a", false)
	if sessions := registry.snapshot(); !sessions[0].Active || sessions[1].Active {
		t.Fatalf("heartbeat state %+v", sessions)
	}
	registry.touch("a", true)
	if sessions := registry.snapshot(); sessions[0].Active || !sessions[0].Expired {
		t.Fatalf("expired session shown as active: %+v", sessions[0])
	}
	now = now.Add(cliRetention)
	registry.touch("a", false)
	if sessions := registry.snapshot(); len(sessions) != 1 || sessions[0].Label != "claude" {
		t.Fatalf("silent session not pruned: %+v", sessions)
	}
	registry.remove("a")
	if len(registry.snapshot()) != 0 {
		t.Fatal("closed session still listed")
	}
}

func TestCLISessionSecretAuthorizesOnlyItsSession(t *testing.T) {
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	first, firstSecret, err := app.CreateCLISession("claude", 1)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := app.CreateCLISession("copilot", 2)
	if err != nil {
		t.Fatal(err)
	}
	basic := func(user, password string) string {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
	}
	for _, tc := range []struct {
		user, password, session string
		ok                      bool
	}{
		{"phantomprompt+" + first, firstSecret, first, true},
		{"phantomprompt+" + second, firstSecret, "", false},
		{"phantomprompt", firstSecret, "", false},
		{"phantomprompt+" + first, "wrong", "", false},
		{"phantomprompt", localSecret, "", true},
		{"phantomprompt+" + second, localSecret, second, true},
	} {
		session, ok := app.proxyCredentials(basic(tc.user, tc.password))
		if ok != tc.ok || session != tc.session {
			t.Errorf("%s: session %q ok %v", tc.user, session, ok)
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	r.Header.Set("Proxy-Authorization", basic("phantomprompt+"+second, firstSecret))
	app.ServeHTTP(w, r)
	if w.Code != http.StatusProxyAuthRequired {
		t.Fatalf("foreign session secret accepted: %d", w.Code)
	}
}

func TestCLISessionEngineEndpoints(t *testing.T) {
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	get := func(handler http.Handler, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	handler := app.Extension(LocalAddresses{Proxy: "127.0.0.1:18080", PanelURL: func() string { return "http://127.0.0.1:18090" }})
	if body := get(handler, "/v1/cli").Body.String(); strings.Contains(body, localSecret) || !strings.Contains(body, "BEGIN CERTIFICATE") {
		t.Fatalf("CLI settings: %s", body)
	}
	if body := get(handler, "/v1/panel").Body.String(); !strings.Contains(body, `"url":"http://127.0.0.1:18090"`) {
		t.Fatalf("panel: %s", body)
	}
	if code := get(app.Extension(LocalAddresses{Proxy: "127.0.0.1:18080"}), "/v1/panel").Code; code != http.StatusNotFound {
		t.Fatalf("panel without workspace: %d", code)
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/cli/session", nil))
	var anonymous map[string]string
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &anonymous) != nil || anonymous["session"] == "" || anonymous["secret"] == "" || anonymous["secret"] == localSecret {
		t.Fatalf("session without body: %d %s", w.Code, w.Body)
	}
	var labelled map[string]string
	if extensionCall(t, handler, "/v1/cli/session", cliSessionRequest{Label: "claude", PID: 7}, &labelled) != 200 || labelled["secret"] == anonymous["secret"] {
		t.Fatal("labelled session not created")
	}
	if extensionCall(t, handler, "/v1/cli/session", map[string]any{"label": "claude", "token": "x"}, nil) != http.StatusBadRequest {
		t.Fatal("unknown session field accepted")
	}
	if extensionCall(t, handler, "/v1/cli/touch", map[string]string{"session": labelled["session"]}, nil) != 204 {
		t.Fatal("touch rejected")
	}
	sessions := app.CLISessions()
	index := slices.IndexFunc(sessions, func(s CLISession) bool { return s.Label == "claude" })
	if len(sessions) != 2 || index < 0 || sessions[index].PID != 7 || sessions[index].Expired || !sessions[index].Active {
		t.Fatalf("sessions %+v", sessions)
	}
	if extensionCall(t, handler, "/v1/cli/close", map[string]string{"session": labelled["session"]}, nil) != 204 {
		t.Fatal("close rejected")
	}
	if _, ok := app.proxyCredentials("Basic " + base64.StdEncoding.EncodeToString([]byte("phantomprompt+"+labelled["session"]+":"+labelled["secret"]))); ok {
		t.Fatal("closed session secret still authorizes the proxy")
	}
	if sessions := app.CLISessions(); len(sessions) != 1 || sessions[0].Label != "cli" {
		t.Fatalf("closed session still listed: %+v", sessions)
	}
}

func TestClientIdentifiersComeFromIdentityHeaders(t *testing.T) {
	header := http.Header{}
	header.Set("x-claude-code-prompt-id", "00000000-0000-4000-8000-00000000000a")
	header.Set("X-Claude-Code-Agent-Id", "agent 00000000-0000-4000-8000-00000000000b")
	header.Set("X-Client-Session-Id", "00000000-0000-4000-8000-00000000000c")
	header.Set("X-Unrelated", "00000000-0000-4000-8000-00000000000d")
	ids := clientIdentifiers(header)
	if len(ids) != 3 || slices.Contains(ids, "00000000-0000-4000-8000-00000000000d") {
		t.Fatalf("identifiers %q", ids)
	}
}

func TestCLISessionFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	handler := app.Extension(LocalAddresses{Proxy: "127.0.0.1:18080"})
	for folder, want := range map[string]string{home + "/work/project/": "~/work/project", "/opt/app": "/opt/app", "relative": "", "/bad\x07name": ""} {
		var reply map[string]string
		if extensionCall(t, handler, "/v1/cli/session", cliSessionRequest{Label: "claude", Folder: folder}, &reply) != 200 {
			t.Fatal("session not created")
		}
		if got := app.cliSessions.folder(reply["session"]); got != want {
			t.Errorf("%q: folder %q, want %q", folder, got, want)
		}
	}
}

func TestEngineShutdownEndpoint(t *testing.T) {
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if code := extensionCall(t, app.Extension(LocalAddresses{Proxy: "127.0.0.1:18080"}), "/v1/shutdown", nil, nil); code != http.StatusNoContent {
		t.Fatalf("status %d", code)
	}
	select {
	case <-app.ShutdownRequested():
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown was not requested")
	}
}
