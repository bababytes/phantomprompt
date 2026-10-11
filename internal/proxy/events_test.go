package proxy

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"phantomprompt/internal/mask"
)

func TestEventsBoundedAndConcurrent(t *testing.T) {
	var events Events
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			for j := 0; j < 50; j++ {
				events.Add(Event{Phase: "request", Status: 200, Reason: "accepted"})
				events.Snapshot()
			}
		})
	}
	wg.Wait()
	items := events.Snapshot()
	if len(items) != maxEvents || items[0].ID != 201 || items[len(items)-1].ID != 400 {
		t.Fatal("event history not bounded or ordered")
	}
	items[0].Reason = "changed"
	if events.Snapshot()[0].Reason != "accepted" {
		t.Fatal("snapshot exposed mutable history")
	}
	events.Clear()
	if len(events.Snapshot()) != 0 {
		t.Fatal("history was not cleared")
	}
}

func TestBlockedEventDoesNotContainInput(t *testing.T) {
	app, err := New(Config{Rules: []Rule{{Origin: "https://example.test", Path: "/echo", Adapter: "demo"}}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	r, _ := http.NewRequest(http.MethodPost, "https://example.test/secret@example.test", strings.NewReader("SECRET-PAYLOAD"))
	r.Header.Set("Authorization", "SECRET-KEY")
	r.Header.Set(SessionHeader, "SECRET-SESSION")
	response := app.Protected(r)
	defer response.Body.Close()
	data, _ := json.Marshal(app.Events.Snapshot())
	if response.StatusCode != 403 || strings.Contains(string(data), "SECRET") || strings.Contains(string(data), "example.test") {
		t.Fatal("event retained untrusted metadata or payload")
	}
	if !strings.Contains(string(data), "route_or_method_not_supported") {
		t.Fatal("safe diagnostic category missing")
	}
}

func TestRepeatedBlocksAreMerged(t *testing.T) {
	var events Events
	block := Event{Phase: "request", Adapter: CopilotCLI, Route: "/responses", Status: 422, Reason: "model_format_not_supported_yet"}
	first := events.Add(block)
	events.Add(Event{Phase: "request", Adapter: CopilotCLI, Route: "/v1/messages", Status: 200, Reason: "masked_and_restored"})
	events.Add(Event{Phase: "request", Adapter: CopilotCLI, Route: "/v1/messages", Status: 200, Reason: "masked_and_restored"})
	if again := events.Add(block); again != first {
		t.Fatal("identical block got a new row")
	}
	other := block
	other.Route = "/chat/completions"
	events.Add(other)
	snapshot := events.Snapshot()
	if len(snapshot) != 4 || snapshot[2].ID != first || snapshot[2].Repeats != 1 || snapshot[3].Route != "/chat/completions" || snapshot[3].Repeats != 0 {
		t.Fatalf("events %+v", snapshot)
	}
}

func TestFormatAlertsGroupUnknownFormats(t *testing.T) {
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	app.discovered.add(Shape{Direction: "request", Method: "POST", Path: "/v1/messages", Note: "content block server_tool_use"})
	for range 3 {
		app.Events.Add(Event{Phase: "request", Adapter: ClaudeCode, Route: "/v1/messages", Status: 422, Reason: "unsupported_content"})
	}
	app.Events.Add(Event{Phase: "request", Adapter: ClaudeCode, Route: "/v1/messages", Status: 422, Reason: "attachments_not_supported"})
	app.Events.Add(Event{Phase: "request", Adapter: "demo", Route: "/echo", Status: 422, Reason: "unsupported_content_or_masking_limit"})
	alerts := app.FormatAlerts()
	byAdapter := make(map[string]FormatAlert)
	for _, alert := range alerts {
		byAdapter[alert.Adapter] = alert
	}
	claude := byAdapter[ClaudeCode]
	if len(alerts) != 1 || claude.Count != 3 || claude.Detail != "content block server_tool_use" || claude.Reason != "unsupported_content" {
		t.Fatalf("alerts %+v", alerts)
	}
	app.Events.Clear()
	if len(app.FormatAlerts()) != 0 {
		t.Fatal("cleared history still raises alerts")
	}
}

func TestSnapshotsAreIndependentOfTheHistory(t *testing.T) {
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	app.cliSessions.add("s1", "secret", "claude", 1)
	event := func() Event {
		return Event{Phase: "request", Adapter: ClaudeCode, Route: "/v1/messages", Status: 200, Reason: "masked_and_restored",
			Matches: mask.Counts{New: 1, NewKinds: map[string]int{"custom": 1}, FieldNames: map[string]int{"codename": 1}},
			Rules:   map[string]RuleUse{"rule-a": {Values: 1, Hits: 1}}}
	}
	app.logTurn(event(), "s1", "p1", true)
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 300 {
			app.logTurn(event(), "s1", "p1", false)
		}
	})
	wg.Go(func() {
		for range 300 {
			if _, err := json.Marshal(app.Events.Snapshot()); err != nil {
				t.Error(err)
				return
			}
		}
	})
	wg.Wait()
	rows := app.Events.Snapshot()
	rows[0].Rules["rule-a"] = RuleUse{}
	rows[0].Matches.FieldNames["codename"] = 0
	row := app.Events.Snapshot()[0]
	if len(rows) != 1 || row.Rules["rule-a"].Hits != 301 || row.Matches.FieldNames["codename"] != 301 {
		t.Fatalf("history changed through a snapshot: rows=%d hits=%d fields=%d", len(rows), row.Rules["rule-a"].Hits, row.Matches.FieldNames["codename"])
	}
}
