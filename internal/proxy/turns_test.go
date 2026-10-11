package proxy

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"phantomprompt/internal/mask"
)

func TestOneRowPerQuestion(t *testing.T) {
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	clock := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	app.turns.now = func() time.Time { return clock }
	app.cliSessions.add("s1", "secret", "claude", 1)
	app.cliSessions.setFolder("s1", "/work/project")
	call := func(after, took time.Duration, prompt string, newTurn bool, newValues int, status int) {
		clock = clock.Add(after + took)
		c := mask.Counts{Fields: newValues, New: newValues, TokenOverhead: 1}
		if newValues > 0 {
			c.NewKinds, c.NewFields = map[string]int{"fields": newValues}, map[string]int{"codename": newValues}
		}
		app.logTurn(Event{Phase: "request", Adapter: ClaudeCode, Route: "/v1/messages", Status: status, Reason: "masked_and_restored", Matches: c, ElapsedMS: float64(took.Milliseconds())}, "s1", turnSignal{prompt: prompt, newTurn: newTurn})
	}
	call(0, 2*time.Second, "p1", true, 0, 200)
	call(0, 3*time.Second, "p1", true, 2, 200)
	call(40*time.Second, 5*time.Second, "p1", false, 1, 200)
	call(0, 2*time.Second, "", true, 1, 200)
	call(0, time.Second, "", true, 0, 422)
	call(60*time.Second, 4*time.Second, "p2", true, 0, 200)
	call(time.Second, 4*time.Second, "p2", false, 3, 200)
	call(30*time.Second, time.Second, "", false, 1, 200)
	rows := app.Events.Snapshot()
	if len(rows) != 3 || rows[0].Folder != "/work/project" || rows[1].Folder != "/work/project" || rows[2].Folder != "/work/project" || rows[0].Requests != 4 || rows[0].Matches.NewFields["codename"] != 4 || rows[0].Matches.TokenOverhead != 4 || rows[1].Status != 422 || rows[2].Requests != 3 || rows[2].Matches.NewFields["codename"] != 4 {
		t.Fatalf("rows %+v", rows)
	}
	app.forgetTurns("s1")
	call(0, time.Second, "p2", false, 1, 200)
	if rows := app.Events.Snapshot(); len(rows) != 4 {
		t.Fatalf("closed session still merges: %d rows", len(rows))
	}
}

func TestEveryToolTellsWhichPromptARequestServes(t *testing.T) {
	header := func(pairs ...string) http.Header {
		h := http.Header{}
		for i := 0; i < len(pairs); i += 2 {
			h.Set(pairs[i], pairs[i+1])
		}
		return h
	}
	for _, tc := range []struct {
		name    string
		adapter string
		header  http.Header
		want    turnSignal
	}{
		{"claude main", ClaudeCode, header("X-Claude-Code-Prompt-Id", "p1", "X-Claude-Code-Request-Class", "main"), turnSignal{prompt: "p1", newTurn: true}},
		{"claude helper", ClaudeCode, header("X-Claude-Code-Prompt-Id", "p1", "X-Claude-Code-Request-Class", "auxiliary"), turnSignal{prompt: "p1", background: true, newTurn: true}},
		{"claude subagent", ClaudeCode, header("X-Claude-Code-Prompt-Id", "p1", "X-Claude-Code-Request-Class", "subagent"), turnSignal{prompt: "p1", background: true, newTurn: true}},
		{"claude without hints", ClaudeCode, header(), turnSignal{newTurn: true}},
		{"copilot agent", CopilotCLI, header("X-Interaction-Id", "i1", "X-Interaction-Type", "conversation-agent"), turnSignal{prompt: "i1", newTurn: true}},
		{"copilot first request", CopilotCLI, header("X-Interaction-Id", "i1", "X-Interaction-Type", "conversation-user"), turnSignal{prompt: "i1", newTurn: true}},
		{"copilot session naming", CopilotCLI, header("X-Interaction-Id", "i1", "X-Interaction-Type", "conversation-background"), turnSignal{prompt: "i1", background: true, newTurn: true}},
		{"codex turn", CodexCLI, header("X-Codex-Turn-Metadata", `{"turn_id":"t2","root_turn_id":"t1"}`), turnSignal{prompt: "t1", newTurn: true}},
		{"codex own turn", CodexCLI, header("X-Codex-Turn-Metadata", `{"turn_id":"t2"}`), turnSignal{prompt: "t2", newTurn: true}},
		{"codex review", CodexCLI, header("X-Codex-Turn-Metadata", `{"turn_id":"t3"}`, "X-Openai-Subagent", "review"), turnSignal{prompt: "t3", background: true, newTurn: true}},
		{"codex unreadable metadata", CodexCLI, header("X-Codex-Turn-Metadata", "not json"), turnSignal{newTurn: true}},
		{"id too long", ClaudeCode, header("X-Claude-Code-Prompt-Id", strings.Repeat("a", 129)), turnSignal{newTurn: true}},
	} {
		if got := clientTurn(tc.adapter, tc.header, true); got != tc.want {
			t.Errorf("%s: %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestOnePromptIsOneRowAndOneCount(t *testing.T) {
	for _, adapter := range []string{ClaudeCode, CopilotCLI, CodexCLI} {
		app, err := New(Config{Integrations: []string{adapter}}, localSecret, Options{})
		if err != nil {
			t.Fatal(err)
		}
		clock := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
		app.turns.now = func() time.Time { return clock }
		app.cliSessions.add("s1", "secret", "zsh", 1)
		call := func(after time.Duration, turn turnSignal, status int, typed int) {
			clock = clock.Add(after + time.Second)
			reason := "masked_and_restored"
			if status >= 400 {
				reason = "passed_without_restore"
			}
			c := mask.Counts{Email: typed, New: typed, TokenOverhead: typed}
			if typed > 0 {
				c.NewKinds, c.TypedKinds = map[string]int{"email": typed}, map[string]int{"email": typed}
			}
			app.logTurn(Event{Phase: "request", Adapter: adapter, Route: "/v1/messages", Status: status, Reason: reason, Matches: c, ElapsedMS: 1000}, "s1", turn)
		}
		call(0, turnSignal{background: true, newTurn: true}, 429, 0)
		call(5*time.Second, turnSignal{prompt: "p1", newTurn: true}, 429, 1)
		call(40*time.Second, turnSignal{prompt: "p1", newTurn: true}, 200, 0)
		call(0, turnSignal{prompt: "p1", background: true, newTurn: true}, 200, 0)
		call(40*time.Second, turnSignal{prompt: "p1"}, 200, 0)
		call(time.Second, turnSignal{prompt: "p2", newTurn: true}, 200, 1)
		call(time.Second, turnSignal{prompt: "p3", background: true, newTurn: true}, 200, 0)

		rows := app.Events.Snapshot()
		if len(rows) != 2 {
			t.Fatalf("%s: one row per prompt, got %+v", adapter, rows)
		}
		first, second := rows[0], rows[1]
		if first.Requests != 4 || first.AIErrors != 1 || first.Status != 200 || first.Matches.TypedKinds["email"] != 1 || second.Requests != 1 || second.AIErrors != 0 {
			t.Fatalf("%s: rows %+v", adapter, rows)
		}
		terminal, integrations := app.CLISessions()[0], app.Integrations()
		if app.PromptsSent.Load() != 2 || terminal.Prompts != 2 || len(integrations) != 1 || integrations[0].Prompts != 2 {
			t.Fatalf("%s: prompts %d, terminal %+v, connections %+v", adapter, app.PromptsSent.Load(), terminal, integrations)
		}
		app.Close()
	}
}

func TestPromptOfAToolWithoutTurnIdsIsCountedOnce(t *testing.T) {
	app, err := New(Config{Integrations: []string{CodexCLI}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	clock := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	app.turns.now = func() time.Time { return clock }
	app.cliSessions.add("s1", "secret", "codex", 1)
	call := func(after time.Duration, newTurn bool) {
		clock = clock.Add(after + time.Second)
		app.logTurn(Event{Phase: "request", Adapter: CodexCLI, Status: 200, Reason: "masked_and_restored", ElapsedMS: 1000}, "s1", turnSignal{newTurn: newTurn})
	}
	failed := mask.Counts{Email: 1, New: 1, NewKinds: map[string]int{"email": 1}}
	app.logTurn(Event{Phase: "request", Adapter: CodexCLI, Status: 429, Reason: "passed_without_restore", Matches: failed, ElapsedMS: 1000}, "s1", turnSignal{newTurn: true})
	if app.PromptsSent.Load() != 0 || len(app.Events.Snapshot()) != 0 {
		t.Fatalf("an AI service error without a turn id was counted: %d prompts, %+v", app.PromptsSent.Load(), app.Events.Snapshot())
	}
	call(0, true)
	call(2*time.Second, false)
	call(30*time.Second, false)
	call(30*time.Second, true)
	if app.PromptsSent.Load() != 2 || app.CLISessions()[0].Prompts != 2 {
		t.Fatalf("prompts %d, terminal %+v", app.PromptsSent.Load(), app.CLISessions()[0])
	}
}
