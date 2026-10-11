package mask

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMainFieldsAndCustomNames(t *testing.T) {
	for _, name := range append(DefaultFields(), "employeeId", "first_name", "LAST-NAME") {
		t.Run(name, func(t *testing.T) {
			p := DefaultPolicy()
			p.CustomFields = []string{"employeeId"}
			s := NewSession(100, 16384, p)
			input := `{"` + name + `":"Synthetic Person"}`
			out, counts, err := s.Mask([]string{input})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out[0], "Synthetic Person") || counts.Fields != 1 || !json.Valid([]byte(out[0])) {
				t.Fatal("field was not masked as valid JSON")
			}
			if s.Restore(out[0]) != input {
				t.Fatal("field was not restored")
			}
		})
	}
}

func TestFieldsValuesAndStreaming(t *testing.T) {
	inputs := []string{
		`{"cardNumber":4111111111111111,"address":{"street":"Synthetic Street","zipCode":"00-000"}}`,
		`{"name":"Test \"Quoted\" User","phone":["+48 000 000 000","+48 111 111 111"]}`,
		`{"name":"Test \"Quoted\" User","accountNumber":9007199254740993}`,
		`{"email":null,"mobile":false}`,
		"firstName: Test User\nphone = +48 000 000 000\naddress: Synthetic Street 1, Synthetic City",
		"Contact TEST0000000000001@example.test about TEST0000000000001",
		"Here is a record:\n```json\n{\"firstName\":\"Test\",\"deviceId\":\"synthetic-id\"}\n```",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			p := DefaultPolicy()
			p.CustomFields = []string{"deviceId"}
			s := NewSession(100, 16384, p)
			out, _, err := s.Mask([]string{input})
			if err != nil {
				t.Fatal(err)
			}
			if out[0] == input {
				t.Fatal("nothing was masked")
			}
			if json.Valid([]byte(input)) && !json.Valid([]byte(out[0])) {
				t.Fatal("JSON structure was corrupted")
			}
			if got := s.Restore(out[0]); got != input {
				t.Fatalf("restoration mismatch: %q", got)
			}
			for split := 0; split <= len(out[0]); split++ {
				stream := NewStream(s)
				got := stream.Push(out[0][:split], false) + stream.Push(out[0][split:], false) + stream.Push("", true)
				if got != input {
					t.Fatalf("stream mismatch at split %d: %q", split, got)
				}
			}
			stream := NewStream(s)
			var restored strings.Builder
			for _, r := range out[0] {
				restored.WriteString(stream.Push(string(r), false))
			}
			restored.WriteString(stream.Push("", true))
			if restored.String() != input {
				t.Fatal("byte-by-byte restoration failed")
			}
		})
	}
}

func TestFieldsConfigurationAndAtomicFailure(t *testing.T) {
	for _, names := range [][]string{{""}, {"name.*"}, {"private value"}, make([]string, 65)} {
		if _, err := FieldNames(names, nil); err == nil {
			t.Fatal("invalid field names accepted")
		}
	}
	s := NewSession(100, 16384, DefaultPolicy())
	if _, _, err := s.Mask([]string{`{"name":"Synthetic User"}`, `{"address":{"broken":`}); err != ErrFields {
		t.Fatal("malformed protected value accepted")
	}
	for _, bad := range []string{`{"phone":123 456}`, `firstName: "Test" User`} {
		if _, _, err := s.Mask([]string{bad}); err != ErrFields {
			t.Fatal("ambiguous field value accepted")
		}
	}
	if len(s.forward) != 0 {
		t.Fatal("failed batch committed mapping")
	}
	input := `{"status":"ok","deviceId":"synthetic-device"}`
	out, _, err := s.Mask([]string{input})
	if err != nil || out[0] != input {
		t.Fatal("unknown field unexpectedly masked")
	}
}

func TestGUIDDetectionAndIsolation(t *testing.T) {
	const guid = "123e4567-e89b-12d3-a456-426614174000"
	for _, input := range []string{guid, strings.ToUpper(guid), "Reference GUID: {" + guid + "}"} {
		a := NewSession(100, 16384, DefaultPolicy())
		b := NewSession(100, 16384, DefaultPolicy())
		out, counts, err := a.Mask([]string{input})
		if err != nil || counts.GUID != 1 || strings.Contains(out[0], guid) {
			t.Fatal("GUID not masked")
		}
		if a.Restore(out[0]) != input {
			t.Fatal("GUID not restored")
		}
		if b.Restore(out[0]) != out[0] {
			t.Fatal("GUID restored outside its session")
		}
	}
	for _, input := range []string{"prefix" + guid, guid + "f", "123e4567-e89b-12d3-a456-42661417400Z"} {
		s := NewSession(100, 16384, Policy{GUID: true})
		out, counts, err := s.Mask([]string{input})
		if err != nil || counts.GUID != 0 || out[0] != input {
			t.Fatal("invalid GUID boundary accepted")
		}
	}
}

func TestMaskExceptLeavesPublicGUIDs(t *testing.T) {
	const public, private = "00000000-0000-4000-8000-000000000001", "123e4567-e89b-12d3-a456-426614174000"
	s := NewSession(100, 16384, DefaultPolicy())
	out, counts, err := s.MaskExcept([]string{"scratchpad /tmp/" + public + "/x ticket " + private}, []string{public})
	if err != nil || counts.GUID != 1 || !strings.Contains(out[0], "/tmp/"+public+"/x ticket ") || strings.Contains(out[0], private) {
		t.Fatalf("out %q counts %+v err %v", out, counts, err)
	}
	again, counts, err := s.Mask([]string{public})
	if err != nil || counts.GUID != 1 || again[0] == public {
		t.Fatal("exemption outlived its request")
	}
}

func TestGUIDs(t *testing.T) {
	const account, session = "00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000003"
	userID := `{"device_id":"` + strings.Repeat("ab", 32) + `","account_uuid":"` + account + `","session_id":"` + session + `","note":"x` + session + `"}`
	if got := GUIDs(userID); len(got) != 2 || got[0] != account || got[1] != session {
		t.Fatalf("GUIDs %q", got)
	}
	if got := GUIDs(""); got != nil {
		t.Fatalf("GUIDs of empty text %q", got)
	}
}

func TestFieldTokensCanBeResubmitted(t *testing.T) {
	s := NewSession(100, 16384, DefaultPolicy())
	input := `{"name":"Synthetic User","accountNumber":9007199254740993}`
	first, _, err := s.Mask([]string{input})
	if err != nil {
		t.Fatal(err)
	}
	second, counts, err := s.Mask(first)
	if err != nil || second[0] != first[0] || counts.Fields != 0 {
		t.Fatal("known token was remasked")
	}
	if s.Restore(second[0]) != input {
		t.Fatal("round trip lost the original type")
	}
}

func TestLenientFieldsForCode(t *testing.T) {
	policy := DefaultPolicy()
	policy.LenientFields = true
	s := NewSession(100, 16384, policy)
	inputs := []string{
		`const user = { firstName: "Ada Lovelace" + suffix, id: 1 }`,
		`Set firstName: "Unterminated Person`,
		`config = { address: {broken`,
		`{"phone":123 456}`,
	}
	out, counts, err := s.Mask(inputs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out[0], "Ada Lovelace") || !strings.Contains(out[0], `+ suffix, id: 1 }`) || strings.Contains(out[1], "Unterminated Person") || out[2] != inputs[2] || strings.Contains(out[3], "123") || strings.Contains(out[3], "456") {
		t.Fatalf("lenient masking: %q", out)
	}
	if counts.Fields != 3 {
		t.Fatalf("fields masked: %d", counts.Fields)
	}
	for i := range inputs {
		if s.Restore(out[i]) != inputs[i] {
			t.Fatalf("restore %d: %q", i, s.Restore(out[i]))
		}
	}
}

func TestCodeNextToFieldLabelsStaysReadable(t *testing.T) {
	policy := DefaultPolicy()
	policy.LenientFields = true
	s := NewSession(100, 16384, policy)
	code := []string{
		"@dataclass\nclass Customer:\n    firstName: str\n    email: str\n    vin: str | None = None\n",
		"def notify(customer: Customer, address: Address) -> str:\n    return customer.email",
		`ADDRESS = re.compile(r"[a-z0-9.]+@example\.test", re.IGNORECASE)`,
		"interface Customer {\n  firstName: string;\n  phone: PhoneNumber;\n  address: Address;\n}",
		"const payload = { firstName: firstName, email: form.email, phone: user.phone };",
		"data class Customer(val firstName: String, val email: EmailAddress?, val vin: Option<String>)",
		"pub struct Owner {\n    pub email: String,\n    pub firstName: Option<String>,\n}",
		"private fun sendReport(address: String, tenant: String) {",
		"val context = this@MainActivity.applicationContext",
		"REPO=\"git@github.com:acme/fleet.git\"\nEMAIL=$ADMIN_EMAIL\nfirstName=${FIRST_NAME}",
	}
	out, counts, err := s.Mask(code)
	if err != nil {
		t.Fatal(err)
	}
	for i := range code {
		if out[i] != code[i] {
			t.Errorf("code changed:\n%s\n%s", code[i], out[i])
		}
	}
	if counts.Total() != 0 || counts.New != 0 {
		t.Fatalf("counts %+v", counts)
	}
}

func TestDataNextToFieldLabelsIsStillMasked(t *testing.T) {
	policy := DefaultPolicy()
	policy.LenientFields = true
	cases := map[string]string{
		"firstName: Anna":                                   "Anna",
		"lastName: Long":                                    "Long",
		"lastName: List":                                    "List",
		"fullName: Anna Kowalczyk":                          "Kowalczyk",
		"address: ul.Mickiewicza 5":                         "Mickiewicza",
		"address: Długa 5, Warszawa":                        "Długa",
		"phone: +48 000 000 002":                            "000 000 002",
		"vin: " + testVIN:                                   testVIN,
		"email: none, lastName: Kowalczyk":                  "Kowalczyk",
		"user firstName=anna lastName=nowak":                "nowak",
		`DEFAULT = Customer(firstName="Anna", vin=vin)`:     "Anna",
		"Contact anna.kowalczyk@example.test: call after 5": "anna.kowalczyk@example.test",
		"anna.kowalczyk@example.test:hunter2":               "anna.kowalczyk@example.test",
		"write to this@example.test":                        "this@example.test",
		"firstName: J.Kowalski":                             "Kowalski",
		"send it to git@example.test: today":                "git@example.test",
	}
	for text, value := range cases {
		out, _, err := NewSession(100, 16384, policy).Mask([]string{text})
		if err != nil || strings.Contains(out[0], value) {
			t.Errorf("%q sent %q in clear: %q %v", text, value, out, err)
		}
	}
}

func TestDisabledBuiltInFields(t *testing.T) {
	policy := DefaultPolicy()
	policy.LenientFields = true
	policy.DisabledFields = []string{"phone", "card_number"}
	s := NewSession(100, 16384, policy)
	out, counts, err := s.Mask([]string{"phone: 000 000 000\ncardNumber: 4111\nfirstName: Ada"})
	if err != nil || counts.Fields != 1 || !strings.Contains(out[0], "phone: 000 000 000") || !strings.Contains(out[0], "cardNumber: 4111") || strings.Contains(out[0], "Ada") {
		t.Fatalf("out %q counts %+v err %v", out, counts, err)
	}
	for _, disabled := range [][]string{{"employeeId"}, {"1phone"}} {
		if _, err := FieldNames(nil, disabled); err != ErrFields {
			t.Fatalf("disabled %v accepted", disabled)
		}
	}
	names, err := FieldNames([]string{"phone"}, []string{"phone"})
	if err != nil || !names["phone"] {
		t.Fatal("custom field cannot re-enable a disabled built-in name")
	}
}

func TestSetRulesKeepsTokensAndChannel(t *testing.T) {
	policy := DefaultPolicy()
	policy.LenientFields = true
	s := NewSession(100, 16384, policy)
	first, _, err := s.Mask([]string{"owner email test.user@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	updated := DefaultPolicy()
	updated.CustomFields = []string{"employeeId"}
	updated.DisabledFields = []string{"firstName"}
	if err := s.SetRules(updated); err != nil {
		t.Fatal(err)
	}
	out, counts, err := s.Mask([]string{`employeeId: "E-1" firstName: "Ada"`})
	if err != nil || counts.Fields != 1 || strings.Contains(out[0], "E-1") || !strings.Contains(out[0], "Ada") || !s.Policy.LenientFields {
		t.Fatalf("out %q counts %+v err %v lenient %v", out, counts, err, s.Policy.LenientFields)
	}
	if s.Restore(first[0]) != "owner email test.user@example.test" {
		t.Fatal("token from before the rule change no longer restores")
	}
	if err := s.SetRules(Policy{}); err != ErrSession {
		t.Fatalf("empty policy accepted: %v", err)
	}
}

func TestStoreSetPolicyUpdatesLiveAndEphemeralSessions(t *testing.T) {
	store := NewStore(time.Minute, time.Hour, 8, DefaultPolicy())
	id, err := store.Create(DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	updated := DefaultPolicy()
	updated.CustomFields = []string{"employeeId"}
	if err := store.SetPolicy(updated); err != nil {
		t.Fatal(err)
	}
	for _, sessionID := range []string{id, ""} {
		session, release, err := store.Acquire(sessionID, "test")
		if err != nil {
			t.Fatal(err)
		}
		_, counts, err := session.Mask([]string{`{"employeeId":"E-1"}`})
		release()
		if err != nil || counts.Fields != 1 {
			t.Fatalf("session %q ignored the new policy: %+v %v", sessionID, counts, err)
		}
	}
	if err := store.SetPolicy(Policy{CustomRules: []RegexRule{{Pattern: `\b`}}}); err == nil {
		t.Fatal("invalid policy accepted")
	}
}

func TestFieldFormsQuotedXMLAndYAML(t *testing.T) {
	for _, lenient := range []bool{true, false} {
		policy := DefaultPolicy()
		policy.CustomFields = []string{"codename"}
		policy.LenientFields = lenient
		for _, tc := range []struct{ text, hidden, kept string }{
			{`'codename': 'hidden'`, "hidden", `'codename': '[PP_FIELD_`},
			{`{'codename': 'hidden', 'other': 1}`, "hidden", `', 'other': 1}`},
			{`codename = 'hidden' + suffix`, "hidden", `' + suffix`},
			{`"codename": 'hidden'`, "hidden", `"codename": '[PP_FIELD_`},
			{`<codename>hidden</codename>`, "hidden", `<codename>[PP_FIELD_`},
			{`<cfg:codename id="1">  hidden value  </cfg:codename><x>keep</x>`, "hidden value", `<x>keep</x>`},
			{"codename:\n  hidden\nnext: 1", "hidden", "\nnext: 1"},
			{"codename: |\n  line one\n  line two\nother: x", "line one", "other: x"},
			{"- codename:\n    hidden\n- other", "hidden", "\n- other"},
			{"codename:\n- a1b2c3\n- d4e5f6\nz: 1", "d4e5f6", "\nz: 1"},
		} {
			out, counts, err := NewSession(100, 16384, policy).Mask([]string{tc.text})
			if err != nil || counts.Fields != 1 || strings.Contains(out[0], tc.hidden) || !strings.Contains(out[0], tc.kept) {
				t.Errorf("lenient=%v %q → %q (fields %d, err %v)", lenient, tc.text, out, counts.Fields, err)
			}
		}
		for _, text := range []string{"<codename>no closing tag", "codename:\nnot indented", "codename - prose", "<other>codename</other>", "codename:", "codename: |"} {
			out, counts, err := NewSession(100, 16384, policy).Mask([]string{text})
			if err != nil || counts.Fields != 0 || out[0] != text {
				t.Errorf("lenient=%v %q changed to %q (fields %d, err %v)", lenient, text, out, counts.Fields, err)
			}
		}
	}
}

func TestUnquotedValueStopsBeforeProsePunctuation(t *testing.T) {
	policy := DefaultPolicy()
	policy.CustomFields = []string{"codename"}
	policy.LenientFields = true
	s := NewSession(100, 16384, policy)
	out, _, err := s.Mask([]string{"is there a codename: hahahahaha ?", "codename: hahahahaha", "codename: secret?", "codename: a b ."})
	if err != nil {
		t.Fatal(err)
	}
	question := strings.TrimSuffix(strings.TrimPrefix(out[0], "is there a codename: "), " ?")
	if question != strings.TrimPrefix(out[1], "codename: ") || !strings.HasSuffix(out[0], " ?") {
		t.Fatalf("same value got different tokens: %q", out)
	}
	if strings.Contains(out[2], "secret") || strings.HasSuffix(out[2], "?") || !strings.HasSuffix(out[3], " .") || strings.Contains(out[3], "a b") {
		t.Fatalf("punctuation handling %q", out)
	}
}

func TestSameValueInProseAndMarkdownGetsOneToken(t *testing.T) {
	policy := DefaultPolicy()
	policy.CustomFields = []string{"codename"}
	policy.LenientFields = true
	s := NewSession(100, 16384, policy)
	texts := []string{"codename: hahahahaha", "Line 7 is codename: hahahahaha.", "is it codename: hahahahaha ?", "See `codename: hahahahaha`.", "**codename: hahahahaha**", "codename: Secret1!"}
	out, counts, err := s.Mask(texts)
	if err != nil || counts.New != 2 {
		t.Fatalf("new %d err %v out %q", counts.New, err, out)
	}
	if !strings.HasSuffix(out[1], "].") || !strings.HasSuffix(out[3], "]`.") || !strings.HasSuffix(out[4], "]**") || strings.Contains(out[5], "!") {
		t.Fatalf("tails %q", out)
	}
}

func TestEnclosedFieldEndsAtClosingMark(t *testing.T) {
	policy := DefaultPolicy()
	policy.CustomFields = []string{"codename"}
	policy.LenientFields = true
	s := NewSession(100, 16384, policy)
	texts := []string{
		"codename: hahahahaha",
		`grep -n "codename: hahahahaha" test`,
		`grep -c 'codename: hahahahaha' test`,
		"Line 7 (codename: hahahahaha) is in the file.",
		"See `codename: hahahahaha` and **codename: hahahahaha** here",
		"[codename: hahahahaha]",
	}
	out, counts, err := s.Mask(texts)
	if err != nil || counts.New != 1 || counts.Fields != 7 {
		t.Fatalf("new %d fields %d err %v out %q", counts.New, counts.Fields, err, out)
	}
	for _, want := range []string{`" test`, `' test`, ") is in the file.", "` and **codename: [PP_FIELD_", "** here", "]"} {
		found := false
		for _, line := range out {
			found = found || strings.Contains(line, want)
		}
		if !found {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
}
