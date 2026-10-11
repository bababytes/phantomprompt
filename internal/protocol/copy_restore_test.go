package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"phantomprompt/internal/mask"
)

func syntheticFile() string {
	card := "4111" + "111111111111"
	return "{\r\n" +
		`  "cardNumber": ` + card + ",\r\n" +
		`  "address": {"street": "Main 1", "zip": "00-950"},` + "\r\n" +
		`  "phone": null,` + "\r\n" +
		`  "accountNumber": [1, 2, 3],` + "\r\n" +
		`  "zipCode": true,` + "\r\n" +
		`  "note": "a \"quoted\" \\ back\/slash \u0040 zażółć \t end",` + "\r\n" +
		`  "fullName": "Ada \"The\" Lovelace"` + "\r\n" +
		"}\r\n  "
}

func copiedFile(t *testing.T, name, input string) string {
	t.Helper()
	var decoded struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(input), &decoded); err != nil {
		t.Fatalf("%s: tool input is not JSON: %v", name, err)
	}
	return decoded.Content
}

func TestACopiedFileIsRestoredExactly(t *testing.T) {
	s := agentSession()
	file := syntheticFile()
	masked, _, err := s.Mask([]string{file})
	if err != nil {
		t.Fatal(err)
	}
	if typed := strings.Count(masked[0], `"`+mask.Prefix+"FIELD_"); typed < 5 {
		t.Fatalf("expected typed placeholders for the number, object, null, array and boolean fields, got %d", typed)
	}
	if strings.Contains(masked[0], "4111") || strings.Contains(masked[0], "Lovelace") {
		t.Fatal("the file was not masked")
	}
	arguments, err := encodeJSON(map[string]any{"file_path": "/tmp/copy.json", "content": masked[0]})
	if err != nil {
		t.Fatal(err)
	}
	expect := func(name, input string) {
		t.Helper()
		if got := copiedFile(t, name, input); got != file {
			t.Fatalf("%s: file differs\n got %q\nwant %q", name, got, file)
		}
	}

	anthropic := func(chunks []string) string {
		events := [][2]string{{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"Write","input":{}}}`}}
		for _, chunk := range chunks {
			events = append(events, [2]string{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":` + mustQuote(chunk) + `}}`})
		}
		events = append(events, [2]string{"content_block_stop", `{"type":"content_block_stop","index":0}`}, [2]string{"message_stop", `{"type":"message_stop"}`})
		var out bytes.Buffer
		if _, err := RestoreAgentStream(strings.NewReader(sse(events...)), &out, s, 1<<20); err != nil {
			t.Fatal(err)
		}
		var input strings.Builder
		for _, line := range strings.Split(out.String(), "\n") {
			var event struct {
				Delta struct {
					Type        string `json:"type"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok && json.Unmarshal([]byte(data), &event) == nil && event.Delta.Type == "input_json_delta" {
				input.WriteString(event.Delta.PartialJSON)
			}
		}
		return input.String()
	}
	chat := func(chunks []string) string {
		var events [][2]string
		for i, chunk := range chunks {
			function := map[string]any{"arguments": chunk}
			if i == 0 {
				function["name"] = "Write"
			}
			data, _ := encodeJSON(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": function}}}}}})
			events = append(events, [2]string{"", string(data)})
		}
		events = append(events, [2]string{"", `{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`}, [2]string{"", "[DONE]"})
		var out bytes.Buffer
		if _, err := RestoreOpenAIChatStream(strings.NewReader(sse(events...)), &out, s, 1<<20); err != nil {
			t.Fatal(err)
		}
		var input strings.Builder
		for _, line := range strings.Split(out.String(), "\n") {
			var event struct {
				Choices []struct {
					Delta struct {
						ToolCalls []struct {
							Function struct {
								Arguments string `json:"arguments"`
							} `json:"function"`
						} `json:"tool_calls"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok && json.Unmarshal([]byte(data), &event) == nil {
				for _, choice := range event.Choices {
					for _, call := range choice.Delta.ToolCalls {
						input.WriteString(call.Function.Arguments)
					}
				}
			}
		}
		return input.String()
	}
	responses := func(chunks []string) string {
		events := [][2]string{{"response.output_item.added", `{"type":"response.output_item.added","sequence_number":0,"output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"Write","arguments":""}}`}}
		for _, chunk := range chunks {
			events = append(events, [2]string{"response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","sequence_number":1,"item_id":"fc_1","output_index":0,"delta":` + mustQuote(chunk) + `}`})
		}
		events = append(events, [2]string{"response.function_call_arguments.done", `{"type":"response.function_call_arguments.done","sequence_number":2,"item_id":"fc_1","output_index":0,"arguments":` + mustQuote(strings.Join(chunks, "")) + `}`},
			[2]string{"response.completed", `{"type":"response.completed","sequence_number":3,"response":{}}`})
		var out bytes.Buffer
		if _, err := RestoreOpenAIResponsesStream(strings.NewReader(sse(events...)), &out, s, 1<<20); err != nil {
			t.Fatal(err)
		}
		var deltas strings.Builder
		done := ""
		for _, line := range strings.Split(out.String(), "\n") {
			var event struct {
				Type      string `json:"type"`
				Delta     string `json:"delta"`
				Arguments string `json:"arguments"`
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok && json.Unmarshal([]byte(data), &event) == nil {
				switch event.Type {
				case "response.function_call_arguments.delta":
					deltas.WriteString(event.Delta)
				case "response.function_call_arguments.done":
					done = event.Arguments
				}
			}
		}
		if deltas.String() != done {
			t.Fatalf("responses: streamed arguments differ from the final ones")
		}
		return done
	}
	text := string(arguments)
	for cut := 0; cut <= len(text); cut++ {
		if cut < len(text) && !utf8.RuneStart(text[cut]) {
			continue
		}
		chunks := []string{text[:cut], text[cut:]}
		expect(fmt.Sprintf("Anthropic stream cut at %d", cut), anthropic(chunks))
		expect(fmt.Sprintf("OpenAI chat stream cut at %d", cut), chat(chunks))
		expect(fmt.Sprintf("OpenAI responses stream cut at %d", cut), responses(chunks))
	}
	var runes []string
	for _, r := range text {
		runes = append(runes, string(r))
	}
	expect("Anthropic stream in one-character deltas", anthropic(runes))
	expect("OpenAI chat stream in one-character deltas", chat(runes))
	expect("OpenAI responses stream in one-character deltas", responses(runes))

	var input any
	if err := json.Unmarshal(arguments, &input); err != nil {
		t.Fatal(err)
	}
	body, _ := encodeJSON(map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": "toolu_1", "name": "Write", "input": input}}})
	restored, _, err := RestoreAgentMessages(body, s)
	if err != nil {
		t.Fatal(err)
	}
	var message struct {
		Content []struct {
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(restored, &message); err != nil || len(message.Content) != 1 {
		t.Fatalf("restored message %s: %v", restored, err)
	}
	expect("Anthropic message", string(message.Content[0].Input))

	body, _ = encodeJSON(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"tool_calls": []any{map[string]any{"function": map[string]any{"name": "Write", "arguments": text}}}}}}})
	restored, _, err = RestoreOpenAI(body, s)
	if err != nil {
		t.Fatal(err)
	}
	var completion struct {
		Choices []struct {
			Message struct {
				ToolCalls []struct {
					Function struct {
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(restored, &completion); err != nil {
		t.Fatal(err)
	}
	expect("OpenAI completion", completion.Choices[0].Message.ToolCalls[0].Function.Arguments)
}

func TestATypedPlaceholderStandingAloneGetsItsType(t *testing.T) {
	s := agentSession()
	masked, _, err := s.Mask([]string{`{"cardNumber": ` + "4111" + "111111111111" + `, "address": {"street": "Main 1"}}`})
	if err != nil {
		t.Fatal(err)
	}
	tokens := issuedToken.FindAllString(masked[0], -1)
	if len(tokens) != 2 {
		t.Fatalf("tokens %v", tokens)
	}
	body, _ := encodeJSON(map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": "toolu_1", "name": "Write", "input": map[string]any{"card": tokens[0], "address": tokens[1], "note": "card " + tokens[0]}}}})
	restored, _, err := RestoreAgentMessages(body, s)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"input":{"address":{"street":"Main 1"},"card":` + "4111" + "111111111111" + `,"note":"card ` + "4111" + "111111111111" + `"}`; !strings.Contains(string(restored), want) {
		t.Fatalf("restored %s", restored)
	}
}
