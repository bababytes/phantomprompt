package proxy

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"phantomprompt/internal/applog"
	"phantomprompt/internal/mask"
)

func TestDeadTerminalSessionsAreSwept(t *testing.T) {
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	finished := exec.Command("true")
	if err := finished.Run(); err != nil {
		t.Fatal(err)
	}
	dead, _, err := app.CreateCLISession("claude", finished.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	live, _, err := app.CreateCLISession("claude", 0)
	if err != nil {
		t.Fatal(err)
	}
	app.turns.groups = map[string]*turnGroup{dead: {}, live: {}, "gone": {}}
	app.sweepTerminals()
	if _, _, err := app.Sessions.Acquire(dead, "cli"); err == nil {
		t.Fatal("session of an exited terminal is still usable")
	}
	session, release, err := app.Sessions.Acquire(live, "cli")
	if err != nil || session == nil {
		t.Fatal("live terminal lost its session")
	}
	release()
	if len(app.turns.groups) != 1 || app.turns.groups[live] == nil {
		t.Fatalf("turn groups not pruned: %v", app.turns.groups)
	}
}

func TestTerminalClosedInPanel(t *testing.T) {
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	handler := app.Extension(LocalAddresses{Proxy: "127.0.0.1:18080"})
	terminal, secret, err := app.CreateCLISession("zsh", 4242)
	if err != nil {
		t.Fatal(err)
	}
	gateway, _, err := app.CreateCLISession("claude-gateway", 0)
	if err != nil {
		t.Fatal(err)
	}
	sessions := app.CLISessions()
	view := func(label string) CLISession {
		return sessions[slices.IndexFunc(sessions, func(s CLISession) bool { return s.Label == label })]
	}
	shown, agent := view("zsh"), view("claude-gateway")
	if shown.ID == "" || shown.ID == terminal || shown.ID == agent.ID || agent.ID == gateway {
		t.Fatalf("terminals not identified by their own id: %+v", sessions)
	}
	if app.CloseTerminal(agent.ID) || app.CloseTerminal("unknown") || app.CloseTerminal("") {
		t.Fatal("closed a terminal that has no launcher or does not exist")
	}
	if !app.CloseTerminal(shown.ID) || app.CloseTerminal(shown.ID) {
		t.Fatal("terminal not closed exactly once")
	}
	if sessions := app.CLISessions(); len(sessions) != 1 || sessions[0].Label != "claude-gateway" {
		t.Fatalf("closed terminal still listed: %+v", sessions)
	}
	if _, ok := app.proxyCredentials("Basic " + base64.StdEncoding.EncodeToString([]byte("phantomprompt+"+terminal+":"+secret))); ok {
		t.Fatal("closed terminal still authorizes the proxy")
	}
	if _, _, err := app.Sessions.Acquire(terminal, "cli"); err == nil {
		t.Fatal("closed terminal keeps its token map")
	}
	for range 2 {
		if code := extensionCall(t, handler, "/v1/cli/touch", map[string]string{"session": terminal}, nil); code != http.StatusGone {
			t.Fatalf("launcher not told that its terminal was closed: %d", code)
		}
	}
	if code := extensionCall(t, handler, "/v1/cli/touch", map[string]string{"session": gateway}, nil); code != http.StatusNoContent {
		t.Fatalf("open session told to close: %d", code)
	}
	if extensionCall(t, handler, "/v1/cli/close", map[string]string{"session": terminal}, nil) != http.StatusNoContent || app.cliSessions.closedInPanel(terminal) {
		t.Fatal("launcher exit did not forget the closed terminal")
	}

	finished := exec.Command("true")
	if err := finished.Run(); err != nil {
		t.Fatal(err)
	}
	dead, _, err := app.CreateCLISession("zsh", finished.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	sessions = app.CLISessions()
	if !app.CloseTerminal(sessions[slices.IndexFunc(sessions, func(s CLISession) bool { return s.PID == finished.Process.Pid })].ID) {
		t.Fatal("terminal without heartbeat not closed")
	}
	app.sweepTerminals()
	if app.cliSessions.closedInPanel(dead) {
		t.Fatal("closed terminal of an exited launcher is never forgotten")
	}
}

func TestTerminalOpenedBeforeRestartIsShownDisconnected(t *testing.T) {
	folder := t.TempDir()
	log, err := applog.Open(folder)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{Log: log})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	app.cliSessions.now = func() time.Time { return now }
	handler := app.Extension(LocalAddresses{Proxy: "127.0.0.1:18080"})
	touch := func(body map[string]string) int { return extensionCall(t, handler, "/v1/cli/touch", body, nil) }
	const shell, launcher = "0123456789abcdef0123456789abcdef", "fedcba9876543210fedcba9876543210"

	for range 2 {
		if code := touch(map[string]string{"session": shell, "label": "zsh", "pid": "4242", "folder": "/tmp/work"}); code != http.StatusNotFound {
			t.Fatalf("launcher not told that its terminal is disconnected: %d", code)
		}
	}
	for _, id := range []string{"", "zsh", shell + "0", "0123456789ABCDEF0123456789ABCDEF"} {
		if code := touch(map[string]string{"session": id, "label": "zsh"}); code != http.StatusNotFound {
			t.Fatalf("invalid session %q: %d", id, code)
		}
	}
	sessions := app.CLISessions()
	if len(sessions) != 1 {
		t.Fatalf("invalid sessions listed or a terminal listed twice: %+v", sessions)
	}
	if s := sessions[0]; !s.Disconnected || s.Active || s.Label != "zsh" || s.PID != 4242 || s.Folder != "/tmp/work" || s.ID == "" || s.ID == shell {
		t.Fatalf("terminal from before the restart not shown as disconnected: %+v", s)
	}
	data, err := os.ReadFile(filepath.Join(folder, time.Now().Format(time.DateOnly)+".log"))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(data), `msg="terminal from before a restart is disconnected" terminal=zsh pid=4242 folder=/tmp/work`); lines != 1 || strings.Contains(string(data), shell) {
		t.Fatalf("disconnected terminal not logged exactly once without its session: %s", data)
	}
	for _, secret := range []string{"", "00000000000000000000000000000000"} {
		if _, ok := app.proxyCredentials("Basic " + base64.StdEncoding.EncodeToString([]byte("phantomprompt+"+shell+":"+secret))); ok {
			t.Fatal("disconnected terminal authorizes the proxy")
		}
	}

	if code := touch(map[string]string{"session": launcher}); code != http.StatusNotFound {
		t.Fatalf("launcher without terminal details: %d", code)
	}
	sessions = app.CLISessions()
	old := sessions[slices.IndexFunc(sessions, func(s CLISession) bool { return s.Label == "cli" })]
	if !old.Disconnected || old.PID != 0 || !app.CloseTerminal(old.ID) || app.CloseTerminal(old.ID) {
		t.Fatalf("disconnected terminal without a pid not closed exactly once: %+v", old)
	}
	if code := touch(map[string]string{"session": launcher}); code != http.StatusGone {
		t.Fatalf("launcher not told that its terminal was closed: %d", code)
	}
	if sessions := app.CLISessions(); len(sessions) != 1 || sessions[0].Label != "zsh" {
		t.Fatalf("closed terminal still listed: %+v", sessions)
	}

	now = now.Add(cliActiveWindow - time.Second)
	touch(map[string]string{"session": shell, "label": "zsh", "pid": "4242"})
	now = now.Add(cliActiveWindow - time.Second)
	if len(app.CLISessions()) != 1 {
		t.Fatal("disconnected terminal that still sends heartbeats dropped")
	}
	now = now.Add(time.Second)
	if sessions := app.CLISessions(); len(sessions) != 0 {
		t.Fatalf("terminals without heartbeats kept: %+v", sessions)
	}

	touch(map[string]string{"session": shell, "label": "zsh", "pid": "4242"})
	if extensionCall(t, handler, "/v1/cli/close", map[string]string{"session": shell}, nil) != http.StatusNoContent || len(app.CLISessions()) != 0 {
		t.Fatal("launcher exit did not forget the disconnected terminal")
	}
}

func TestToolStartedInAProtectedTerminalIsShownInItsRow(t *testing.T) {
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	handler := app.Extension(LocalAddresses{Proxy: "127.0.0.1:18080"})
	open := func(body map[string]any) string {
		var reply map[string]string
		if code := extensionCall(t, handler, "/v1/cli/session", body, &reply); code != http.StatusOK {
			t.Fatalf("session for %v: %d", body, code)
		}
		return reply["session"]
	}
	shell := open(map[string]any{"label": "zsh", "pid": 4242, "folder": "/tmp/work"})
	claude := open(map[string]any{"label": "claude", "pid": 4343, "folder": "/tmp/work", "terminal": shell})
	open(map[string]any{"label": "codex", "pid": 4444, "terminal": "0123456789abcdef0123456789abcdef"})
	open(map[string]any{"label": "copilot", "pid": 4545, "terminal": "not-a-session"})
	open(map[string]any{"label": "nested", "pid": 4646, "terminal": claude})
	self := open(map[string]any{"label": "self", "pid": 4747})
	app.cliSessions.setTerminal(self, self)
	app.cliSessions.record(claude, "Claude Code", func(s *CLISession) {
		s.Prompts, s.Masked, s.Blocked, s.TokenOverhead, s.InputTokens, s.UsageOverhead = 2, 1, 1, 4, 42050, 4
	})

	rows := func() []string {
		labels := []string{}
		for _, s := range app.CLISessions() {
			labels = append(labels, s.Label)
		}
		slices.Sort(labels)
		return labels
	}
	if labels := rows(); !slices.Equal(labels, []string{"codex", "copilot", "nested", "self", "zsh"}) {
		t.Fatalf("rows %v", labels)
	}
	sessions := app.CLISessions()
	row := sessions[slices.IndexFunc(sessions, func(s CLISession) bool { return s.Label == "zsh" })]
	if row.PID != 4242 || !row.Active || row.Prompts != 2 || row.Masked != 1 || row.Blocked != 1 || row.TokenOverhead != 4 || row.InputTokens != 42050 || row.UsageOverhead != 4 || !slices.Equal(row.Services, []string{"Claude Code"}) || row.LastActivity == nil {
		t.Fatalf("terminal row does not include the tool started in it: %+v", row)
	}

	if !app.CloseTerminal(row.ID) {
		t.Fatal("terminal not closed")
	}
	if code := extensionCall(t, handler, "/v1/cli/touch", map[string]string{"session": claude}, nil); code != http.StatusGone {
		t.Fatalf("tool in a closed terminal not told to stop: %d", code)
	}
	if _, _, err := app.Sessions.Acquire(claude, "cli"); err == nil {
		t.Fatal("tool in a closed terminal keeps its token map")
	}
	if labels := rows(); !slices.Equal(labels, []string{"codex", "copilot", "nested", "self"}) {
		t.Fatalf("rows after closing the terminal %v", labels)
	}

	const outer, inner = "00112233445566778899aabbccddeeff", "ffeeddccbbaa99887766554433221100"
	extensionCall(t, handler, "/v1/cli/touch", map[string]string{"session": outer, "label": "zsh", "pid": "5252"}, nil)
	extensionCall(t, handler, "/v1/cli/touch", map[string]string{"session": inner, "label": "claude", "pid": "5353", "terminal": outer}, nil)
	disconnected := slices.DeleteFunc(app.CLISessions(), func(s CLISession) bool { return !s.Disconnected })
	if len(disconnected) != 1 || disconnected[0].Label != "zsh" {
		t.Fatalf("terminal from before a restart shown with the tool in it as two rows: %+v", disconnected)
	}
}

func TestClearHistoryResetsTotalsAndTerminalCounters(t *testing.T) {
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	session, _, err := app.CreateCLISession("claude", 4242)
	if err != nil {
		t.Fatal(err)
	}
	app.recordMasked(mask.Counts{VIN: 1, PESEL: 1, Email: 1, GUID: 1, Fields: 1, Custom: 1, Secret: 1, File: 1, New: 3, TokenOverhead: 4, TypedKinds: map[string]int{"email": 1}, ContextKinds: map[string]int{"vin": 1}})
	app.countPrompt(session, ClaudeCode)
	app.recordInputTokens(session, "Claude Code", 42050, 4, app.resets.Load())
	app.cliSessions.record(session, "Claude Code", func(s *CLISession) { s.Prompts, s.Masked, s.Blocked, s.TokenOverhead = 1, 3, 1, 4 })
	app.Blocked.Add(1)
	app.checks.time(2)
	app.detected.record("Claude Code", true, func(i *Integration) { i.Requests, i.Blocked = 3, 1 })
	control := func(method, path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, nil)
		request.Header.Set(AccessHeader, localSecret)
		w := httptest.NewRecorder()
		app.Control(w, request)
		return w
	}
	counters := []string{"forwarded", "blocked", "vin", "pesel", "email", "fields", "guid", "custom", "secret", "file", "new_values", "typed_values", "context_values", "prompts_sent", "token_overhead", "input_tokens", "usage_overhead"}
	status := func() map[string]any {
		var totals map[string]any
		if err := json.Unmarshal(control(http.MethodGet, "/status").Body.Bytes(), &totals); err != nil {
			t.Fatal(err)
		}
		return totals
	}
	for _, name := range counters {
		if status()[name] == float64(0) {
			t.Fatalf("%s not counted before the reset", name)
		}
	}
	if code := control(http.MethodDelete, "/events").Code; code != http.StatusNoContent {
		t.Fatalf("clear history: %d", code)
	}
	totals := status()
	for _, name := range counters {
		if totals[name] != float64(0) {
			t.Fatalf("%s = %v after Clear history", name, totals[name])
		}
	}
	if stats := app.PromptStats(); stats != (PromptStats{}) {
		t.Fatalf("request statistics after Clear history %+v", stats)
	}
	if integrations := app.Integrations(); len(integrations) != 1 || integrations[0].Prompts != 0 || integrations[0].Requests != 0 || integrations[0].Blocked != 0 || integrations[0].LastSeen == nil || !integrations[0].Protected {
		t.Fatalf("connections after Clear history %+v", integrations)
	}
	terminal := app.CLISessions()[0]
	if terminal.Prompts != 0 || terminal.Requests != 0 || terminal.Masked != 0 || terminal.Blocked != 0 || terminal.TokenOverhead != 0 || terminal.InputTokens != 0 || terminal.UsageOverhead != 0 || !slices.Equal(terminal.Services, []string{"Claude Code"}) || terminal.PID != 4242 {
		t.Fatalf("terminal after Clear history %+v", terminal)
	}
}
