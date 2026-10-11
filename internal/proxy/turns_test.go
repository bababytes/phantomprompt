package proxy

import (
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
		app.logTurn(Event{Phase: "request", Adapter: ClaudeCode, Route: "/v1/messages", Status: status, Reason: "masked_and_restored", Matches: c, ElapsedMS: float64(took.Milliseconds())}, "s1", prompt, newTurn)
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
