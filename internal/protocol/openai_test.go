package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"phantomprompt/internal/mask"
)

func maskedToken(t *testing.T, s *mask.Session, value string) string {
	t.Helper()
	out, _, err := s.Mask([]string{value})
	if err != nil || out[0] == value {
		t.Fatalf("value not masked: %v", err)
	}
	return out[0]
}

func TestMaskOpenAIChat(t *testing.T) {
	s := agentSession()
	body := []byte(`{"model":"gpt-4o-mini","snippy":{"enabled":true},"stream":true,"messages":[
		{"role":"system","content":"Owner ` + agentEmail + `"},
		{"role":"user","content":[{"type":"text","text":"Check ` + vin + `"}]},
		{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"lookup","arguments":"{\"vin\":\"` + vin + `\",\"limit\":2}"}}]},
		{"role":"tool","tool_call_id":"c1","content":"found ` + vin + `"}
	]}`)
	out, counts, err := MaskOpenAIChat(body, s, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), vin) || strings.Contains(string(out), agentEmail) || counts.VIN != 3 || counts.Email != 1 {
		t.Fatalf("counts %+v out %s", counts, out)
	}
	var root struct {
		Snippy   map[string]any `json:"snippy"`
		Messages []struct {
			ToolCalls []struct {
				Function struct{ Arguments string } `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &root); err != nil || root.Snippy["enabled"] != true {
		t.Fatalf("unknown fields not preserved: %s", out)
	}
	var arguments map[string]any
	if err := json.Unmarshal([]byte(root.Messages[2].ToolCalls[0].Function.Arguments), &arguments); err != nil || !strings.HasPrefix(arguments["vin"].(string), mask.Prefix+"VIN_") || arguments["limit"] != float64(2) {
		t.Fatalf("tool arguments %v", root.Messages[2].ToolCalls[0].Function.Arguments)
	}
	for _, bad := range []struct {
		body string
		want error
	}{
		{`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"x"}}]}]}`, ErrAttachment},
		{`{"messages":[{"role":"user","content":[{"type":"Bad Type","text":"x"}]}]}`, ErrFormat},
		{`{"messages":[{"role":5,"content":"x"}]}`, ErrFormat},
		{`{"input":"x"}`, ErrFormat},
	} {
		if _, _, err := MaskOpenAIChat([]byte(bad.body), agentSession(), "", nil); !errors.Is(err, bad.want) {
			t.Fatalf("%s: %v", bad.body, err)
		}
	}
}

func TestMaskOpenAIResponses(t *testing.T) {
	s := agentSession()
	body := []byte(`{"model":"gpt-5.6-luna-utility","store":false,"instructions":"Workspace of ` + agentEmail + `","input":[
		{"role":"user","content":[{"type":"input_text","text":"Summarise ` + vin + `"}]},
		{"type":"reasoning","encrypted_content":"opaque ` + vin + `","summary":[]},
		{"type":"function_call","call_id":"c1","name":"read","arguments":"{\"path\":\"notes ` + vin + `\"}"},
		{"type":"function_call_output","call_id":"c1","output":"contents ` + agentEmail + `"},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Earlier ` + vin + `","annotations":[]}]}
	]}`)
	out, counts, err := MaskOpenAIResponses(body, s, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if counts.VIN != 3 || counts.Email != 2 || !strings.Contains(string(out), `"opaque `+vin+`"`) || strings.Count(string(out), vin) != 1 || strings.Contains(string(out), agentEmail) {
		t.Fatalf("counts %+v out %s", counts, out)
	}
	if out, _, err := MaskOpenAIResponses([]byte(`{"input":"plain `+vin+`"}`), s, "", nil); err != nil || strings.Contains(string(out), vin) {
		t.Fatalf("string input: %s %v", out, err)
	}
	for _, bad := range []string{`{"input":[{"type":"Web Search"}]}`, `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"x"}]}]}`, `{"model":"x"}`} {
		if _, _, err := MaskOpenAIResponses([]byte(bad), agentSession(), "", nil); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestRestoreOpenAIResponseBodies(t *testing.T) {
	s := agentSession()
	quoted := `say "hi" \ ok`
	policy := mask.DefaultPolicy()
	policy.CustomFields = []string{"note"}
	s.SetRules(policy)
	field, _, _ := s.Mask([]string{"note: " + quoted})
	token := strings.TrimPrefix(field[0], "note: ")
	vinToken := maskedToken(t, s, vin)
	chat := `{"choices":[{"index":0,"message":{"role":"assistant","content":"VIN ` + vinToken + `","tool_calls":[{"function":{"name":"save","arguments":"{\"note\":\"` + token + `\"}"}}]}}],"usage":{"prompt_tokens":321}}`
	out, _, err := RestoreOpenAI([]byte(chat), s)
	var decoded struct {
		Choices []struct {
			Message struct {
				Content   string
				ToolCalls []struct {
					Function struct{ Arguments string } `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err != nil || json.Unmarshal(out, &decoded) != nil || decoded.Choices[0].Message.Content != "VIN "+vin {
		t.Fatalf("restored %s %v", out, err)
	}
	var arguments map[string]string
	if err := json.Unmarshal([]byte(decoded.Choices[0].Message.ToolCalls[0].Function.Arguments), &arguments); err != nil || arguments["note"] != quoted {
		t.Fatalf("arguments %q %v", decoded.Choices[0].Message.ToolCalls[0].Function.Arguments, err)
	}
	if OpenAIInputTokens([]byte(chat)) != 321 || OpenAIInputTokens([]byte(`{"usage":{"input_tokens":77}}`)) != 77 {
		t.Fatal("usage not read")
	}
}

func TestRestoreOpenAIChatStream(t *testing.T) {
	s := agentSession()
	token := maskedToken(t, s, vin)
	chunk := func(delta string, finish string) string {
		data := map[string]any{"id": "c", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": json.RawMessage(delta), "finish_reason": nil}}}
		if finish != "" {
			data["choices"].([]any)[0].(map[string]any)["finish_reason"] = finish
		}
		encoded, _ := json.Marshal(data)
		return "data: " + string(encoded) + "\n\n"
	}
	text, _ := json.Marshal("VIN " + token[:5])
	tail, _ := json.Marshal(token[5:] + " ok")
	args1, _ := json.Marshal(`{"vin":"` + token[:3])
	args2, _ := json.Marshal(token[3:] + `"}`)
	input := chunk(`{"content":`+string(text)+`}`, "") +
		chunk(`{"content":`+string(tail)+`}`, "") +
		chunk(`{"tool_calls":[{"index":0,"function":{"name":"save","arguments":`+string(args1)+`}}]}`, "") +
		chunk(`{"tool_calls":[{"index":0,"function":{"arguments":`+string(args2)+`}}]}`, "tool_calls") +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":900}}\n\n" +
		"data: [DONE]\n\n"
	var out bytes.Buffer
	stats, err := RestoreOpenAIChatStream(strings.NewReader(input), &out, s, 1<<20)
	if err != nil || stats.InputTokens != 900 || !strings.HasSuffix(out.String(), "data: [DONE]\n\n") {
		t.Fatalf("stats %+v err %v out %s", stats, err, out.String())
	}
	var content, arguments strings.Builder
	for _, line := range strings.Split(out.String(), "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		var value struct {
			Choices []struct {
				Delta struct {
					Content   string
					ToolCalls []struct {
						Function struct{ Arguments string } `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		json.Unmarshal([]byte(data), &value)
		for _, choice := range value.Choices {
			content.WriteString(choice.Delta.Content)
			for _, call := range choice.Delta.ToolCalls {
				arguments.WriteString(call.Function.Arguments)
			}
		}
	}
	if content.String() != "VIN "+vin+" ok" || arguments.String() != `{"vin":"`+vin+`"}` {
		t.Fatalf("content %q arguments %q", content.String(), arguments.String())
	}
}

func TestRestoreOpenAIResponsesStream(t *testing.T) {
	s := agentSession()
	token := maskedToken(t, s, vin)
	event := func(name string, data map[string]any) string {
		data["type"] = name
		encoded, _ := json.Marshal(data)
		return "event: " + name + "\ndata: " + string(encoded) + "\n\n"
	}
	input := event("response.created", map[string]any{"sequence_number": 0, "response": map[string]any{"instructions": "for " + token}}) +
		event("response.output_text.delta", map[string]any{"sequence_number": 1, "item_id": "m1", "output_index": 0, "content_index": 0, "delta": "VIN " + token[:6]}) +
		event("response.output_text.delta", map[string]any{"sequence_number": 2, "item_id": "m1", "output_index": 0, "content_index": 0, "delta": token[6 : len(token)-1]}) +
		event("response.output_text.done", map[string]any{"sequence_number": 3, "item_id": "m1", "output_index": 0, "content_index": 0, "text": "VIN " + token[:len(token)-1]}) +
		event("response.completed", map[string]any{"sequence_number": 4, "response": map[string]any{"output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "VIN " + token}}}}, "usage": map[string]any{"input_tokens": 1234}}})
	var out bytes.Buffer
	stats, err := RestoreOpenAIResponsesStream(strings.NewReader(input), &out, s, 1<<20)
	if err != nil || stats.InputTokens != 1234 {
		t.Fatalf("stats %+v err %v", stats, err)
	}
	var deltas strings.Builder
	sequence := 0
	for _, line := range strings.Split(out.String(), "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var value struct {
			Type     string
			Delta    string
			Sequence int `json:"sequence_number"`
		}
		json.Unmarshal([]byte(data), &value)
		if value.Sequence != sequence {
			t.Fatalf("sequence %d, want %d: %s", value.Sequence, sequence, out.String())
		}
		sequence++
		deltas.WriteString(value.Delta)
	}
	if deltas.String() != "VIN "+token[:len(token)-1] || !strings.Contains(out.String(), `"instructions":"for `+vin+`"`) || !strings.Contains(out.String(), `"text":"VIN `+vin+`"`) {
		t.Fatalf("deltas %q out %s", deltas.String(), out.String())
	}
}

func TestOpenAIUnknownTypesAreMaskedGenerically(t *testing.T) {
	chat := []byte(`{"messages":[{"role":"critic","content":[{"type":"mystery","text":"car ` + vin + `"}]}]}`)
	out, counts, err := MaskOpenAIChat(chat, agentSession(), "", nil)
	if err != nil || strings.Contains(string(out), vin) || counts.UnknownBlocks["mystery"] != 1 {
		t.Fatalf("chat: %s %+v %v", out, counts, err)
	}
	responses := []byte(`{"input":[{"type":"local_shell_call","call_id":"c1","action":{"type":"exec","command":["cat","notes ` + vin + `"]}},{"type":"web_search_call","id":"ws_1","action":{"query":"owner ` + agentEmail + `"}},{"type":"future_item","note":"car ` + vin + `"},{"type":"compaction","encrypted_content":"opaque ` + vin + `"},{"type":"additional_tools","tools":[{"name":"x","description":"mail ` + agentEmail + `"}]}]}`)
	out, counts, err = MaskOpenAIResponses(responses, agentSession(), "", nil)
	if err != nil || strings.Contains(string(out), "notes "+vin) || strings.Contains(string(out), "owner "+agentEmail) || strings.Contains(string(out), "car "+vin) || !strings.Contains(string(out), `"call_id":"c1"`) {
		t.Fatalf("responses: %s %+v %v", out, counts, err)
	}
	if !strings.Contains(string(out), `"opaque `+vin+`"`) || !strings.Contains(string(out), "mail "+agentEmail) {
		t.Fatalf("encrypted content or tool definitions were changed: %s", out)
	}
	if len(counts.UnknownBlocks) != 1 || counts.UnknownBlocks["future_item"] != 1 {
		t.Fatalf("only truly new item types should raise an alert: %+v", counts.UnknownBlocks)
	}
}

func TestOpenAISecretsAreRestoredOnlyInFileEdits(t *testing.T) {
	s := agentSession()
	masked, _, err := s.Mask([]string{"AKIAIOSFODNN7EXAMPLE"})
	if err != nil {
		t.Fatal(err)
	}
	secret := masked[0]
	arguments := func(text string) string {
		data, _ := json.Marshal(map[string]string{"command": text})
		return mustQuote(string(data))
	}
	body := []byte(`{"output":[{"type":"function_call","name":"shell","arguments":` + arguments("curl -d "+secret) + `},{"type":"custom_tool_call","name":"apply_patch","input":"+DB_PASSWORD=` + secret + `"}]}`)
	restored, kept, err := RestoreOpenAI(body, s)
	if len(kept) != 1 {
		t.Fatalf("withheld placeholders not reported as kept: %v", kept)
	}
	if err != nil || !strings.Contains(string(restored), "curl -d "+secret) || !strings.Contains(string(restored), "DB_PASSWORD=AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("non-stream: %s %v", restored, err)
	}
	stream := sse(
		[2]string{"response.output_item.added", `{"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","name":"shell"}}`},
		[2]string{"response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":` + arguments("curl -d "+secret) + `}`},
		[2]string{"response.function_call_arguments.done", `{"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":0,"arguments":` + arguments("curl -d "+secret) + `}`},
		[2]string{"response.output_item.added", `{"type":"response.output_item.added","item":{"type":"custom_tool_call","id":"ct_1","name":"apply_patch"}}`},
		[2]string{"response.custom_tool_call_input.delta", `{"type":"response.custom_tool_call_input.delta","item_id":"ct_1","output_index":1,"delta":"+DB_PASSWORD=` + secret + `"}`},
		[2]string{"response.custom_tool_call_input.done", `{"type":"response.custom_tool_call_input.done","item_id":"ct_1","output_index":1,"input":"+DB_PASSWORD=` + secret + `"}`},
		[2]string{"response.completed", `{"type":"response.completed","response":{}}`},
	)
	var out bytes.Buffer
	stats, err := RestoreOpenAIResponsesStream(strings.NewReader(stream), &out, s, 1<<20)
	if err != nil || strings.Contains(out.String(), "curl -d AKIAIOSFODNN7EXAMPLE") || !strings.Contains(out.String(), "DB_PASSWORD=AKIAIOSFODNN7EXAMPLE") || stats.Withheld < 2 {
		t.Fatalf("stream withheld %d: %s %v", stats.Withheld, out.String(), err)
	}
	chat := sse([2]string{"", `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"run_terminal","arguments":` + arguments("echo "+secret) + `}}]},"finish_reason":"tool_calls"}]}`}, [2]string{"", "[DONE]"})
	out.Reset()
	if _, err := RestoreOpenAIChatStream(strings.NewReader(chat), &out, s, 1<<20); err != nil || strings.Contains(out.String(), "echo AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("chat stream: %s %v", out.String(), err)
	}
}
