package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func extensionCall(t *testing.T, handler http.Handler, path string, payload any, reply any) int {
	t.Helper()
	data, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(data))))
	if reply != nil && w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), reply); err != nil {
			t.Fatal(err)
		}
	}
	return w.Code
}

func TestProtectionToggle(t *testing.T) {
	app, err := New(Config{Integrations: []string{CopilotCLI}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if !app.Protection() {
		t.Fatal("protection should default to on")
	}
	// With protection on, a sensitive CLI body is blocked.
	r := httptest.NewRequest(http.MethodPost, "https://api.individual.githubcopilot.com/v1/messages", strings.NewReader(`{"messages":[{"role":"user","content":[{"type":"text","text":"Check `+syntheticVIN+`"}]}]}`))
	r.Header.Set("Content-Type", "application/json")
	if _, resp := app.CLI(r, tunnel{adapter: CopilotCLI}); resp == nil || resp.StatusCode < 400 {
		t.Fatal("sensitive CLI body should be handled while protection is on")
	}

	app.SetProtection(false)
	if app.Protection() {
		t.Fatal("protection should be off")
	}
	// CLI passes through untouched when off.
	r2 := httptest.NewRequest(http.MethodPost, "https://api.individual.githubcopilot.com/v1/messages", strings.NewReader(`{"messages":[{"role":"user","content":[{"type":"text","text":"Check `+syntheticVIN+`"}]}]}`))
	out, resp := app.CLI(r2, tunnel{adapter: CopilotCLI})
	if resp != nil || out != r2 {
		t.Fatal("CLI should pass through unchanged while protection is off")
	}

	app.SetProtection(true)
	if !app.Protection() {
		t.Fatal("protection should be back on")
	}
	reasons := map[string]bool{}
	for _, e := range app.Events.Snapshot() {
		reasons[e.Reason] = true
	}
	if !reasons["protection_disabled"] || !reasons["protection_enabled"] {
		t.Fatal("toggle events not recorded")
	}
}

func TestRequestShutdown(t *testing.T) {
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	select {
	case <-app.ShutdownRequested():
		t.Fatal("shutdown signalled before request")
	default:
	}
	app.RequestShutdown()
	app.RequestShutdown() // idempotent, must not panic
	select {
	case <-app.ShutdownRequested():
	default:
		t.Fatal("shutdown not signalled")
	}
}
