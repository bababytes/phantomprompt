package proxy

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"phantomprompt/internal/mask"
)

func TestCustomRuleStatsFollowActiveRules(t *testing.T) {
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	policy := mask.DefaultPolicy()
	policy.CustomRules = []mask.RegexRule{{Pattern: `(?i:abc-defg-jjj)`}, {Pattern: `PRJ-\d{4}`}}
	if err := app.SetPolicy(policy); err != nil {
		t.Fatal(err)
	}
	session, release, err := app.cliSession("")
	if err != nil {
		t.Fatal(err)
	}
	_, counts, err := session.Mask([]string{"send abc-defg-jjj and abc-defg-jjj"})
	release()
	if err != nil {
		t.Fatal(err)
	}
	app.recordMasked(counts)
	stats := app.CustomRuleStats()
	if len(stats) != 2 || stats[0].Pattern != `(?i:abc-defg-jjj)` || stats[0].Hits != 2 || stats[0].Unique != 1 || stats[0].LastHit == nil || stats[1].Hits != 0 || stats[1].LastHit != nil {
		t.Fatalf("stats %+v", stats)
	}
	policy.CustomRules = policy.CustomRules[1:]
	if err := app.SetPolicy(policy); err != nil {
		t.Fatal(err)
	}
	if stats := app.CustomRuleStats(); len(stats) != 1 || stats[0].Pattern != `PRJ-\d{4}` {
		t.Fatalf("removed rule still listed: %+v", stats)
	}
}

func TestHistoryNamesRulesWithoutPatterns(t *testing.T) {
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	policy := mask.DefaultPolicy()
	policy.CustomRules = []mask.RegexRule{{Pattern: `(?i:abc-defg-jjj)`, Name: "Order number"}, {Pattern: `PRJ-\d{4}`, Secret: true}}
	if err := app.SetPolicy(policy); err != nil {
		t.Fatal(err)
	}
	session, release, err := app.cliSession("")
	if err != nil {
		t.Fatal(err)
	}
	_, counts, err := session.Mask([]string{"send abc-defg-jjj, abc-defg-jjj and PRJ-1234"})
	release()
	if err != nil {
		t.Fatal(err)
	}
	var event Event
	app.setMatches(&event, counts)
	want := map[string]RuleUse{"Order number": {Values: 1, Hits: 2}, "unnamed secret rule": {Secret: true, Values: 1, Hits: 1}}
	if !maps.Equal(event.Rules, want) || event.Matches.RuleHits != nil || event.Matches.Custom != 2 || event.Matches.Secret != 1 {
		t.Fatalf("rules %+v matches %+v", event.Rules, event.Matches)
	}
	data, _ := json.Marshal(event)
	if strings.Contains(string(data), "abc-defg") || strings.Contains(string(data), `PRJ-\\d`) || !strings.Contains(string(data), `"Order number"`) {
		t.Fatalf("event JSON %s", data)
	}
	row := Event{}
	mergeTurn(&row, event)
	mergeTurn(&row, event)
	if row.Rules["Order number"] != (RuleUse{Values: 2, Hits: 4}) {
		t.Fatalf("turn merge %+v", row.Rules)
	}
}

func TestRuleExamplesAreCappedAndClearedWithHistory(t *testing.T) {
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	pattern := `(?i:\bord-[0-9]{3}[a-z]\b)`
	policy := mask.DefaultPolicy()
	policy.CustomRules = []mask.RegexRule{{Pattern: pattern, Name: "Order"}}
	if err := app.SetPolicy(policy); err != nil {
		t.Fatal(err)
	}
	session, release, err := app.cliSession("")
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for n := range 20 {
		value := fmt.Sprintf("ord-%03d%c", 100+n, 'a'+n)
		fmt.Fprintf(&text, "%s %s ", value, value)
	}
	_, counts, err := session.Mask([]string{text.String()})
	release()
	if err != nil {
		t.Fatal(err)
	}
	app.recordMasked(counts)
	stats := app.CustomRuleStats()
	if len(stats) != 1 || stats[0].Hits != 40 || stats[0].Unique != 20 || len(stats[0].Examples) != maxRuleExamples || stats[0].Examples[maxRuleExamples-1] != "or•-•••t" {
		t.Fatalf("stats %+v", stats)
	}
	for _, example := range stats[0].Examples {
		if strings.Count(example, "•") != 4 {
			t.Fatalf("example not hidden: %q", example)
		}
	}
	stats[0].Examples[0] = "changed"
	if app.CustomRuleStats()[0].Examples[0] == "changed" {
		t.Fatal("status shares the examples slice with the recorder")
	}
	request := httptest.NewRequest(http.MethodDelete, "/events", nil)
	request.Header.Set(AccessHeader, localSecret)
	app.Control(httptest.NewRecorder(), request)
	if after := app.CustomRuleStats()[0]; after.Examples != nil || after.Hits != 40 {
		t.Fatalf("clear history must drop examples only: %+v", after)
	}
}

func TestBuiltInSecretPatternsAppearByRule(t *testing.T) {
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	policy := mask.DefaultPolicy()
	policy.DisabledSecrets = []string{"jwt"}
	if err := app.SetPolicy(policy); err != nil {
		t.Fatal(err)
	}
	session, release, err := app.cliSession("")
	if err != nil {
		t.Fatal(err)
	}
	_, counts, err := session.Mask([]string{"key AKIAIOSFODNN7EXAMPLE and AKIAIOSFODNN7EXAMPLE"})
	release()
	if err != nil {
		t.Fatal(err)
	}
	app.recordMasked(counts)
	var event Event
	app.setMatches(&event, counts)
	if event.Rules["AWS access key ID"] != (RuleUse{Secret: true, Values: 1, Hits: 2}) {
		t.Fatalf("history rules %+v", event.Rules)
	}
	stats := app.SecretRuleStats()
	index := slices.IndexFunc(stats, func(stat RuleStat) bool { return stat.ID == "aws-access-key" })
	if index < 0 || stats[index].Hits != 2 || stats[index].Unique != 1 || stats[index].Name != "AWS access key ID" || len(stats[index].Examples) != 1 || strings.Trim(stats[index].Examples[0], "•") != "" {
		t.Fatalf("stats %+v", stats)
	}
	if slices.ContainsFunc(stats, func(stat RuleStat) bool { return stat.ID == "jwt" }) {
		t.Fatal("disabled pattern listed")
	}
}
