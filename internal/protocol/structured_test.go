package protocol

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"phantomprompt/internal/mask"
)

var (
	structuredFirstName = "Ad" + "elajda"
	structuredCard      = "4111" + "222233334444"
	structuredPassword  = "Q7xk2" + "LmP9zw"
	structuredEmail     = "jan.nowak" + "@" + "example.com"
	structuredMobile    = "600" + "100200"
	structuredLastName  = "Kowal" + "czykowski"
	structuredRaw       = []string{structuredFirstName, structuredCard, structuredPassword, structuredEmail, structuredMobile, structuredLastName}
)

func structuredInput() map[string]any {
	return map[string]any{
		"firstName":  structuredFirstName,
		"cardNumber": json.Number(structuredCard),
		"password":   structuredPassword,
		"contacts":   map[string]any{structuredEmail: map[string]any{"mobile": []any{structuredMobile}}},
		"payload":    `{"lastName":"` + structuredLastName + `"}`,
		"count":      json.Number("3"),
		"enabled":    true,
	}
}

func structuredSession() *mask.Session {
	policy := mask.DefaultPolicy()
	policy.LenientFields = true
	return mask.NewSession(1000, 1<<20, policy)
}

func assertNoRawValues(t *testing.T, name string, data []byte) {
	t.Helper()
	for i, raw := range structuredRaw {
		if bytes.Contains(data, []byte(raw)) {
			t.Fatalf("%s: synthetic value %d reached the upstream", name, i)
		}
	}
}

func TestStructuredToolInputIsMaskedWithItsFieldNames(t *testing.T) {
	arguments, _ := encodeJSON(structuredInput())
	for name, request := range map[string]struct {
		body string
		mask func([]byte, *mask.Session) ([]byte, mask.Counts, error)
		ids  []string
	}{
		"anthropic tool_use": {structuredJSON(map[string]any{"model": "m", "messages": []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "toolu_77", "name": "save_customer", "input": structuredInput()}}}}}),
			func(d []byte, s *mask.Session) ([]byte, mask.Counts, error) { return MaskAgentMessages(d, s, "", nil) }, []string{`"id":"toolu_77"`, `"name":"save_customer"`}},
		"anthropic unknown block": {structuredJSON(map[string]any{"model": "m", "messages": []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "future_block", "id": "fb_1", "name": "save_customer", "data": structuredInput()}}}}}),
			func(d []byte, s *mask.Session) ([]byte, mask.Counts, error) { return MaskAgentMessages(d, s, "", nil) }, []string{`"id":"fb_1"`, `"name":"save_customer"`}},
		"MCP tools/call": {structuredJSON(map[string]any{"jsonrpc": "2.0", "id": 9, "method": "tools/call", "params": map[string]any{"name": "save_customer", "arguments": structuredInput()}}),
			MaskToolArguments, []string{`"name":"save_customer"`, `"method":"tools/call"`}},
		"openai chat": {structuredJSON(map[string]any{"model": "m", "messages": []any{map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": "call_5", "type": "function", "function": map[string]any{"name": "save_customer", "arguments": string(arguments)}}}}}}),
			func(d []byte, s *mask.Session) ([]byte, mask.Counts, error) { return MaskOpenAIChat(d, s, "", nil) }, []string{`"id":"call_5"`, `"name":"save_customer"`}},
		"openai responses": {structuredJSON(map[string]any{"model": "m", "input": []any{map[string]any{"type": "function_call", "call_id": "call_6", "name": "save_customer", "arguments": string(arguments)}}}),
			func(d []byte, s *mask.Session) ([]byte, mask.Counts, error) {
				return MaskOpenAIResponses(d, s, "", nil)
			}, []string{`"call_id":"call_6"`, `"name":"save_customer"`}},
	} {
		masked, counts, err := request.mask([]byte(request.body), structuredSession())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		assertNoRawValues(t, name, masked)
		for _, id := range request.ids {
			if !bytes.Contains(masked, []byte(id)) {
				t.Fatalf("%s: protocol identifier %s changed", name, id)
			}
		}
		if counts.Secret < 1 || counts.Fields < 3 {
			t.Fatalf("%s: secrets %d fields %d", name, counts.Secret, counts.Fields)
		}
	}
}

func TestStructuredToolInputRoundTrips(t *testing.T) {
	s := structuredSession()
	body := structuredJSON(map[string]any{"model": "m", "messages": []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "toolu_77", "name": "Write", "input": structuredInput()}}}}})
	masked, _, err := MaskAgentMessages([]byte(body), s, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var request struct {
		Messages []struct {
			Content []struct {
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(masked, &request); err != nil {
		t.Fatal(err)
	}
	maskedInput := request.Messages[0].Content[0].Input
	want, _ := encodeJSON(structuredInput())

	reply := structuredJSON(map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": "toolu_78", "name": "Write", "input": json.RawMessage(maskedInput)}}})
	restored, _, err := RestoreAgentMessages([]byte(reply), s)
	if err != nil {
		t.Fatal(err)
	}
	var message struct {
		Content []struct {
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	json.Unmarshal(restored, &message)
	if !sameJSON(message.Content[0].Input, want) {
		t.Fatalf("message restore differs:\n got %s\nwant %s", message.Content[0].Input, want)
	}

	stream := sse(
		[2]string{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_79","name":"Write","input":{}}}`},
		[2]string{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":` + mustQuote(string(maskedInput)) + `}}`},
		[2]string{"content_block_stop", `{"type":"content_block_stop","index":0}`},
		[2]string{"message_stop", `{"type":"message_stop"}`},
	)
	var out bytes.Buffer
	if _, err := RestoreAgentStream(strings.NewReader(stream), &out, s, 1<<20); err != nil {
		t.Fatal(err)
	}
	var streamed strings.Builder
	for _, line := range strings.Split(out.String(), "\n") {
		var event struct {
			Delta struct {
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if data, ok := strings.CutPrefix(line, "data: "); ok && json.Unmarshal([]byte(data), &event) == nil {
			streamed.WriteString(event.Delta.PartialJSON)
		}
	}
	if !sameJSON([]byte(streamed.String()), want) {
		t.Fatalf("stream restore differs:\n got %s\nwant %s", streamed.String(), want)
	}
}

func structuredJSON(value any) string {
	data, err := encodeJSON(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func sameJSON(a, b []byte) bool {
	x, errA := decodeExact(string(a))
	y, errB := decodeExact(string(b))
	if errA != nil || errB != nil {
		return false
	}
	left, _ := encodeJSON(x)
	right, _ := encodeJSON(y)
	return bytes.Equal(left, right)
}
