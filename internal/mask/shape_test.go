package mask

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

func TestShapePatternCopiesTheLayout(t *testing.T) {
	cases := []struct {
		example       string
		caseSensitive bool
		pattern       string
		description   string
		matches       []string
		misses        []string
	}{
		{"abdsfsdb-ksabsdbvf-ahsgd", false, `(?i:\b[a-z]{8}-[a-z]{9}-[a-z]{5}\b)`, "8 letters “-” 9 letters “-” 5 letters, any letter case",
			[]string{"id QWERTYUI-asdfghjkl-zxcvb here"}, []string{"abdsfsdb-ksabsdbvf-ahsg", "xabdsfsdb-ksabsdbvf-ahsgd", "abdsfsdb_ksabsdbvf_ahsgd"}},
		{"550e8400-e29b-41d4-a716-446655440000", false, `(?i:\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b)`, "8 hex characters “-” 4 hex characters “-” 4 hex characters “-” 4 hex characters “-” 12 hex characters, any letter case",
			[]string{"3F2504E0-4F89-11D3-9A0C-0305E82C3301"}, []string{"zzzzzzzz-4f89-11d3-9a0c-0305e82c3301"}},
		{"PL12345678", false, `(?i:\b[a-z]{2}[0-9]{8}\b)`, "2 letters 8 digits, any letter case",
			[]string{"DE87654321"}, []string{"PL1234567", "PLN12345678"}},
		{"abc-x7k2-9f1", false, `(?i:\b[a-z]{3}-[a-z0-9]{4}-[a-z0-9]{3}\b)`, "3 letters “-” 4 letters or digits “-” 3 letters or digits, any letter case",
			[]string{"xyz-q1w2-zz9"}, []string{"xyz-q1w-zz9"}},
		{"AB-12", true, `\b[A-Z]{2}-[0-9]{2}\b`, "2 letters “-” 2 digits",
			[]string{"XY-99"}, []string{"ab-12", "AB-123"}},
		{"Faktura 2024/0001", false, `(?i:\b[a-z]{7}\s+[0-9]{4}/[0-9]{4}\b)`, "7 letters space 4 digits “/” 4 digits, any letter case",
			[]string{"faktura  2025/1234"}, []string{"Faktura 2025/12345"}},
		{"Żółw-12", false, `(?i:\p{L}{4}-[0-9]{2}\b)`, "4 letters “-” 2 digits, any letter case",
			[]string{"ŻÓŁW-34"}, []string{"Żółw-1"}},
	}
	for _, tc := range cases {
		pattern, description, err := ShapePattern(tc.example, tc.caseSensitive)
		if err != nil {
			t.Fatalf("%s: %v", tc.example, err)
		}
		if pattern != tc.pattern || description != tc.description {
			t.Fatalf("%s: got %q (%s)", tc.example, pattern, description)
		}
		re := regexp.MustCompile(pattern)
		if !re.MatchString(tc.example) {
			t.Fatalf("%s does not match its own example", pattern)
		}
		for _, text := range tc.matches {
			if !re.MatchString(text) {
				t.Fatalf("%s does not match %q", pattern, text)
			}
		}
		for _, text := range tc.misses {
			if re.MatchString(text) {
				t.Fatalf("%s matches %q", pattern, text)
			}
		}
	}
}

func TestShapePatternRejectsExamplesWithoutLayout(t *testing.T) {
	for _, example := range []string{"hello", "123456", "hello world", "x7k2m9p4", "x7k2 m9p4", strings.Repeat("a1.", 42) + "a1"} {
		if _, _, err := ShapePattern(example, false); !errors.Is(err, ErrShape) {
			t.Fatalf("%q: got %v", example, err)
		}
	}
	for _, example := range []string{"", "  ", strings.Repeat("x", 129), Prefix + "X_1", string([]byte{255})} {
		if _, _, err := ShapePattern(example, false); !errors.Is(err, ErrRegex) {
			t.Fatalf("%q: got %v", example, err)
		}
	}
}

func TestRuleNamesAreValidated(t *testing.T) {
	if _, err := CompileRules([]RegexRule{{Pattern: "Orion", Name: "Project name"}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{strings.Repeat("n", 65), "bad\nname", string([]byte{255})} {
		if _, err := CompileRules([]RegexRule{{Pattern: "Orion", Name: name}}); !errors.Is(err, ErrRegex) {
			t.Fatalf("name %q accepted", name)
		}
	}
}
