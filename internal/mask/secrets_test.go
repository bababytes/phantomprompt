package mask

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func secretSession() *Session {
	p := DefaultPolicy()
	p.LenientFields = true
	return NewSession(4096, 1<<20, p)
}

func TestSecretDetection(t *testing.T) {
	cases := map[string]struct {
		text   string
		masked string // substring that must disappear
		keep   string // substring that must remain (label/context)
	}{
		"aws key":      {"deploy with AKIAIOSFODNN7EXAMPLE now", "AKIAIOSFODNN7EXAMPLE", "deploy with"},
		"github token": {"token ghp_" + strings.Repeat("a", 36) + " end", "ghp_" + strings.Repeat("a", 36), "token"},
		"github pat":   {"github_pat_" + strings.Repeat("A", 22), "github_pat_" + strings.Repeat("A", 22), ""},
		"slack":        {"use xoxb-123456789012-abcdefABCDEF here", "xoxb-123456789012-abcdefABCDEF", "use"},
		"google":       {"key AIza" + strings.Repeat("a", 35), "AIza" + strings.Repeat("a", 35), "key"},
		"stripe":       {"sk_live_" + strings.Repeat("a", 24), "sk_live_" + strings.Repeat("a", 24), ""},
		"openai":       {"OPENAI=sk-" + strings.Repeat("a", 32), "sk-" + strings.Repeat("a", 32), "OPENAI="},
		"anthropic":    {"sk-ant-" + strings.Repeat("a", 30), "sk-ant-" + strings.Repeat("a", 30), ""},
		"jwt":          {"Bearer eyJhbGciOiJIUzI1.eyJzdWIiOiIxMjM0NQ.SflKxwRJSMeKKF2Q done", "eyJhbGciOiJIUzI1.eyJzdWIiOiIxMjM0NQ.SflKxwRJSMeKKF2Q", "done"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := secretSession()
			out, counts, err := s.Mask([]string{tc.text})
			if err != nil {
				t.Fatal(err)
			}
			if counts.Secret == 0 {
				t.Fatalf("no secret detected in %q", tc.text)
			}
			if strings.Contains(out[0], tc.masked) {
				t.Fatalf("secret value survived: %q", out[0])
			}
			if tc.keep != "" && !strings.Contains(out[0], tc.keep) {
				t.Fatalf("context/label was masked away: %q", out[0])
			}
			if !strings.Contains(out[0], Prefix+"SECRET_") {
				t.Fatalf("no SECRET token produced: %q", out[0])
			}
			if s.Restore(out[0]) != tc.text {
				t.Fatalf("restore failed: %q", s.Restore(out[0]))
			}
		})
	}
}

func TestSecretMultilinePEM(t *testing.T) {
	s := secretSession()
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\nabc/def+ghi=\n-----END RSA PRIVATE KEY-----"
	text := "here is the key:\n" + pem + "\nkeep this line"
	out, counts, err := s.Mask([]string{text})
	if err != nil || counts.Secret != 1 {
		t.Fatalf("PEM not masked: counts=%+v err=%v", counts, err)
	}
	if strings.Contains(out[0], "MIIEowIBAAKCAQEA") || strings.Contains(out[0], "PRIVATE KEY") {
		t.Fatalf("private key survived: %q", out[0])
	}
	if !strings.Contains(out[0], "keep this line") || s.Restore(out[0]) != text {
		t.Fatalf("surrounding text or restore broken: %q", out[0])
	}
}

func TestSecretNoFalsePositivesInCode(t *testing.T) {
	s := secretSession()
	inputs := []string{
		"func keyFor(id string) string { return prefix + id }",
		"const token = parseToken(headers)",
		"the API key is stored safely",             // no value after label
		"map[string]string{\"name\": \"example\"}", // not a secret field
		"git commit -m \"add auth handler\"",
		"https://example.test/path?ref=main",
		"interface Session { token: string; password?: string }",
		"const token = getToken(1)",
		`{"max_tokens": 4096, "usage": {"input_tokens": 1234}}`,
		"the password is stored in the vault",
		"password to be changed in the next release",
	}
	out, counts, err := s.Mask(inputs)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Secret != 0 {
		t.Fatalf("false positive secret: %d in %v -> %v", counts.Secret, inputs, out)
	}
}

func TestSecretScanningIsBounded(t *testing.T) {
	s := secretSession()
	// Adversarial: many 'sk-' prefixes and a huge run; RE2 keeps this linear and fast.
	big := strings.Repeat("sk-", 100000) + strings.Repeat("A", 500000)
	done := make(chan struct{})
	go func() {
		s.Mask([]string{big})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("secret scanning did not finish within 5s (possible catastrophic backtracking)")
	}
}

func TestSecretDetectorsDescribeEveryRule(t *testing.T) {
	detectors := SecretDetectors()
	if len(detectors) != len(secretRules) {
		t.Fatalf("got %d detectors for %d rules", len(detectors), len(secretRules))
	}
	ids := map[string]bool{}
	for _, detector := range detectors {
		if detector.ID == "" || detector.Name == "" || ids[detector.ID] {
			t.Fatalf("detector without a unique id and name: %+v", detector)
		}
		ids[detector.ID] = true
		if _, err := regexp.Compile(detector.Pattern); err != nil {
			t.Fatalf("%s: %v", detector.ID, err)
		}
	}
	if !regexp.MustCompile(detectors[0].Pattern).MatchString("AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("first detector is not the AWS key format: %+v", detectors[0])
	}
}

func TestLabeledPasswordsStaySecretWhenAFieldClaimsThem(t *testing.T) {
	out, counts, err := secretSession().Mask([]string{"password: hunter2secret"})
	if err != nil || strings.Contains(out[0], "hunter2secret") || counts.Secret != 1 {
		t.Fatalf("a labeled password must be masked as a secret: %q %+v %v", out, counts, err)
	}
	p := DefaultPolicy()
	p.CustomFields = []string{"password"}
	s := NewSession(4096, 1<<20, p)
	out, counts, err = s.Mask([]string{"password: hunter2secret"})
	if err != nil || strings.Contains(out[0], "hunter2secret") || counts.Fields != 1 {
		t.Fatalf("a password field must mask it: %q %+v %v", out, counts, err)
	}
	if shell, kept := s.RestoreExcept(`{"command":"curl -u `+tokenPattern.FindString(out[0])+`"}`, true, map[string]bool{"SECRET": true}, nil); kept != 1 || strings.Contains(shell, "hunter2secret") {
		t.Fatalf("a password claimed by a field reached a shell command: kept %d", kept)
	}
	if edit := s.RestoreJSON(out[0]); !strings.Contains(edit, "hunter2secret") {
		t.Fatal("a file edit no longer gets the password back")
	}
}

func TestSecretClassSurvivesOverlappingRulesAndRuleChanges(t *testing.T) {
	github := "ghp" + "_" + strings.Repeat("Ab1", 12)
	withheld := func(s *Session, token string) bool {
		out, kept := s.RestoreExcept("run "+token, false, map[string]bool{"SECRET": true}, nil)
		return kept == 1 && strings.Contains(out, token)
	}
	p := DefaultPolicy()
	p.CustomRules = []RegexRule{{Pattern: `ghp_[A-Za-z0-9]+`}}
	s := NewSession(4096, 1<<20, p)
	out, _, err := s.Mask([]string{"token " + github})
	if err != nil || !strings.Contains(out[0], Prefix+"CUSTOM_") || !withheld(s, tokenPattern.FindString(out[0])) {
		t.Fatalf("a plain custom rule over a GitHub token weakened it: %q %v", out, err)
	}

	p = DefaultPolicy()
	p.Secrets = false
	p.CustomFields = []string{"password"}
	s = NewSession(4096, 1<<20, p)
	value := "Zq8" + "vLm2pX"
	first, _, err := s.Mask([]string{"password: " + value})
	if err != nil {
		t.Fatal(err)
	}
	token := tokenPattern.FindString(first[0])
	if withheld(s, token) {
		t.Fatal("with secret detection off the field value is not a secret")
	}
	p.Secrets = true
	if err := s.SetRules(p); err != nil {
		t.Fatal(err)
	}
	again, _, err := s.Mask([]string{"password: " + value})
	if err != nil || again[0] != first[0] || !withheld(s, token) {
		t.Fatalf("turning secret detection on must keep the token and make it secret: %q %q %v", first, again, err)
	}
	p.Secrets = false
	if err := s.SetRules(p); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Mask([]string{"firstName: " + value}); err != nil || !withheld(s, token) {
		t.Fatalf("a weaker rule downgraded an issued secret: %v", err)
	}
}

func TestAValueLearnedAsAFieldBecomesSecret(t *testing.T) {
	p := DefaultPolicy()
	p.LenientFields = true
	s := NewSession(4096, 1<<20, p)
	value := "Pw7" + "kQ2xRt9"
	first, counts, err := s.Mask([]string{"firstName: " + value})
	if err != nil || counts.Fields != 1 {
		t.Fatalf("%q %+v %v", first, counts, err)
	}
	token := tokenPattern.FindString(first[0])
	second, _, err := s.Mask([]string{"DB_PASSWORD=" + value})
	if err != nil || tokenPattern.FindString(second[0]) != token {
		t.Fatalf("the learned value must keep its token: %q %v", second, err)
	}
	if out, kept := s.RestoreExcept("psql "+token, false, map[string]bool{"SECRET": true}, nil); kept != 1 || strings.Contains(out, value) {
		t.Fatal("a value seen later as a password was restored into a shell command")
	}
}

func TestRemovedSecretIDsAreDropped(t *testing.T) {
	if got := KnownSecretIDs([]string{"password-label", "jwt", "token-label"}); !slices.Equal(got, []string{"jwt"}) {
		t.Fatalf("got %v", got)
	}
}

func TestDisabledSecretPatternsAreSkipped(t *testing.T) {
	p := DefaultPolicy()
	p.DisabledSecrets = []string{"aws-access-key"}
	if err := ValidatePolicy(p); err != nil {
		t.Fatal(err)
	}
	s := NewSession(4096, 1<<20, p)
	github := "ghp_" + strings.Repeat("a", 36)
	out, _, err := s.Mask([]string{"AKIAIOSFODNN7EXAMPLE and " + github})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out[0], "AKIAIOSFODNN7EXAMPLE") || strings.Contains(out[0], github) {
		t.Fatalf("only the AWS pattern should be off: %q", out[0])
	}
	for _, disabled := range [][]string{{"no-such-pattern"}, slices.Repeat([]string{"jwt"}, len(secretRules)+1)} {
		p.DisabledSecrets = disabled
		if !errors.Is(ValidatePolicy(p), ErrSecrets) {
			t.Fatalf("invalid disabled patterns accepted: %v", disabled)
		}
	}
}

func TestCustomSecretRulesMaskAsSecrets(t *testing.T) {
	pattern := `(?i:\babc-[a-z0-9]{4}-[a-z0-9]{3}\b)`
	p := DefaultPolicy()
	p.Secrets = false
	p.CustomRules = []RegexRule{{Pattern: pattern, Secret: true}, {Pattern: "Orion"}}
	s := NewSession(4096, 1<<20, p)
	out, counts, err := s.Mask([]string{"ids abc-defg-jjj and Orion"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out[0], "abc-defg-jjj") || !strings.Contains(out[0], Prefix+"SECRET_") || !strings.Contains(out[0], Prefix+"CUSTOM_") {
		t.Fatalf("custom secret not masked as SECRET: %q", out[0])
	}
	if counts.Secret != 1 || counts.Custom != 1 || counts.RuleHits[pattern] != 1 || counts.RuleHits["Orion"] != 1 {
		t.Fatalf("counts %+v", counts)
	}
}

func TestDetectorPatternsMatchWhatIsMasked(t *testing.T) {
	samples := map[string]string{"vin": "1HGCM82633A004352", "pesel": "12345678901", "guid": "550e8400-e29b-41d4-a716-446655440000", "email": "jan@example.test"}
	patterns := DetectorPatterns()
	if len(patterns) != len(samples) {
		t.Fatalf("patterns %v", patterns)
	}
	for id, sample := range samples {
		if !regexp.MustCompile(patterns[id]).MatchString(sample) {
			t.Fatalf("%s pattern %q does not match %q", id, patterns[id], sample)
		}
	}
}

func TestBuiltInSecretMatchesAreCountedPerPattern(t *testing.T) {
	s := NewSession(100, 16384, Policy{Secrets: true})
	key := SecretRuleKey("aws-access-key")
	_, first, err := s.Mask([]string{"deploy with AKIAIOSFODNN7EXAMPLE now"})
	if err != nil || first.RuleHits[key] != 1 || first.RuleNew[key] != 1 || len(first.RulePreviews[key]) != 1 || strings.Contains(first.RulePreviews[key][0], "AKIA") {
		t.Fatalf("first %+v %v", first, err)
	}
	_, again, err := s.Mask([]string{"again AKIAIOSFODNN7EXAMPLE and AKIAIOSFODNN7EXAMPLE"})
	if err != nil || again.RuleHits[key] != 2 || again.RuleNew[key] != 0 {
		t.Fatalf("repeated key %+v %v", again, err)
	}
}

func TestURLCredentialsMaskOnlyThePassword(t *testing.T) {
	s := NewSession(100, 16384, DefaultPolicy())
	password := "secret" + "pw"
	for _, c := range []struct{ text, want string }{
		{"postgres://user:" + password + "@db:5432/app", "postgres://user:" + Prefix + "SECRET_"},
		{"git@github.com:org/repo.git", "git@github.com:org/repo.git"},
		{"https://github.com/org/repo", "https://github.com/org/repo"},
		{"https://user@localhost/x", "https://user@localhost/x"},
		{"psql postgres://app:${PGPASSWORD}@db/app", "psql postgres://app:${PGPASSWORD}@db/app"},
		{"mysql://root:<password>@localhost", "mysql://root:<password>@localhost"},
	} {
		out, counts, err := s.Mask([]string{c.text})
		if err != nil || !strings.HasPrefix(out[0], c.want) || strings.Contains(out[0], password) {
			t.Fatalf("%q -> %q %v", c.text, out[0], err)
		}
		if strings.Contains(c.want, Prefix) && (!strings.HasSuffix(out[0], "]@db:5432/app") || counts.Secret != 1) {
			t.Fatalf("only the password must be masked: %q %+v", out[0], counts)
		}
	}
	policy := DefaultPolicy()
	policy.DisabledSecrets = []string{"url-credentials"}
	out, _, err := NewSession(100, 16384, policy).Mask([]string{"redis://default:" + password + "@cache:6379"})
	if err != nil || !strings.Contains(out[0], password) {
		t.Fatalf("a disabled pattern still masked: %q %v", out, err)
	}
}

func TestSecretMatchLimitFailsClosed(t *testing.T) {
	github := func(n int) string {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, "%s_%036d ", "ghp", i)
		}
		return b.String()
	}
	labelled := func(n int) string {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, "db_password = \"Q7%06d\"\n", i)
		}
		return b.String()
	}
	for name, build := range map[string]func(int) string{"github token": github, "labelled password": labelled} {
		for _, n := range []int{maxSecretMatches - 1, maxSecretMatches, maxSecretMatches + 1} {
			out, counts, err := NewSession(65536, 8<<20, DefaultPolicy()).Mask([]string{build(n)})
			if n > maxSecretMatches {
				if !errors.Is(err, ErrLimit) || out != nil {
					t.Fatalf("%s n=%d: want ErrLimit and no output, got err=%v outputs=%d", name, n, err, len(out))
				}
				continue
			}
			if err != nil || counts.Secret != n || strings.Contains(out[0], "ghp_") || strings.Contains(out[0], "\"Q7") {
				t.Fatalf("%s n=%d: err=%v secrets=%d", name, n, err, counts.Secret)
			}
		}
	}
}

func TestMatchLimitCoversAllDetectorsTogether(t *testing.T) {
	var b strings.Builder
	for i := range maxSecretMatches {
		fmt.Fprintf(&b, "%s_%036d ", "ghp", i)
	}
	for i := range 8192 - maxSecretMatches + 1 {
		fmt.Fprintf(&b, "u%d%s ", i, "@"+"example.com")
	}
	if _, _, err := NewSession(65536, 8<<20, DefaultPolicy()).Mask([]string{b.String()}); !errors.Is(err, ErrLimit) {
		t.Fatalf("8193 matches across detectors: want ErrLimit, got %v", err)
	}
}
