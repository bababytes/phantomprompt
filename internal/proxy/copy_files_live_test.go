package proxy

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingAnthropic stands between the gateway and the real api.anthropic.com: it keeps every
// request body the gateway sends, so the test can prove what left the machine.
type recordingAnthropic struct {
	mu     sync.Mutex
	bodies []string
	client *http.Client
}

func (f *recordingAnthropic) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.bodies = append(f.bodies, string(body))
	f.mu.Unlock()
	out, err := http.NewRequestWithContext(r.Context(), r.Method, "https://api.anthropic.com"+r.URL.RequestURI(), bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	out.Header = r.Header.Clone()
	response, err := f.client.Do(out)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	for name, values := range response.Header {
		w.Header()[name] = values
	}
	w.WriteHeader(response.StatusCode)
	buffer := make([]byte, 32<<10)
	for {
		n, err := response.Body.Read(buffer)
		if n > 0 {
			w.Write(buffer[:n])
			w.(http.Flusher).Flush()
		}
		if err != nil {
			return
		}
	}
}

// TestLiveClaudeCopiesAProject runs the real Claude Code against this source's gateway and the real
// Anthropic API. It costs model usage, so it runs only when asked:
//
//	PHANTOMPROMPT_LIVE_CLAUDE=1 go test -run TestLiveClaudeCopiesAProject -v -timeout 20m ./internal/proxy
//
// The generated project stays in the logged folder for inspection.
func TestLiveClaudeCopiesAProject(t *testing.T) {
	if os.Getenv("PHANTOMPROMPT_LIVE_CLAUDE") != "1" {
		t.Skip("set PHANTOMPROMPT_LIVE_CLAUDE=1 to run Claude Code against the real API")
	}
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not installed")
	}
	seed := fixtureSeed(t)
	fixture := newProjectFixture(seed)
	files := fixture.files()
	dir, err := os.MkdirTemp("", "phantomprompt-live-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("project folder %s", dir)
	var paths []string
	for _, file := range files {
		path := filepath.Join(dir, file.path)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(file.content), 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, file.path)
	}

	upstream := &recordingAnthropic{client: &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true, DisableCompression: true}}}
	app, _ := gatewayFixtureWith(t, upstream)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: app.Gateway(listener.Addr().String())}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	session, secret, err := app.CreateCLISession("claude", 0)
	if err != nil {
		t.Fatal(err)
	}

	prompt := "Copy these files into a new folder named copy/, keeping their relative paths: " + strings.Join(paths, ", ") +
		". Read each file with the Read tool and create the copy with the Write tool, with exactly the same content. Do not change, fix or reformat anything."
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, claude, "-p", prompt, "--permission-mode", "acceptEdits", "--allowedTools", "Read", "Write", "Glob", "LS")
	command.Dir = dir
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if name != "ANTHROPIC_BASE_URL" && !strings.HasPrefix(name, "CLAUDE_CODE_USE_") && !strings.EqualFold(name, "HTTPS_PROXY") && !strings.EqualFold(name, "HTTP_PROXY") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "ANTHROPIC_BASE_URL="+ClaudeBaseURL(listener.Addr().String(), session, secret),
		"DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1", "CLAUDE_CODE_GATEWAY_HINT_HEADERS=1")
	output, err := command.CombinedOutput()
	t.Logf("claude said:\n%s", output)
	if err != nil {
		t.Fatalf("claude failed: %v", err)
	}

	upstream.mu.Lock()
	sent := strings.Join(upstream.bodies, "\n")
	requests := len(upstream.bodies)
	upstream.mu.Unlock()
	for _, value := range fixture.values {
		if strings.Contains(sent, value.value) {
			t.Errorf("%s value reached Anthropic: %q", value.kind, value.value)
		}
	}

	want, detectors := map[string]int{}, map[string]int{}
	for _, value := range fixture.values {
		want[value.kind]++
		if value.detector != "" {
			detectors[value.detector]++
		}
	}
	learned, rules := map[string]int{}, map[string]int{}
	for _, event := range app.Events.Snapshot() {
		for kind, count := range event.Matches.NewKinds {
			learned[kind] += count
		}
		for name, use := range event.Rules {
			rules[name] += use.Values
		}
		if event.Reason == "placeholders_not_restored" {
			t.Errorf("an answer kept %d placeholders", event.Unrestored)
		}
	}
	keys := map[string]string{"vin": "vin", "email": "email", "guid": "guid", "secret": "secret", "fields": "fields"}
	for kind, key := range keys {
		t.Logf("panel: %-6s %2d new values, %2d written", kind, learned[key], want[kind])
		if learned[key] < want[kind] {
			t.Errorf("panel counted %d new %s values, %d were written", learned[key], kind, want[kind])
		}
	}
	for detector, count := range detectors {
		t.Logf("panel: %-42q %2d new values, %2d written", detector, rules[detector], count)
	}
	t.Logf("%d model requests went through the gateway", requests)

	for _, file := range files {
		copied, err := os.ReadFile(filepath.Join(dir, "copy", file.path))
		switch {
		case err != nil:
			t.Errorf("no copy of %s: %v", file.path, err)
		case string(copied) != file.content:
			t.Errorf("copy of %s differs\n--- original\n%s\n--- copy\n%s", file.path, file.content, copied)
		}
	}
}
