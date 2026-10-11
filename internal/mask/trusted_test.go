package mask

import (
	"slices"
	"strings"
	"testing"
)

const trustedKey = "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\nabc/def+ghi=\n-----END RSA PRIVATE KEY-----"

func trustedFixture() (string, []string) {
	fake := "fixture.owner" + "@" + "example.com"
	short := "jo" + "@" + "ex.pl"
	fixture := "contact = \"" + fake + "\"\nDB_PASSWORD=fixture-Pass-1\ncar: TEST0000000000001\n" + short + "\n" + trustedKey + "\n"
	return fixture, []string{fake, "fixture-Pass-1", "TEST0000000000001", short, trustedKey}
}

func trustedSession(t *testing.T, values []string) *Session {
	t.Helper()
	policy := DefaultPolicy()
	policy.LenientFields = true
	session := NewSession(4096, 1<<20, policy)
	index := NewFileIndex()
	index.AddTrustedValues(values)
	session.SetFiles(index)
	return session
}

func TestValueScannerFindsWhatMaskingFinds(t *testing.T) {
	scanner, err := NewValueScanner(DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	fixture, want := trustedFixture()
	got := scanner.Values(fixture + fixture)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("found %q, want %q", got, want)
	}

	far := "late" + "@" + "example.com"
	long := strings.Repeat("filler line without values\n", 5000) + "contact: " + far + "\n"
	if values := scanner.Values(long); !slices.Equal(values, []string{far}) {
		t.Fatalf("value after the first scanned piece: %q", values)
	}
}

func TestTrustedValuesAreSentAsIsWherever(t *testing.T) {
	fixture, values := trustedFixture()
	if _, counts, err := trustedSession(t, nil).Mask([]string{fixture}); err != nil || counts.Email != 2 || counts.VIN != 1 || counts.Secret != 2 {
		t.Fatalf("without trust the fixture must be masked: %+v %v", counts, err)
	}
	session := trustedSession(t, values)
	var read strings.Builder
	for n, line := range strings.Split(strings.TrimSuffix(fixture, "\n"), "\n") {
		read.WriteString(strings.Repeat(" ", 5) + string(rune('1'+n)) + "→" + line + "\n")
	}
	texts := []string{
		fixture,
		read.String(),
		"src/test/CarTest.java:3:car: TEST0000000000001",
		"I copied " + values[0] + " and TEST0000000000001 into the new test, keeping " + values[3] + ".",
		`{"contact": "` + values[0] + `", "password": "fixture-Pass-1"}`,
	}
	for _, text := range texts {
		out, counts, err := session.Mask([]string{text})
		if err != nil || out[0] != text || counts.Total() != 0 {
			t.Fatalf("trusted values were masked: %q -> %q %+v %v", text, out, counts, err)
		}
	}
	other := "real.person" + "@" + "example.com"
	if out, _, err := session.Mask([]string{"also write to " + other}); err != nil || strings.Contains(out[0], other) {
		t.Fatalf("a value that is not in the folder must stay masked: %q %v", out, err)
	}
}

func TestValuesMaskedBeforeTheFolderWasTrustedPass(t *testing.T) {
	value := "jan.fixture" + "@" + "example.com"
	policy := DefaultPolicy()
	policy.LenientFields = true
	session := NewSession(4096, 1<<20, policy)
	text := "Write to " + value
	if out, _, err := session.Mask([]string{text}); err != nil || out[0] == text {
		t.Fatalf("value not masked before trust: %q %v", out, err)
	}
	index := NewFileIndex()
	index.AddTrustedValues([]string{value})
	session.SetFiles(index)
	if out, _, err := session.Mask([]string{text}); err != nil || out[0] != text {
		t.Fatalf("trusted value still masked: %q %v", out, err)
	}
}

func TestProtectedLinesStayMaskedWithTrustedValues(t *testing.T) {
	short := "jo" + "@" + "ex.pl"
	line := "IdentityFile ~/.ssh/deploy_key for " + short
	for _, blocked := range []bool{false, true} {
		session := trustedSession(t, []string{short})
		session.files.AddFile(session.files.AddFolder("/repo/secrets", blocked), line+"\n")
		if out, _, err := session.Mask([]string{line}); err != nil || out[0] == line {
			t.Fatalf("blocked=%v: protected line sent as is: %q %v", blocked, out, err)
		}
	}
}

func TestTrustedPathsValidation(t *testing.T) {
	policy := DefaultPolicy()
	policy.TrustedPaths = []string{"/repo/src/test", "~/work/fixtures"}
	policy.ProtectedPaths = []string{"/repo/src/test/secrets"}
	if err := ValidatePolicy(policy); err != nil {
		t.Fatalf("valid trusted folders rejected: %v", err)
	}
	for _, change := range []func(*Policy){
		func(p *Policy) { p.TrustedPaths = []string{"src/test"} },
		func(p *Policy) { p.TrustedPaths = []string{"/"} },
		func(p *Policy) { p.TrustedPaths = []string{"/repo/a", "/repo/a/"} },
		func(p *Policy) { p.ProtectedPaths = []string{"/repo/src/test/"} },
	} {
		invalid := DefaultPolicy()
		invalid.TrustedPaths = []string{"/repo/src/test"}
		change(&invalid)
		if err := ValidatePolicy(invalid); err != ErrPaths {
			t.Fatalf("%+v accepted: %v", invalid, err)
		}
	}
}
