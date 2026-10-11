package mask

import (
	"strings"
	"testing"
)

func lenientSession() *Session {
	policy := DefaultPolicy()
	policy.LenientFields = true
	return NewSession(4096, 1<<20, policy)
}

func TestLabelDetectedValuesStayMaskedWithoutLabel(t *testing.T) {
	policy := DefaultPolicy()
	policy.LenientFields = true
	policy.CustomFields = []string{"password"}
	s := NewSession(4096, 1<<20, policy)
	first, _, err := s.Mask([]string{"firstName: Jan\npassword: hunter2xyz"})
	if err != nil {
		t.Fatal(err)
	}
	restored := s.Restore("Hello " + strings.Fields(first[0])[1] + ", run mysql -p" + strings.Fields(first[0])[3])
	if restored != "Hello Jan, run mysql -phunter2xyz" {
		t.Fatalf("unexpected restore: %q", restored)
	}
	again, counts, err := s.Mask([]string{restored, `{"command":"mysql -phunter2xyz"}`})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(again, "\n")
	if strings.Contains(joined, "Jan") || strings.Contains(joined, "hunter2xyz") {
		t.Fatalf("label-detected value re-sent in clear: %q", joined)
	}
	if counts.New != 0 || counts.Fields != 3 {
		t.Fatalf("known values must reuse their placeholders: %+v", counts)
	}
	if s.Restore(again[0]) != restored {
		t.Fatal("re-masked text does not restore")
	}
}

func TestValueLearnedInRequestIsMaskedEverywhereInIt(t *testing.T) {
	s := lenientSession()
	out, counts, err := s.Mask([]string{"Jan wrote the report.", "firstName: Jan"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(out, " "), "Jan") || counts.New != 1 {
		t.Fatalf("value learned later in the request leaked earlier in it: %q %+v", out, counts)
	}
	again, _, err := s.Mask([]string{"Jan wrote the report.", "firstName: Jan"})
	if err != nil || again[0] != out[0] || again[1] != out[1] {
		t.Fatal("same history must mask identically on the next turn")
	}
}

func TestKnownValuesRespectWordsAndLength(t *testing.T) {
	s := lenientSession()
	if _, _, err := s.Mask([]string{"firstName: Jan\nlastName: Al"}); err != nil {
		t.Fatal(err)
	}
	out, _, err := s.Mask([]string{"January, Janet and Al stay; Jan is masked; (Jan) too."})
	if err != nil {
		t.Fatal(err)
	}
	for _, kept := range []string{"January", "Janet", "Al stay"} {
		if !strings.Contains(out[0], kept) {
			t.Fatalf("%q was changed: %q", kept, out[0])
		}
	}
	if strings.Count(out[0], "[PP_FIELD_") != 2 {
		t.Fatalf("whole-word occurrences not masked: %q", out[0])
	}
}

func TestForeignTokensNeverCollide(t *testing.T) {
	s := lenientSession()
	foreign := map[string]bool{}
	var texts []string
	for _, a := range tokenAlphabet {
		token := Prefix + "VIN_" + strings.Repeat(string(a), tokenIDLength) + "]"
		foreign[token] = true
		texts = append(texts, "seen "+token)
	}
	if _, _, err := s.Mask(texts); err != nil {
		t.Fatal(err)
	}
	for i := range 2000 {
		out, _, err := s.Mask([]string{vinFromIndex(i)})
		if err != nil {
			t.Fatal(err)
		}
		if foreign[out[0]] {
			t.Fatalf("issued token %s collides with foreign text", out[0])
		}
	}
}

func TestMemoKeepsResultsAndInvalidates(t *testing.T) {
	s := lenientSession()
	long := strings.Repeat("plain filler text without labels; ", 20) + "owner " + testVIN
	first, firstCounts, err := s.Mask([]string{long})
	if err != nil {
		t.Fatal(err)
	}
	cached, cachedCounts, err := s.Mask([]string{long})
	if err != nil || cached[0] != first[0] || cachedCounts.VIN != firstCounts.VIN || cachedCounts.New != 0 {
		t.Fatalf("memoized result differs: %q %+v", cached, cachedCounts)
	}
	withName := strings.Repeat("plain filler text without labels; ", 20) + "Ada was here"
	if out, _, _ := s.Mask([]string{withName}); !strings.Contains(out[0], "Ada") {
		t.Fatal("unknown word masked")
	}
	if _, _, err := s.Mask([]string{"firstName: Ada"}); err != nil {
		t.Fatal(err)
	}
	if out, _, _ := s.Mask([]string{withName}); strings.Contains(out[0], "Ada") {
		t.Fatal("stale memo kept a newly learned value in clear")
	}
	policy := s.Policy
	policy.CustomRules = []RegexRule{{Pattern: `filler`}}
	if err := s.SetRules(policy); err != nil {
		t.Fatal(err)
	}
	if out, _, _ := s.Mask([]string{long}); strings.Contains(out[0], "filler") {
		t.Fatal("stale memo survived a rule change")
	}
}

func TestPublicValuesAreNotMemoized(t *testing.T) {
	s := lenientSession()
	guid := "123e4567-e89b-12d3-a456-426614174000"
	text := strings.Repeat("context line; ", 30) + guid
	out, _, err := s.MaskExcept([]string{text}, []string{guid})
	if err != nil || !strings.Contains(out[0], guid) {
		t.Fatalf("public value masked: %q %v", out, err)
	}
	out, _, err = s.MaskExcept([]string{text}, nil)
	if err != nil || strings.Contains(out[0], guid) {
		t.Fatalf("value public only in an earlier request stayed in clear: %q %v", out, err)
	}
}

func TestSentTextKeepsItsFormForItsDestination(t *testing.T) {
	const destination = "api.example.test:443"
	s := lenientSession()
	note := "Ada was here"
	sent, _, err := s.MaskFor(destination, []string{note}, nil)
	if err != nil || sent[0] != note {
		t.Fatalf("unknown word masked: %q %v", sent, err)
	}
	if _, _, err := s.MaskFor(destination, []string{"firstName: Ada"}, nil); err != nil {
		t.Fatal(err)
	}
	if again, _, _ := s.MaskFor(destination, []string{note}, nil); again[0] != sent[0] {
		t.Fatalf("text already sent to %s changed after a value was learned: %q", destination, again)
	}
	if fresh, _, _ := s.MaskFor(destination, []string{"Ada called back"}, nil); strings.Contains(fresh[0], "Ada") {
		t.Fatal("learned value stayed in clear in a new text")
	}
	if other, _, _ := s.MaskFor("other.example.test:443", []string{note}, nil); strings.Contains(other[0], "Ada") {
		t.Fatal("form kept for one destination was sent to another")
	}
	if plain, _, _ := s.Mask([]string{note}); strings.Contains(plain[0], "Ada") {
		t.Fatal("form kept for a destination was used without one")
	}
	policy := s.Policy
	policy.CustomRules = []RegexRule{{Pattern: `here`}}
	if err := s.SetRules(policy); err != nil {
		t.Fatal(err)
	}
	if out, _, _ := s.MaskFor(destination, []string{note}, nil); strings.Contains(out[0], "Ada") || strings.Contains(out[0], "here") {
		t.Fatalf("kept form survived a rule change: %q", out)
	}
}

func TestResentTextCountsNoNewValues(t *testing.T) {
	const destination = "api.example.test:443"
	s := lenientSession()
	text := "owner " + testVIN
	_, first, err := s.MaskFor(destination, []string{text}, nil)
	if err != nil || first.New != 1 || first.VIN != 1 {
		t.Fatalf("first send %+v %v", first, err)
	}
	_, again, err := s.MaskFor(destination, []string{text}, nil)
	if err != nil || again.New != 0 || len(again.NewKinds) != 0 || again.VIN != 1 || again.TokenOverhead != first.TokenOverhead {
		t.Fatalf("re-sent text %+v %v", again, err)
	}
}

func TestValueLearnedInARequestIsMaskedAcrossItForADestination(t *testing.T) {
	const destination = "api.example.test:443"
	s := lenientSession()
	texts := []string{"Ada called", "firstName: Ada"}
	out, _, err := s.MaskFor(destination, texts, nil)
	if err != nil || strings.Contains(out[0], "Ada") {
		t.Fatalf("value learned in the same request stayed in clear: %q %v", out, err)
	}
	if again, _, _ := s.MaskFor(destination, texts, nil); again[0] != out[0] || again[1] != out[1] {
		t.Fatalf("re-sent request changed: %q %q", out, again)
	}
}

func TestPolicyWithOnlySecretsOrPathsIsValid(t *testing.T) {
	for _, policy := range []Policy{{Secrets: true}, {ProtectedPaths: []string{"~/.ssh"}}} {
		if err := ValidatePolicy(policy); err != nil {
			t.Fatalf("%+v rejected: %v", policy, err)
		}
	}
	if err := ValidatePolicy(Policy{}); err != ErrSession {
		t.Fatal("empty policy accepted")
	}
}

func BenchmarkMaskResentHistory(b *testing.B) {
	s := lenientSession()
	chunk := strings.Repeat("func handler() { // owner dev.team@example.test\n\tlog.Printf(\"ok\")\n}\n", 64)
	texts := make([]string, 400)
	for i := range texts {
		texts[i] = chunk + vinFromIndex(i)
	}
	if _, _, err := s.Mask(texts); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := s.Mask(texts); err != nil {
			b.Fatal(err)
		}
	}
}

func TestFieldNamesAsValuesAreNotLearned(t *testing.T) {
	s := lenientSession()
	for _, text := range []string{
		`map[string]string{"VIN": "vin", "EMAIL": "email", "FIELD": "fields"}`,
		`{"email": "Email"}`,
		"type User struct { Email string `json:\"email\"` }",
		"labels = {'vin': 'Vin', phone: Phone}",
		"<email>email</email>",
		"Send the email to the VIN owner; vin is checked.",
	} {
		out, _, err := s.Mask([]string{text})
		if err != nil || strings.Contains(out[0], Prefix) {
			t.Fatalf("a field name was masked as a value: %q -> %q %v", text, out[0], err)
		}
	}
	for _, text := range []string{
		`{"email": "jan.kowalski` + "@" + `example.com"}`,
		`{"vin": "` + testVIN + `"}`,
		`firstName: "Anna Nowak"`,
	} {
		out, _, err := s.Mask([]string{text})
		if err != nil || !strings.Contains(out[0], Prefix) {
			t.Fatalf("value left in clear: %q -> %q %v", text, out[0], err)
		}
	}
}
