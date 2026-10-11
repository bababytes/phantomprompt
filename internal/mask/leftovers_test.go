package mask

import (
	"strings"
	"testing"
)

func TestLeftoversCountsPlaceholdersThatSurvivedRestore(t *testing.T) {
	policy := DefaultPolicy()
	s := NewSession(100, 16384, policy)
	masked, _, err := s.Mask([]string{"mail jan.kowalski" + "@" + "example.com, key AKIAIOSFODNN7EXAMPLE"})
	if err != nil {
		t.Fatal(err)
	}
	email := tokenPattern.FindAllString(masked[0], -1)[0]
	secret := tokenPattern.FindAllString(masked[0], -1)[1]
	if !strings.HasPrefix(email, Prefix+"EMAIL_") || !strings.HasPrefix(secret, Prefix+"SECRET_") {
		t.Fatalf("unexpected tokens %q %q", email, secret)
	}
	kept := make(map[string]bool)
	count := func(parts ...string) int {
		leftovers := s.Leftovers()
		for _, part := range parts {
			leftovers.Write([]byte(part))
		}
		return leftovers.Count(kept)
	}
	id := email[len(Prefix)+6 : len(email)-1]
	upper := Prefix + "EMAIL_" + strings.ToUpper(id) + "]"
	lower := strings.ToLower(email)
	truncated := strings.TrimSuffix(email, "]")
	withheld, count1 := s.RestoreExcept(`{"command":"curl -u `+email+` -H `+secret+`"}`, true, map[string]bool{"SECRET": true}, kept)
	if count1 != 1 || len(kept) != 1 || !kept[secret] {
		t.Fatalf("the shell command kept %d secrets, reported %v", count1, kept)
	}
	for _, c := range []struct {
		name  string
		parts []string
		want  int
	}{
		{"restored", []string{s.Restore("Write to " + email)}, 0},
		{"secret withheld in a shell command", []string{withheld}, 0},
		{"issued but not restored", []string{"Write to " + email}, 1},
		{"issued secret outside the withheld command", []string{withheld, " and " + secret}, 0},
		{"upper case", []string{"Write to " + upper}, 1},
		{"lower case", []string{"Write to " + lower + "."}, 1},
		{"cut short", []string{"Write to " + truncated + " now"}, 1},
		{"cut short at the end", []string{"Write to " + truncated}, 1},
		{"opening bracket missing", []string{"Write to " + email[1:] + " now"}, 1},
		{"opening bracket missing at the start", []string{email[1:]}, 1},
		{"space inside", []string{"Write to " + Prefix + "EMAIL_ " + id + "]"}, 1},
		{"space inside the id", []string{"Write to " + Prefix + "EMAIL_" + id[:3] + " " + id[3:] + "]"}, 1},
		{"shorter id", []string{"Write to " + Prefix + "EMAIL_" + id[:4] + "]"}, 1},
		{"unknown token", []string{"see " + Prefix + "VIN_abcdef]"}, 1},
		{"token of another session", []string{"see " + Prefix + "EMAIL_" + strings.Repeat("z", 6) + "]"}, 1},
		{"in a URL", []string{"https://example.test/u/" + upper + "/x"}, 1},
		{"in JSON", []string{`{"to":"` + truncated + `"}`}, 1},
		{"split between writes", []string{"to " + upper[:7], upper[7:] + " ok"}, 1},
		{"split inside a long text", []string{strings.Repeat("a ", 40) + "to " + upper[:12], upper[12:] + " ok" + strings.Repeat(" b", 40)}, 1},
		{"repeated by a final event", []string{"delta " + upper, "done " + upper + " completed " + upper}, 1},
		{"two different", []string{upper + " and " + Prefix + "VIN_abcdef]"}, 2},
		{"plain brackets", []string{"[PP] [pp_ [x] PP_EMAIL_handler [PP_FOO_BAR_BAZ] app_EMAIL_x1]"}, 0},
	} {
		if got := count(c.parts...); got != c.want {
			t.Errorf("%s: %d leftovers, want %d", c.name, got, c.want)
		}
	}
}
