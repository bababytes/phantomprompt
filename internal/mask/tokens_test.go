package mask

import "testing"

func TestEstimateTokens(t *testing.T) {
	for text, want := range map[string]int{
		"":                       0,
		"   ":                    0,
		"hello":                  1,
		"TEST0000000000001":      6,
		"[PP_VIN_6x6843]":        10,
		"a.b@example.test":       8,
		"zażółć":                 3,
		"one two three":          3,
		"4111 1111 1111 1111":    8,
		"sk-ant-0123456789abcde": 9,
	} {
		if got := EstimateTokens(text); got != want {
			t.Errorf("%q: %d, want %d", text, got, want)
		}
	}
}

func TestMaskReportsTokenOverhead(t *testing.T) {
	s := NewSession(100, 16384, DefaultPolicy())
	out, counts, err := s.Mask([]string{"VIN TEST0000000000001 twice TEST0000000000001"})
	if err != nil || counts.VIN != 2 {
		t.Fatal(err)
	}
	token := out[0][len("VIN ") : len("VIN ")+len("[PP_VIN_123456]")]
	want := 2 * (EstimateTokens(token) - EstimateTokens("TEST0000000000001"))
	if counts.TokenOverhead != want || counts.Total() != 2 {
		t.Fatalf("overhead %d, want %d; total %d", counts.TokenOverhead, want, counts.Total())
	}
}

func TestNewValuesAreCountedOncePerSession(t *testing.T) {
	s := NewSession(100, 16384, DefaultPolicy())
	_, counts, err := s.Mask([]string{"TEST0000000000001 and TEST0000000000001", "test.user@example.test"})
	if err != nil || counts.VIN != 2 || counts.Email != 1 || counts.New != 2 {
		t.Fatalf("first call %+v %v", counts, err)
	}
	_, counts, err = s.Mask([]string{"history TEST0000000000001, new TEST0000000000002"})
	if err != nil || counts.VIN != 2 || counts.New != 1 {
		t.Fatalf("second call %+v %v", counts, err)
	}
}

func TestFieldCountsNameTheField(t *testing.T) {
	policy := DefaultPolicy()
	policy.CustomFields = []string{"codename"}
	policy.LenientFields = true
	_, counts, err := NewSession(100, 16384, policy).Mask([]string{"codename: x1\nfirstName: Ada\nfirst_name: Bob\n<codename>y2</codename>"})
	if err != nil || counts.Fields != 4 || counts.FieldNames["codename"] != 2 || counts.FieldNames["firstName"] != 2 || len(counts.FieldNames) != 2 {
		t.Fatalf("counts %+v err %v", counts, err)
	}
}

func TestTokenOverheadComparesWholeTexts(t *testing.T) {
	policy := DefaultPolicy()
	policy.CustomRules = []RegexRule{{Pattern: "ORION"}}
	policy.LenientFields = true
	mask := func(text string) (string, string, Counts) {
		t.Helper()
		out, counts, err := NewSession(100, 1<<20, policy).Mask([]string{text})
		if err != nil {
			t.Fatal(err)
		}
		return out[0], tokenPattern.FindString(out[0]), counts
	}
	if _, _, counts := mask("plain words, nothing to hide"); counts.TokenOverhead != 0 {
		t.Fatalf("unchanged text: %d", counts.TokenOverhead)
	}
	if _, token, counts := mask(`{"cardNumber":` + "4111" + "111111111111" + `}`); counts.TokenOverhead != (1+1+2+1+1+1+EstimateTokens(token)+1+1)-(1+1+2+1+1+6+1) {
		t.Fatalf("typed field: %d, token %d", counts.TokenOverhead, EstimateTokens(token))
	}
	if _, token, counts := mask("abcdefORIONg"); counts.TokenOverhead != (2+EstimateTokens(token)+1)-3 {
		t.Fatalf("substring: %d, token %d", counts.TokenOverhead, EstimateTokens(token))
	}
	guid := "5d1c" + "7e2a-3b4f-4c6d-8e9f-" + "0a1b2c3d4e5f"
	if _, token, counts := mask("id " + guid); counts.TokenOverhead >= 0 || counts.TokenOverhead != EstimateTokens(token)-EstimateTokens(guid) {
		t.Fatalf("a GUID shrinks the prompt: %d", counts.TokenOverhead)
	}
}

func TestTokenOverheadOfALearnedValueIsCountedOnce(t *testing.T) {
	value := "Q7xk2" + "LmP9zw"
	out, counts, err := NewSession(100, 1<<20, DefaultPolicy()).Mask([]string{"password: " + value + "\nlater " + value})
	if err != nil {
		t.Fatal(err)
	}
	token := tokenPattern.FindString(out[0])
	if counts.Secret != 2 || counts.TokenOverhead != 2*(EstimateTokens(token)-EstimateTokens(value)) {
		t.Fatalf("secret %d overhead %d, token %d value %d", counts.Secret, counts.TokenOverhead, EstimateTokens(token), EstimateTokens(value))
	}
}

func TestTokenOverheadOfABlockedText(t *testing.T) {
	files := NewFileIndex()
	line := "internal roadmap for the next quarter"
	files.AddFile(files.AddFolder("~/private", true), line)
	s := NewSession(100, 1<<20, DefaultPolicy())
	s.SetFiles(files)
	text := line + "\n" + "more lines of that file"
	out, counts, err := s.Mask([]string{text})
	if err != nil || out[0] != BlockedNotice {
		t.Fatalf("not blocked: %q %v", out, err)
	}
	if want := EstimateTokens(BlockedNotice) - EstimateTokens(text); counts.TokenOverhead != want {
		t.Fatalf("blocked text overhead %d, want %d", counts.TokenOverhead, want)
	}
}
