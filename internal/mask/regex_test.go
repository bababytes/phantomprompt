package mask

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
)

func TestLiteralPattern(t *testing.T) {
	pattern, err := LiteralPattern("PROJECT.ORION+[test]", false)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(pattern)
	if !re.MatchString("project.orion+[test]") || re.MatchString("PROJECTxORIONttest") {
		t.Fatal("literal metacharacters or case were handled incorrectly")
	}
	pattern, _ = LiteralPattern("Żółw", false)
	if !regexp.MustCompile(pattern).MatchString("ŻÓŁW") {
		t.Fatal("Unicode case folding failed")
	}
	pattern, _ = LiteralPattern("Orion", true)
	if regexp.MustCompile(pattern).MatchString("orion") {
		t.Fatal("case-sensitive rule ignored")
	}
	for _, literal := range []string{"", "   ", strings.Repeat("x", 129), Prefix, string([]byte{255})} {
		if _, err := LiteralPattern(literal, false); err == nil {
			t.Fatal("invalid literal accepted")
		}
	}
}

func TestRegexMaskRestoreAndOverlap(t *testing.T) {
	policy := DefaultPolicy()
	policy.CustomRules = []RegexRule{{Pattern: `(?i:PROJECT\.ORION)`}, {Pattern: "PROJECT"}}
	s := NewSession(4096, 512<<10, policy)
	defer s.Clear()
	text := "PROJECT.ORION project.orion firstName: PROJECT.ORION"
	masked, counts, err := s.Mask([]string{text})
	if err != nil || counts.Custom != 2 || counts.Fields != 1 || counts.Total() != 3 || strings.Contains(masked[0], "ORION") {
		t.Fatal("custom and field rules did not mask disjoint values")
	}
	if s.Restore(masked[0]) != text {
		t.Fatal("custom rule restoration failed")
	}
	for split := 0; split <= len(masked[0]); split++ {
		stream := NewStream(s)
		if stream.Push(masked[0][:split], false)+stream.Push(masked[0][split:], true) != text {
			t.Fatal("split custom token restoration failed")
		}
	}
	other := NewSession(4096, 512<<10, policy)
	defer other.Clear()
	if other.Restore(masked[0]) != masked[0] {
		t.Fatal("custom rule leaked across sessions")
	}
	if resent, _, err := other.Mask(masked); err != nil || resent[0] != masked[0] || other.Restore(resent[0]) != masked[0] {
		t.Fatalf("unknown session token must stay opaque: %q %v", resent, err)
	}
}

func TestCustomRulesPreserveHistoryTokens(t *testing.T) {
	s := NewSession(4096, 512<<10, Policy{CustomRules: []RegexRule{{Pattern: `\[|SECRET|PHANTOM|CUSTOM|[0-9]`}}})
	defer s.Clear()
	masked, _, err := s.Mask([]string{"SECRET 1"})
	if err != nil {
		t.Fatal(err)
	}
	again, counts, err := s.Mask(masked)
	if err != nil || again[0] != masked[0] || counts.Total() != 0 {
		t.Fatal("custom regex corrupted an existing placeholder")
	}
}

func TestRegexLimitsAreAtomic(t *testing.T) {
	for _, pattern := range []string{"", "(", "a*", strings.Repeat("x", 513), `(?=secret)`, `\b`, `secret|\b`, `(?:x?){3}`} {
		if _, err := CompileRules([]RegexRule{{Pattern: pattern}}); err == nil {
			t.Fatal("invalid regex accepted")
		}
	}
	if _, err := CompileRules(make([]RegexRule, 17)); err == nil {
		t.Fatal("rule limit ignored")
	}
	s := NewSession(1, 512<<10, Policy{CustomRules: []RegexRule{{Pattern: `secret[0-9]`}}})
	if _, _, err := s.Mask([]string{"secret1 secret2"}); err != ErrLimit || len(s.reverse) != 0 {
		t.Fatal("capacity failure partially committed the map")
	}
	s = NewSession(4096, 512<<10, Policy{CustomRules: []RegexRule{{Pattern: `\b`}}})
	if _, _, err := s.Mask([]string{"secret"}); err != ErrRegex || len(s.reverse) != 0 {
		t.Fatal("zero-width match not rejected atomically")
	}
}

func TestConcurrentRegexSessions(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Go(func() {
			s := NewSession(4096, 512<<10, Policy{CustomRules: []RegexRule{{Pattern: "SECRET"}}})
			defer s.Clear()
			for j := 0; j < 20; j++ {
				masked, counts, err := s.Mask([]string{"SECRET SECRET"})
				if err != nil || counts.Custom != 2 || s.Restore(masked[0]) != "SECRET SECRET" {
					t.Error("parallel masking failed")
					return
				}
			}
		})
	}
	wg.Wait()
}

func BenchmarkRegexContext(b *testing.B) {
	for _, layout := range []string{"Plain", "CodeCaseFold"} {
		b.Run(layout, func(b *testing.B) {
			for _, size := range []int{100 << 10, 1 << 20} {
				b.Run(fmt.Sprintf("%dKiB", size>>10), func(b *testing.B) {
					policy := DefaultPolicy()
					pattern, _ := LiteralPattern("PROJECT.ORION", layout == "Plain")
					policy.CustomRules = []RegexRule{{Pattern: pattern}}
					line := "source text PROJECT.ORION test.user@example.test\n"
					// Sparse PII keeps large contexts inside the documented 8192-candidate limit.
					text := strings.Repeat("source code without identifiers\n", size/32) + strings.Repeat(line, 50)
					if layout == "CodeCaseFold" {
						padding := "const field = compute(value); // source without identifiers\n"
						text = strings.Repeat(padding, size/len(padding)) + strings.Repeat(line, 50)
					}
					s := NewSession(4096, 512<<10, policy)
					b.SetBytes(int64(len(text)))
					b.ReportAllocs()
					for b.Loop() {
						if _, _, err := s.Mask([]string{text}); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}

func TestCustomRulesAreCountedPerRule(t *testing.T) {
	policy := DefaultPolicy()
	policy.CustomRules = []RegexRule{{Pattern: `(?i:abc-defg-jjj)`}, {Pattern: `PRJ-\d{4}`}}
	s := NewSession(4096, 1<<20, policy)
	_, counts, err := s.Mask([]string{"abc-defg-jjj and ABC-DEFG-JJJ, PRJ-1234 and PRJ-5678"})
	if err != nil {
		t.Fatal(err)
	}
	if counts.RuleHits[`(?i:abc-defg-jjj)`] != 2 || counts.RuleNew[`(?i:abc-defg-jjj)`] != 2 || counts.RuleHits[`PRJ-\d{4}`] != 2 || counts.RuleNew[`PRJ-\d{4}`] != 2 {
		t.Fatalf("hits %v new %v", counts.RuleHits, counts.RuleNew)
	}
	_, again, err := s.Mask([]string{"abc-defg-jjj again"})
	if err != nil || again.RuleHits[`(?i:abc-defg-jjj)`] != 1 || again.RuleNew[`(?i:abc-defg-jjj)`] != 0 {
		t.Fatalf("repeat: hits %v new %v %v", again.RuleHits, again.RuleNew, err)
	}
	encoded, _ := json.Marshal(counts)
	if strings.Contains(string(encoded), "abc-defg") || strings.Contains(string(encoded), "PRJ") {
		t.Fatalf("patterns leaked into serialized counts: %s", encoded)
	}
}

func TestCustomRulesSwitchedOff(t *testing.T) {
	policy := Policy{CustomRules: []RegexRule{{Pattern: `(?i:PROJECT\.ORION)`}}, CustomOff: true}
	if ValidatePolicy(policy) != ErrSession {
		t.Fatal("switched-off custom rules counted as an active detector")
	}
	policy.VIN = true
	s := NewSession(4096, 512<<10, policy)
	defer s.Clear()
	masked, counts, err := s.Mask([]string{"PROJECT.ORION"})
	if err != nil || counts.Custom != 0 || masked[0] != "PROJECT.ORION" {
		t.Fatal("switched-off custom rule still masked")
	}
	policy.CustomOff = false
	if err := s.SetRules(policy); err != nil {
		t.Fatal(err)
	}
	if _, counts, err = s.Mask([]string{"PROJECT.ORION"}); err != nil || counts.Custom != 1 {
		t.Fatal("custom rule not applied after switching it back on")
	}
	if ValidatePolicy(Policy{VIN: true, CustomRules: []RegexRule{{Pattern: "("}}, CustomOff: true}) == nil {
		t.Fatal("invalid pattern accepted while custom rules are off")
	}
	var saved Policy
	if json.Unmarshal([]byte(`{"vin":true,"custom_rules":[{"pattern":"ORION"}]}`), &saved) != nil || saved.CustomOff {
		t.Fatal("rules saved before the switch existed must stay on")
	}
}
