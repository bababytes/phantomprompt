package proxy

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"phantomprompt/internal/mask"
)

func TestSetPolicyPersistsAndAppliesToOpenCLISessions(t *testing.T) {
	rules := filepath.Join(t.TempDir(), "PhantomPrompt", "rules.json")
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{RulesFile: rules})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	id, _, err := app.CreateCLISession("claude", 1)
	if err != nil {
		t.Fatal(err)
	}
	policy := mask.DefaultPolicy()
	policy.CustomFields = []string{"employeeId"}
	policy.DisabledFields = []string{"phone"}
	policy.LenientFields = true
	if err := app.SetPolicy(policy); err != nil {
		t.Fatal(err)
	}
	session, release, err := app.Sessions.Acquire(id, "cli")
	if err != nil {
		t.Fatal(err)
	}
	out, counts, err := session.Mask([]string{"employeeId: \"E-1\"\nphone: 000 000 000"})
	lenient := session.Policy.LenientFields
	release()
	if err != nil || counts.Fields != 1 || strings.Contains(out[0], "E-1") || !strings.Contains(out[0], "phone: 000 000 000") || !lenient {
		t.Fatalf("open CLI session: out %q counts %+v err %v lenient %v", out, counts, err, lenient)
	}
	info, err := os.Stat(rules)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("rules file %v %v", info, err)
	}
	data, _ := os.ReadFile(rules)
	if strings.Contains(string(data), "lenient") || !strings.Contains(string(data), "employeeId") {
		t.Fatalf("saved rules: %s", data)
	}
	if err := app.SetPolicy(mask.Policy{}); err == nil || app.Policy().CustomFields[0] != "employeeId" {
		t.Fatal("rejected policy changed the live rules")
	}
	if saved, _ := os.ReadFile(rules); string(saved) != string(data) {
		t.Fatal("rejected policy changed the saved rules")
	}

	restarted, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{RulesFile: rules})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if loaded := restarted.Policy(); len(loaded.CustomFields) != 1 || len(loaded.DisabledFields) != 1 || loaded.LenientFields {
		t.Fatalf("restart lost the rules: %+v", loaded)
	}
	startup, _ := restarted.StartupPolicy()
	if len(startup.CustomFields) != 0 || len(startup.DisabledFields) != 0 {
		t.Fatalf("startup policy should come from configuration: %+v", startup)
	}
}

func TestInvalidRulesFileStopsStartup(t *testing.T) {
	rules := filepath.Join(t.TempDir(), "rules.json")
	for _, content := range []string{`{"vin":true,"unknown":1}`, `{}`, `{"vin":true,"custom_rules":[{"pattern":"\\b"}]}`, `not json`} {
		os.WriteFile(rules, []byte(content), 0600)
		if _, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{RulesFile: rules}); err == nil || !strings.Contains(err.Error(), rules) {
			t.Fatalf("%s: %v", content, err)
		}
	}
	if _, found, err := LoadRules(filepath.Join(t.TempDir(), "missing.json")); found || err != nil {
		t.Fatal("missing rules file should mean defaults")
	}
}

func TestSavedRulesWithRemovedSecretPatternsStillLoad(t *testing.T) {
	rules := filepath.Join(t.TempDir(), "rules.json")
	os.WriteFile(rules, []byte(`{"secrets":true,"disabled_secrets":["password-label","jwt"]}`), 0600)
	policy, found, err := LoadRules(rules)
	if err != nil || !found || !slices.Equal(policy.DisabledSecrets, []string{"jwt"}) {
		t.Fatalf("got %+v %v %v", policy.DisabledSecrets, found, err)
	}
}
