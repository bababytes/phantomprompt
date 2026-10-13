package mask

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

const testVIN = "TEST0000000000001"
const testPESEL = "00210100004"

func TestDetect(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		vin, pesel int
	}{
		{"both", testVIN + " " + testPESEL, 1, 1},
		{"lowercase", strings.ToLower(testVIN), 1, 0},
		{"numeric VIN", "12345678901234567", 1, 0},
		{"numeric prefix short VIN", "1234567890ABCDEF", 0, 0},
		{"numeric prefix full VIN", "12345678901ABCDEF", 1, 0},
		{"embedded", "x" + testVIN + "y", 0, 0},
		{"unicode boundary", "ż" + testVIN + "ę", 0, 0},
		{"punctuation", "(" + testVIN + ") PESEL=" + testPESEL, 1, 1},
		{"invalid VIN letter", "TEST000000000000I", 0, 0},
		{"invalid PESEL checksum still sensitive", "00210100005", 0, 1},
		{"short numbers", "1234567890 123456789012", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			counts := Counts{}
			for _, m := range Detect(tc.text) {
				if m.Kind == "VIN" {
					counts.VIN++
				} else {
					counts.PESEL++
				}
			}
			if counts.VIN != tc.vin || counts.PESEL != tc.pesel {
				t.Fatalf("unexpected detection counts: %+v", counts)
			}
		})
	}
}

func TestValidPESEL(t *testing.T) {
	if !ValidPESEL(testPESEL) {
		t.Fatal("synthetic valid PESEL rejected")
	}
	for _, value := range []string{"00210100005", "00990100004", "00213000000", "abcdefghijk", "123"} {
		if ValidPESEL(value) {
			t.Fatal("invalid PESEL accepted")
		}
	}
}

func TestPolicyAndRoundTrip(t *testing.T) {
	for _, policy := range []Policy{DefaultPolicy(), {VIN: true, PESEL: true}, {PESEL: true}} {
		s := NewSession(10, 1024, policy)
		text := "\"" + testVIN + "\" " + testPESEL + " " + testVIN + "\ną"
		out, counts, err := s.Mask([]string{text})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out[0], testVIN) == policy.VIN {
			t.Fatal("VIN policy not respected")
		}
		if strings.Contains(out[0], testPESEL) == policy.PESEL {
			t.Fatal("PESEL policy not respected")
		}
		if policy.VIN && counts.VIN != 2 {
			t.Fatal("repeated values not counted")
		}
		if s.Restore(out[0]) != text {
			t.Fatal("round trip changed original text")
		}
		again, _, err := s.Mask([]string{text})
		if err != nil || again[0] != out[0] {
			t.Fatal("mapping is not stable")
		}
		preserved, _, err := s.Mask(out)
		if err != nil || preserved[0] != out[0] {
			t.Fatal("known tokens not preserved")
		}
	}
}

func TestIsolationCapacityAndClear(t *testing.T) {
	a := NewSession(1, 1024, DefaultPolicy())
	b := NewSession(1, 1024, DefaultPolicy())
	first, _, err := a.Mask([]string{testVIN})
	if err != nil {
		t.Fatal(err)
	}
	if b.Restore(first[0]) != first[0] {
		t.Fatal("foreign token was restored")
	}
	if resent, _, err := b.Mask(first); err != nil || resent[0] != first[0] || b.Restore(resent[0]) != first[0] {
		t.Fatalf("foreign token must pass through as opaque text: %q %v", resent, err)
	}
	if _, _, err := a.Mask([]string{"TEST0000000000002"}); err != ErrLimit {
		t.Fatal("capacity not enforced")
	}
	if a.Restore(first[0]) != testVIN {
		t.Fatal("active entry was evicted")
	}
	b.Clear()
	if _, _, err := b.Mask([]string{testVIN}); err != ErrSession {
		t.Fatal("closed session accepted input")
	}
	a.Clear()
	if a.Restore(first[0]) != first[0] {
		t.Fatal("cleared mapping remains accessible")
	}
	limited := NewSession(1, 1024, DefaultPolicy())
	if _, _, err := limited.Mask([]string{testVIN, "TEST0000000000002"}); err != ErrLimit {
		t.Fatal("batch should exceed capacity")
	}
	if len(limited.forward) != 0 {
		t.Fatal("failed batch partially mutated map")
	}
}

func TestStreamEverySplit(t *testing.T) {
	s := NewSession(10, 2048, DefaultPolicy())
	masked, _, err := s.Mask([]string{"start " + testVIN + " end"})
	if err != nil {
		t.Fatal(err)
	}
	for split := 0; split <= len(masked[0]); split++ {
		stream := NewStream(s)
		out := stream.Push(masked[0][:split], false) + stream.Push(masked[0][split:], false) + stream.Push("", true)
		if out != "start "+testVIN+" end" {
			t.Fatalf("split %d did not restore original", split)
		}
	}
	stream := NewStream(s)
	if got := stream.Push("plain text", false); got != "plain text" {
		t.Fatal("ordinary text was buffered")
	}
	stream.Push(Prefix, false)
	if got := stream.Push("", true); got != Prefix {
		t.Fatal("truncated token was not flushed")
	}
	stream = NewStream(s)
	stream.Push(Prefix+strings.Repeat("A", 1024), false)
	if len(stream.pending) > 64 {
		t.Fatal("stream buffer grew without bound")
	}
}

func TestConcurrentStableMapping(t *testing.T) {
	s := NewSession(100, 4096, DefaultPolicy())
	var wg sync.WaitGroup
	results := make(chan string, 64)
	for i := 0; i < cap(results); i++ {
		wg.Go(func() {
			out, _, err := s.Mask([]string{testVIN})
			if err != nil {
				t.Error(err)
				return
			}
			results <- out[0]
		})
	}
	wg.Wait()
	close(results)
	first := ""
	for token := range results {
		if first == "" {
			first = token
		}
		if token != first {
			t.Fatal("concurrent requests generated inconsistent mappings")
		}
	}
}

func TestStoreLifecycle(t *testing.T) {
	now := time.Now()
	store := NewStore(time.Minute, time.Hour, 2, DefaultPolicy())
	store.now = func() time.Time { return now }
	id, err := store.Create(DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	s, release, err := store.Acquire(id, "provider-a")
	if err != nil {
		t.Fatal(err)
	}
	masked, _, _ := s.Mask([]string{testVIN})
	now = now.Add(2 * time.Minute)
	store.Sweep()
	if s.Restore(masked[0]) != testVIN {
		t.Fatal("active stream lost mapping on idle TTL")
	}
	if _, _, err := store.Acquire(id, "provider-b"); err != ErrSession {
		t.Fatal("session crossed provider boundary")
	}
	release()
	release()
	now = now.Add(2 * time.Minute)
	store.Sweep()
	if _, _, err := store.Acquire(id, "provider-a"); err != ErrSession {
		t.Fatal("expired session reused")
	}
	if s.Restore(masked[0]) != masked[0] {
		t.Fatal("expired map not cleared")
	}
	id, _ = store.Create(DefaultPolicy())
	_, release, _ = store.Acquire(id, "provider-a")
	store.Delete(id)
	if _, _, err := store.Acquire(id, "provider-a"); err != ErrSession {
		t.Fatal("closing session acquired")
	}
	release()
	if len(store.entries) != 0 {
		t.Fatal("deletion did not complete after release")
	}
}

func BenchmarkMask100KiB(b *testing.B) {
	s := NewSession(4096, 512<<10, DefaultPolicy())
	text := strings.Repeat("synthetic text "+testVIN+". ", 3104)
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := s.Mask([]string{text}); err != nil {
			b.Fatal(err)
		}
	}
}

func TestRestoreJSONEscapesValues(t *testing.T) {
	policy := DefaultPolicy()
	policy.LenientFields = true
	s := NewSession(100, 16384, policy)
	out, _, err := s.Mask([]string{`firstName: "Ann \"Q\" Lee"`, `{"cardNumber":4111111111111111}`, "TEST0000000000001"})
	if err != nil {
		t.Fatal(err)
	}
	quotedName := strings.TrimSuffix(strings.TrimPrefix(out[0], `firstName: "`), `"`)
	arguments := `{"new_str":"owner ` + quotedName + ` vin ` + out[2] + `","card":` + strings.TrimSuffix(strings.TrimPrefix(out[1], `{"cardNumber":`), "}") + `}`
	restored := s.RestoreJSON(arguments)
	var decoded map[string]any
	if err := json.Unmarshal([]byte(restored), &decoded); err != nil {
		t.Fatalf("restored arguments are not JSON: %s", restored)
	}
	if decoded["new_str"] != `owner Ann \"Q\" Lee vin TEST0000000000001` || decoded["card"] != float64(4111111111111111) {
		t.Fatalf("restored %v", decoded)
	}
	stream := NewJSONStream(s)
	var b strings.Builder
	for _, r := range arguments {
		b.WriteString(stream.Push(string(r), false))
	}
	b.WriteString(stream.Push("", true))
	if b.String() != restored {
		t.Fatalf("split JSON stream restored %q", b.String())
	}
}

func TestShortTokenFormatAndUniqueness(t *testing.T) {
	format := regexp.MustCompile(`^\[PP_(?:VIN|EMAIL|GUID|FIELD|CUSTOM|PESEL)_[a-z2-9]{6}\]$`)
	s := NewSession(65536, 8<<20, DefaultPolicy())
	texts := make([]string, 4000)
	for i := range texts {
		texts[i] = "vehicle " + vinFromIndex(i)
	}
	out, counts, err := s.Mask(texts)
	if err != nil || counts.VIN != 4000 {
		t.Fatalf("masking failed: counts=%+v err=%v", counts, err)
	}
	seen := map[string]bool{}
	for i, text := range out {
		token := text[len("vehicle "):]
		if !format.MatchString(token) {
			t.Fatalf("token %q does not match the short format", token)
		}
		if seen[token] {
			t.Fatal("duplicate token generated")
		}
		seen[token] = true
		if s.Restore(text) != texts[i] {
			t.Fatal("restore failed")
		}
	}
}

func vinFromIndex(i int) string {
	const chars = "ABCDEFGHJKLMNPRSTUVWXYZ0123456789"
	b := []byte("TEST00000000000000")[:17]
	b[10] = chars[i%len(chars)]
	b[11] = chars[(i/len(chars))%len(chars)]
	b[12] = chars[(i/len(chars)/len(chars))%len(chars)]
	return string(b)
}

func TestLoosePrefixInUserTextIsNotReserved(t *testing.T) {
	s := NewSession(100, 16384, DefaultPolicy())
	out, _, err := s.Mask([]string{"config macro [PP_MAX] and array[PP_IDX] stay as text " + testVIN})
	if err != nil {
		t.Fatalf("code-like prefix rejected: %v", err)
	}
	if !strings.Contains(out[0], "[PP_MAX]") || !strings.Contains(out[0], "[PP_IDX]") || strings.Contains(out[0], testVIN) {
		t.Fatalf("unexpected masking: %q", out[0])
	}
	masked, _, _ := s.Mask([]string{testVIN})
	foreign := "[PP_VIN_zzzzz9]"
	if foreign == masked[0] {
		t.Skip("improbable id collision")
	}
	if _, _, err := s.Mask([]string{"resend " + masked[0] + " ok"}); err != nil {
		t.Fatal("our own token should pass on resend")
	}
	out, _, err = s.Mask([]string{"foreign " + foreign})
	if err != nil || out[0] != "foreign "+foreign || s.Restore(out[0]) != out[0] {
		t.Fatalf("foreign strict-shaped token must stay opaque and never restore: %q %v", out, err)
	}
}

func TestForeignPlaceholdersCountTowardTheSessionBudget(t *testing.T) {
	alphabet := "abcdefghijkmnpqrstuvwxyz23456789"
	foreign := func(i int) string {
		id := []byte("aaaaaa")
		for j := range id {
			id[j] = alphabet[i%32]
			i /= 32
		}
		return Prefix + "VIN_" + string(id) + "]"
	}
	s := NewSession(50, 1<<20, DefaultPolicy())
	other := NewSession(50, 1<<20, DefaultPolicy())
	sent := 0
	var err error
	for ; sent < 200 && err == nil; sent++ {
		_, _, err = s.Mask([]string{"note " + foreign(sent)})
	}
	if !errors.Is(err, ErrLimit) || sent > 51 {
		t.Fatalf("unknown placeholders grew past the budget: %d requests, err %v", sent, err)
	}
	size := len(s.foreign)
	if _, _, err := s.Mask([]string{"note " + foreign(500)}); !errors.Is(err, ErrLimit) || len(s.foreign) != size {
		t.Fatalf("a refused request still grew the registry: %d -> %d, err %v", size, len(s.foreign), err)
	}
	if out, _, err := other.Mask([]string{"note " + foreign(500) + " " + testVIN}); err != nil || !strings.Contains(out[0], foreign(500)) {
		t.Fatalf("another session was affected: %v", err)
	}
	s.Clear()
	if len(s.foreign) != 0 {
		t.Fatal("registry kept after the session closed")
	}
}

func TestSideChannelsBlockOnlyRealPESELNumbers(t *testing.T) {
	policy := DefaultPolicy()
	policy.PESEL = true
	s := NewSession(4096, 1<<20, policy)
	if kinds, err := s.Unmasked(`{"system.memory.totalBytes":38654705664}`); err != nil || len(kinds) != 0 {
		t.Fatalf("a memory size was treated as a PESEL: %v %v", kinds, err)
	}
	if kinds, err := s.Unmasked(`{"payload":"` + testPESEL + `"}`); err != nil || len(kinds) != 1 || kinds[0] != "PESEL" {
		t.Fatalf("a real PESEL passed a side channel: %v %v", kinds, err)
	}
	const typo = "00210100005"
	if _, _, err := s.Mask([]string{"client " + typo}); err != nil {
		t.Fatal(err)
	}
	if kinds, err := s.Unmasked(`{"payload":"` + typo + `"}`); err != nil || len(kinds) != 1 || kinds[0] != "PESEL" {
		t.Fatalf("a mistyped PESEL from the conversation passed a side channel: %v %v", kinds, err)
	}
}

func TestSideChannelsBlockOnlyPlausibleVINs(t *testing.T) {
	s := NewSession(4096, 1<<20, DefaultPolicy())
	for _, body := range []string{`{"verdict_confidence":0.98765432109876543}`, `{"id":"deadbeefcafebab17"}`} {
		if kinds, err := s.Unmasked(body); err != nil || len(kinds) != 0 {
			t.Fatalf("%s was treated as a VIN: %v %v", body, kinds, err)
		}
	}
	if kinds, err := s.Unmasked(`{"vehicle":"1HGCM82633A004352"}`); err != nil || len(kinds) != 1 || kinds[0] != "VIN" {
		t.Fatalf("a real VIN passed a side channel: %v %v", kinds, err)
	}
	const unchecked = "1HGCM82643A004352"
	if _, _, err := s.Mask([]string{"vehicle " + unchecked}); err != nil {
		t.Fatal(err)
	}
	if kinds, err := s.Unmasked(`{"vehicle":"` + unchecked + `"}`); err != nil || len(kinds) != 1 || kinds[0] != "VIN" {
		t.Fatalf("a VIN from the conversation passed a side channel: %v %v", kinds, err)
	}
}
