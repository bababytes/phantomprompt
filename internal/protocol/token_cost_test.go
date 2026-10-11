package protocol

import (
	"bytes"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"phantomprompt/internal/mask"
)

const costGUID = "123e4567-e89b-12d3-a456-426614174000"
const costAWSKey = "AKIAABCDEFGHJKLMNPRS"

var issuedToken = regexp.MustCompile(`\[PP_[A-Z]+_[a-z2-9]{6}\]`)

func claudeCodeRequest(t *testing.T, messages []any) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"model":      "synthetic-model",
		"max_tokens": 32000,
		"stream":     true,
		"system": []any{
			map[string]any{"type": "text", "text": "You are a coding agent. Working directory: /tmp/project"},
			map[string]any{"type": "text", "text": "Use the tools below.", "cache_control": map[string]any{"type": "ephemeral"}},
		},
		"tools":    []any{map[string]any{"name": "Read", "description": "Reads a file", "input_schema": map[string]any{"type": "object", "properties": map[string]any{"file_path": map[string]any{"type": "string"}}}}},
		"messages": messages,
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func decodeWithNumbers(t *testing.T, data []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func maskedParts(t *testing.T, data []byte) (prefix json.RawMessage, messages []json.RawMessage) {
	t.Helper()
	var root struct {
		System   json.RawMessage   `json:"system"`
		Tools    json.RawMessage   `json:"tools"`
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	return append(append(json.RawMessage{}, root.System...), root.Tools...), root.Messages
}

func TestMaskingWithoutProtectedValuesKeepsContent(t *testing.T) {
	body := claudeCodeRequest(t, []any{
		map[string]any{"role": "user", "content": "Read config.yaml and explain the retry settings."},
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "Reading it now."},
			map[string]any{"type": "tool_use", "id": "toolu_01", "name": "Read", "input": map[string]any{"file_path": "/tmp/project/config.yaml"}},
		}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "toolu_01", "content": "retries: 3\nbackoff: exponential\ntimeout_ms: 2500"}}},
	})
	out, counts, err := MaskAgentMessages(body, agentSession(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decodeWithNumbers(t, body), decodeWithNumbers(t, out)) {
		t.Fatalf("request content changed although nothing is protected:\n%s\n%s", body, out)
	}
	if counts.Total() != 0 || counts.TokenOverhead != 0 {
		t.Fatalf("counts %+v", counts)
	}
}

// Claude Code re-sends the whole conversation on every call; the provider's prompt cache only
// hits when the earlier part of the request is byte-identical, so placeholders must not drift.
func TestConversationPrefixStaysByteIdentical(t *testing.T) {
	s := agentSession()
	userTurns := []any{
		"Vehicle " + vin + " belongs to " + agentEmail + ".",
		[]any{map[string]any{"type": "tool_result", "tool_use_id": "toolu_01", "content": "ticket " + costGUID + "\naws_key " + costAWSKey}},
		"Compare " + vin + " with ticket " + costGUID + ".",
		"Thanks.",
	}
	var history []any
	var previousPrefix json.RawMessage
	var previous []json.RawMessage
	for turn, content := range userTurns {
		history = append(history, map[string]any{"role": "user", "content": content})
		out, _, err := MaskAgentMessages(claudeCodeRequest(t, history), s, "", nil)
		if err != nil {
			t.Fatalf("turn %d: %v", turn, err)
		}
		for _, original := range []string{vin, agentEmail, costGUID, costAWSKey} {
			if strings.Contains(string(out), original) {
				t.Fatalf("turn %d sent %q upstream", turn, original)
			}
		}
		prefix, messages := maskedParts(t, out)
		if previousPrefix != nil && !bytes.Equal(prefix, previousPrefix) {
			t.Fatalf("turn %d: system prompt or tools changed", turn)
		}
		for i := range previous {
			if !bytes.Equal(previous[i], messages[i]) {
				t.Fatalf("turn %d: message %d changed between calls:\n%s\n%s", turn, i, previous[i], messages[i])
			}
		}
		previousPrefix, previous = prefix, messages
		reply := "Noted " + strings.Join(issuedToken.FindAllString(string(messages[len(messages)-1]), -1), " and ")
		history = append(history, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": s.Restore(reply)}}})
	}
}

// A value learned from its label later in the conversation is masked from then on, while turns
// already sent keep their form, so the provider's prompt cache keeps hitting.
func TestLearnedValueKeepsSentTurnsByteIdentical(t *testing.T) {
	const destination = "api.anthropic.com:443"
	s := agentSession()
	history := []any{map[string]any{"role": "user", "content": "Please ask Kowalczyk about the delivery."}}
	first, _, err := MaskAgentMessages(claudeCodeRequest(t, history), s, destination, nil)
	if err != nil {
		t.Fatal(err)
	}
	history = append(history,
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Sure."}}},
		map[string]any{"role": "user", "content": "lastName: Kowalczyk\nPlease ask Kowalczyk again."},
	)
	second, _, err := MaskAgentMessages(claudeCodeRequest(t, history), s, destination, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, before := maskedParts(t, first)
	_, after := maskedParts(t, second)
	if !bytes.Equal(before[0], after[0]) {
		t.Fatalf("turn already sent changed:\n%s\n%s", before[0], after[0])
	}
	if strings.Contains(string(after[2]), "Kowalczyk") {
		t.Fatalf("learned value sent in clear in the new turn: %s", after[2])
	}
}

// A new rule also protects earlier turns. The provider re-reads the conversation from the first
// changed message once; placeholders issued before the change stay the same.
func TestRuleChangeRewritesEarlierTurnsOnce(t *testing.T) {
	s := agentSession()
	body := claudeCodeRequest(t, []any{map[string]any{"role": "user", "content": "Project ORION ships with " + vin + "."}})
	before, _, err := MaskAgentMessages(body, s, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	policy := s.Policy
	policy.CustomRules = append(policy.CustomRules, mask.RegexRule{Pattern: `ORION`})
	if err := s.SetRules(policy); err != nil {
		t.Fatal(err)
	}
	after, _, err := MaskAgentMessages(body, s, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	again, _, err := MaskAgentMessages(body, s, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before, after) || strings.Contains(string(after), "ORION") {
		t.Fatalf("new rule did not reach the earlier turn: %s", after)
	}
	if !bytes.Equal(after, again) {
		t.Fatal("history keeps changing after the rule change")
	}
	vinToken := issuedToken.FindString(string(before))
	if !strings.Contains(string(after), vinToken) {
		t.Fatalf("placeholder %s was reissued after the rule change", vinToken)
	}
}

func TestPlaceholderSizes(t *testing.T) {
	pem := "-----BEGIN PRIVATE KEY-----\n" + strings.Repeat("MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7\n", 25) + "-----END PRIVATE KEY-----"
	fileLine := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyMaterial build-host"
	samples := []struct{ kind, text, value string }{
		{"VIN", "car " + vin, vin},
		{"EMAIL", "mail " + agentEmail, agentEmail},
		{"GUID", "id " + costGUID, costGUID},
		{"PESEL", "pesel 12345678901", "12345678901"},
		{"SECRET", "key " + costAWSKey, costAWSKey},
		{"SECRET", pem, pem},
		{"FIELD", "firstName: Jan", "Jan"},
		{"FIELD", "address: Example Street 1, 00-001 Example City", "Example Street 1, 00-001 Example City"},
		{"CUSTOM", "codename ORION", "ORION"},
		{"FILE", fileLine, fileLine},
	}
	files := mask.NewFileIndex()
	files.AddFile(files.AddFolder("~/notes", false), fileLine)
	t.Logf("%-7s %10s %12s %10s %12s", "kind", "orig bytes", "placeholder", "orig est.", "placeholder est.")
	for _, sample := range samples {
		policy := mask.DefaultPolicy()
		policy.PESEL = true
		policy.CustomRules = []mask.RegexRule{{Pattern: `ORION`}}
		s := mask.NewSession(100, 1<<20, policy)
		s.SetFiles(files)
		out, counts, err := s.Mask([]string{sample.text})
		if err != nil {
			t.Fatal(err)
		}
		token := issuedToken.FindString(out[0])
		if !strings.HasPrefix(token, mask.Prefix+sample.kind+"_") || strings.Contains(out[0], sample.value) {
			t.Fatalf("%s: %q", sample.kind, out[0])
		}
		if len(token) > 18 {
			t.Fatalf("%s placeholder longer than 18 bytes: %s", sample.kind, token)
		}
		if want := mask.EstimateTokens(token) - mask.EstimateTokens(sample.value); counts.TokenOverhead != want {
			t.Fatalf("%s overhead %d, want %d", sample.kind, counts.TokenOverhead, want)
		}
		t.Logf("%-7s %10d %12d %10d %12d", sample.kind, len(sample.value), len(token), mask.EstimateTokens(sample.value), mask.EstimateTokens(token))
	}
}
