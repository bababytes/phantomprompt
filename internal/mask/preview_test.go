package mask

import "testing"

func TestPreviewHidesMostOfTheValue(t *testing.T) {
	cases := []struct {
		value  string
		secret bool
		want   string
	}{
		{"abc-x7k2-91", false, "abc-••••-•1"},
		{"PL12345678", false, "PL1••••••8"},
		{"abc-x7k2-91", true, redacted},
		{"x", true, redacted},
		{"a", false, redacted},
		{"ab", false, redacted},
		{"abc", false, "a•c"},
		{"a-b", false, redacted},
		{"!!", false, redacted},
		{"#$%&*", false, redacted},
		{"日本", false, redacted},
		{"日本語", false, "日•語"},
		{"Żółw-12", false, "Żó••-•2"},
	}
	for _, tc := range cases {
		got := Preview(tc.value, tc.secret)
		if got != tc.want {
			t.Fatalf("Preview(%q, %v) = %q, want %q", tc.value, tc.secret, got, tc.want)
		}
		if got == tc.value {
			t.Fatalf("Preview(%q) shows the whole value", tc.value)
		}
	}
	long := Preview("ACME-0123456789012345678901234567890123456789", false)
	if len([]rune(long)) != maxPreview || []rune(long)[maxPreview-1] != '…' {
		t.Fatalf("long preview %q", long)
	}
}

func TestCustomRulesReportPreviewsOfNewValuesOnly(t *testing.T) {
	policy := DefaultPolicy()
	shape := `(?i:\babc-[a-z0-9]{4}-[a-z0-9]{2}\b)`
	secret := `\bACME-[0-9]{6}\b`
	policy.CustomRules = []RegexRule{{Pattern: shape}, {Pattern: secret, Secret: true}}
	s := NewSession(4096, 1<<20, policy)
	_, counts, err := s.Mask([]string{"abc-x7k2-91 and abc-q1w2-zz, again abc-x7k2-91, key ACME-123456"})
	if err != nil {
		t.Fatal(err)
	}
	if got := counts.RulePreviews[shape]; len(got) != 2 || got[0] != "abc-••••-•1" || got[1] != "abc-••••-•z" {
		t.Fatalf("shape previews %v", got)
	}
	if got := counts.RulePreviews[secret]; len(got) != 1 || got[0] != redacted {
		t.Fatalf("secret previews %v", got)
	}
	_, again, err := s.Mask([]string{"abc-x7k2-91"})
	if err != nil || len(again.RulePreviews) != 0 {
		t.Fatalf("known values must not be reported again: %v %v", again.RulePreviews, err)
	}
}
