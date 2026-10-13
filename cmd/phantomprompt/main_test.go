package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"phantomprompt/internal/proxy"
)

func TestGatewayKeyIsCreatedOnceAndPrivate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	first, err := gatewayKey()
	if err != nil || len(first) < 32 {
		t.Fatalf("key %q: %v", first, err)
	}
	second, err := gatewayKey()
	if err != nil || second != first {
		t.Fatal("the key changed between starts; a shell profile would stop working")
	}
	path, _ := os.UserConfigDir()
	info, err := os.Stat(path + "/PhantomPrompt/gateway.key")
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("key file %v %v", info, err)
	}
}

func TestClaudeSetupSnippet(t *testing.T) {
	snippet := claudeSetupSnippet("127.0.0.1:47820", "k")
	for _, want := range []string{"ANTHROPIC_BASE_URL=http://127.0.0.1:47820/claude/k/k", "ENABLE_TOOL_SEARCH=true", "DISABLE_TELEMETRY=1"} {
		if !strings.Contains(snippet, want) {
			t.Fatalf("%q missing from %q", want, snippet)
		}
	}
}

func TestAppCommandsStopWithTheApp(t *testing.T) {
	stopped := false
	links := 0
	appCommands(strings.NewReader("panel\nunknown\npanel\n"), func() string { links++; return "http://127.0.0.1:1/login/x" }, func() { stopped = true })
	if links != 2 || !stopped {
		t.Fatalf("links %d stopped %v", links, stopped)
	}
}

func TestShellShimsRunAgentsThroughTheLauncher(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"claude", "codex"} {
		os.WriteFile(bin+"/"+name, []byte("#!/bin/sh\n"), 0755)
	}
	t.Setenv("PATH", bin)
	shims, remove, err := agentShims()
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	for _, name := range []string{"claude", "codex"} {
		script, err := os.ReadFile(shims + "/" + name)
		if err != nil || !strings.Contains(string(script), " run "+name+` "$@"`) {
			t.Fatalf("%s shim: %q %v", name, script, err)
		}
	}
	t.Setenv("PATH", shims+string(os.PathListSeparator)+bin)
	t.Setenv("PHANTOMPROMPT_SHIMS", shims)
	if found, err := lookPath("codex"); err != nil || found != bin+"/codex" {
		t.Fatalf("launcher found its own shim instead of codex: %q %v", found, err)
	}
	if shellQuote("/Applications/Phantom Prompt's.app") != `'/Applications/Phantom Prompt'"'"'s.app'` {
		t.Fatal("unsafe quoting")
	}
}

func TestBannerIsColouredOnlyOnATerminal(t *testing.T) {
	notice := banner{"Your terminal is protected", "Type exit to leave.", false}
	if plain := notice.render(false); plain != "PhantomPrompt: Your terminal is protected. Type exit to leave.\n" {
		t.Fatalf("plain banner %q", plain)
	}
	if coloured := notice.render(true); !strings.HasPrefix(coloured, "\033[1;32m") || !strings.Contains(coloured, "Your terminal is protected") || !strings.HasSuffix(coloured, "\033[0m\n") {
		t.Fatalf("coloured banner %q", coloured)
	}
	file, err := os.Create(t.TempDir() + "/log")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if colorTerminal(file) {
		t.Fatal("escape codes would end up in a redirected log")
	}
}

func TestPhantomInsideAProtectedShellIsNotAnError(t *testing.T) {
	t.Setenv("PHANTOMPROMPT_SHELL", "1")
	if err := runShell(); err != nil {
		t.Fatal(err)
	}
}

func TestSetupSnippetCodexAliasKeepsArguments(t *testing.T) {
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not installed")
	}
	file := t.TempDir() + "/setup"
	os.WriteFile(file, []byte(claudeSetupSnippet("127.0.0.1:47820", "k")+"\n"), 0600)
	out, err := exec.Command("zsh", "-c", "source "+file+"; codex() { print -rl -- \"$@\"; }; eval 'codex exec hi'").Output()
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(out)), "\n")
	want := []string{"-c", "model_provider=phantomprompt", "-c", `model_providers.phantomprompt={name="OpenAI",base_url="http://127.0.0.1:47820/codex/k/k/backend-api/codex",requires_openai_auth=true,supports_websockets=false}`, "-c", "features.enable_request_compression=false", "-c", "analytics.enabled=false", "exec", "hi"}
	if strings.Join(args, "\n") != strings.Join(want, "\n") {
		t.Fatalf("alias expands to %q", args)
	}
}

func TestTerminalScriptOpensTheChosenFolderSafely(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "it's a \"folder\" $HOME `id` ; x")
	os.MkdirAll(folder, 0700)
	self := filepath.Join(root, "fake phantom")
	os.WriteFile(self, []byte("#!/bin/sh\npwd\n"), 0700)
	scripts := filepath.Join(root, "scripts")
	os.Mkdir(scripts, 0700)
	script := filepath.Join(scripts, "PhantomPrompt.command")
	os.WriteFile(script, []byte(terminalScript(self, folder)), 0700)
	out, err := exec.Command(script).Output()
	if err != nil || strings.TrimSpace(string(out)) != folder {
		t.Fatalf("terminal started in %q, want %q: %v", out, folder, err)
	}
	if _, err := os.Stat(scripts); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the temporary script was left behind")
	}
}

func TestGhostBannerFitsAnEightyColumnTerminal(t *testing.T) {
	notice := banner{"Your terminal is protected", "AI tools you start here go through PhantomPrompt:\nclaude, codex, copilot, …\nType exit to leave.", true}
	if plain := notice.render(false); plain != "PhantomPrompt: Your terminal is protected. AI tools you start here go through PhantomPrompt: claude, codex, copilot, … Type exit to leave.\n" {
		t.Fatalf("plain banner %q", plain)
	}
	visible := regexp.MustCompile("\033\\[[0-9;]*m").ReplaceAllString(notice.render(true), "")
	lines := strings.Split(strings.TrimSuffix(visible, "\n"), "\n")
	if len(lines) != len(ghostArt)+1 || !strings.Contains(lines[len(ghostArt)], "━━━━━━━━━━━") || !strings.Contains(lines[0], "▄█████▄") || !strings.Contains(lines[1], "PhantomPrompt · Your terminal is protected") || !strings.HasSuffix(lines[4], "   Type exit to leave.") {
		t.Fatalf("ghost banner:\n%s", visible)
	}
	for _, line := range lines {
		if width := len([]rune(line)); width > 80 {
			t.Fatalf("line of %d columns: %q", width, line)
		}
	}
}

func TestChooseVersion(t *testing.T) {
	for _, c := range []struct{ linked, module, want string }{
		{"v0.2.0", "v9.9.9", "v0.2.0"},
		{"dev", "v0.2.0", "v0.2.0"},
		{"dev", "v0.2.1-0.20261010120000-6d76500abcde+dirty", "v0.2.1-0.20261010120000-6d76500abcde+dirty"},
		{"dev", "(devel)", "dev"},
		{"dev", "", "dev"},
	} {
		if got := chooseVersion(c.linked, c.module); got != c.want {
			t.Errorf("chooseVersion(%q, %q) = %q, want %q", c.linked, c.module, got, c.want)
		}
	}
}

func TestClaudeEndpointError(t *testing.T) {
	for _, c := range []struct {
		env     []string
		refused string
	}{
		{nil, ""},
		{[]string{"ANTHROPIC_BASE_URL="}, ""},
		{[]string{"ANTHROPIC_BASE_URL=https://api.anthropic.com"}, ""},
		{[]string{"ANTHROPIC_BASE_URL=https://api.anthropic.com/"}, ""},
		{[]string{"ANTHROPIC_BASE_URL=http://127.0.0.1:47820/claude/k/x"}, ""},
		{[]string{"ANTHROPIC_BASE_URL=http://localhost:47820/claude/k/x"}, ""},
		{[]string{"ANTHROPIC_BASE_URL=http://[::1]:8081/claude/s/a/b"}, ""},
		{[]string{"ANTHROPIC_BASE_URL=http://127.0.0.1:4000"}, "ANTHROPIC_BASE_URL"},
		{[]string{"ANTHROPIC_BASE_URL=https://gw.company.example"}, "ANTHROPIC_BASE_URL"},
		{[]string{"ANTHROPIC_BASE_URL=https://api.anthropic.com/v2"}, "ANTHROPIC_BASE_URL"},
		{[]string{"ANTHROPIC_BASE_URL=http://api.anthropic.com"}, "ANTHROPIC_BASE_URL"},
		{[]string{"ANTHROPIC_BASE_URL=https://gw.company.example/claude/"}, "ANTHROPIC_BASE_URL"},
		{[]string{"CLAUDE_CODE_USE_BEDROCK=1"}, "CLAUDE_CODE_USE_BEDROCK"},
		{[]string{"CLAUDE_CODE_USE_VERTEX=1"}, "CLAUDE_CODE_USE_VERTEX"},
		{[]string{"CLAUDE_CODE_USE_FOUNDRY=1", "ANTHROPIC_BASE_URL=https://api.anthropic.com"}, "CLAUDE_CODE_USE_FOUNDRY"},
	} {
		err := claudeEndpointError(c.env)
		if c.refused == "" {
			if err != nil {
				t.Errorf("%v refused: %v", c.env, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "unset "+c.refused) {
			t.Errorf("%v: want %s refused, got %v", c.env, c.refused, err)
		}
	}
}

func TestClaudeEndpointErrorNeverShowsTheURL(t *testing.T) {
	err := claudeEndpointError([]string{"ANTHROPIC_BASE_URL=https://gw.company.example/t/sk-secret-value", "CLAUDE_CODE_USE_BEDROCK=1"})
	if err == nil || strings.Contains(err.Error(), "sk-secret-value") || strings.Contains(err.Error(), "company") || !strings.Contains(err.Error(), "unset ANTHROPIC_BASE_URL CLAUDE_CODE_USE_BEDROCK") {
		t.Fatalf("message %v", err)
	}
}

func TestSecondAppTellsItsPanelThatTerminalsUseTheFirst(t *testing.T) {
	const access = "synthetic-access-token-00000000000000"
	short, err := os.MkdirTemp("", "pp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(short) })
	t.Setenv("HOME", short)
	t.Setenv("TMPDIR", short)
	start := func(listener net.Listener, handler http.Handler) *http.Server {
		server := &http.Server{Handler: handler}
		go server.Serve(listener)
		return server
	}
	secondInstance := func(app *proxy.App) any {
		request := httptest.NewRequest(http.MethodGet, "/status", nil)
		request.Header.Set(proxy.AccessHeader, access)
		w := httptest.NewRecorder()
		app.Control(w, request)
		var status map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		return status["second_instance"]
	}
	var apps []*proxy.App
	for range 2 {
		app, err := proxy.New(proxy.Config{Integrations: []string{proxy.ClaudeCode}}, access, proxy.Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer app.Close()
		defer engineSocket(app, proxy.LocalAddresses{}, start)()
		apps = append(apps, app)
	}
	if secondInstance(apps[0]) != false || secondInstance(apps[1]) != true {
		t.Fatalf("first app second_instance %v, second %v", secondInstance(apps[0]), secondInstance(apps[1]))
	}
}

func TestPanelCloseTurnsMouseReportingOffOnlyOnATerminal(t *testing.T) {
	onScreen := closedInPanelNotice(true)
	for _, mode := range []string{"\033[?1000l", "\033[?1002l", "\033[?1003l", "\033[?1006l", "\033[?2004l", "\033[?25h"} {
		if !strings.Contains(onScreen, mode) {
			t.Fatalf("mode %q left on after the panel closed the terminal: %q", mode, onScreen)
		}
	}
	if !strings.HasSuffix(onScreen, "PhantomPrompt: this terminal was closed in the panel.\n") {
		t.Fatalf("notice %q", onScreen)
	}
	if logged := closedInPanelNotice(false); logged != "PhantomPrompt: this terminal was closed in the panel.\n" {
		t.Fatalf("escape codes would end up in a redirected log: %q", logged)
	}
}
