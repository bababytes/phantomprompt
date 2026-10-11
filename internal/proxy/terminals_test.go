package proxy

import (
	"encoding/base64"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"phantomprompt/internal/applog"
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
