package protocol

import (
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"

	"phantomprompt/internal/mask"
)

func TestMaskedValuesAreToldByWhereTheyCameFrom(t *testing.T) {
	const account, reminder, typed, found = "account@example.test", "git.user@example.test", "typed@example.test", "found@example.test"
	const guid = "123e4567-e89b-12d3-a456-426614174000"
	reminderText := "<system-reminder>git " + reminder + "</system-reminder>"
	prompt := "Mail " + typed + " and " + account
	output := "owner " + found + " id " + guid
	body := func(value any) []byte {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	for _, tc := range []struct {
		name string
		mask func([]byte, *mask.Session, string, []string) ([]byte, mask.Counts, error)
		body []byte
	}{
		{"anthropic messages", MaskAgentMessages, body(map[string]any{
			"system": "Account " + account,
			"messages": []any{
				map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": reminderText}, map[string]any{"type": "text", "text": prompt}}},
				map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{"command": "cat notes"}}}},
				map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": output}}},
			},
		})},
		{"openai chat", MaskOpenAIChat, body(map[string]any{
			"messages": []any{
				map[string]any{"role": "system", "content": "Account " + account},
				map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": reminderText}, map[string]any{"type": "text", "text": prompt}}},
				map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{"id": "c1", "type": "function", "function": map[string]any{"name": "shell", "arguments": `{"cmd":"cat notes"}`}}}},
				map[string]any{"role": "tool", "tool_call_id": "c1", "content": output},
			},
		})},
		{"openai responses", MaskOpenAIResponses, body(map[string]any{
			"instructions": "Account " + account,
			"input": []any{
				map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": reminderText}, map[string]any{"type": "input_text", "text": prompt}}},
				map[string]any{"type": "function_call", "call_id": "c1", "name": "shell", "arguments": `{"cmd":"cat notes"}`},
				map[string]any{"type": "function_call_output", "call_id": "c1", "output": output},
			},
		})},
	} {
		masked, counts, err := tc.mask(tc.body, agentSession(), "api.example.test", nil)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		for _, value := range []string{account, reminder, typed, found, guid} {
			if strings.Contains(string(masked), value) {
				t.Fatalf("%s: %s sent in clear", tc.name, value)
			}
		}
		if !maps.Equal(counts.NewKinds, map[string]int{"email": 4, "guid": 1}) || !maps.Equal(counts.TypedKinds, map[string]int{"email": 2}) || !maps.Equal(counts.ContextKinds, map[string]int{"email": 1}) || counts.NewTokens != nil {
			t.Fatalf("%s: new %v, typed %v, context %v", tc.name, counts.NewKinds, counts.TypedKinds, counts.ContextKinds)
		}
	}
}

func TestValueTypedAgainIsNotCountedTwice(t *testing.T) {
	session := agentSession()
	request := func(text string) []byte {
		data, _ := json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "user", "content": text}}})
		return data
	}
	if _, counts, err := MaskAgentMessages(request("Mail "+agentEmail), session, "api.example.test", nil); err != nil || counts.TypedKinds["email"] != 1 {
		t.Fatalf("first prompt: typed %v, err %v", counts.TypedKinds, err)
	}
	if _, counts, err := MaskAgentMessages(request("Mail "+agentEmail+" again"), session, "api.example.test", nil); err != nil || counts.New != 0 || counts.TypedKinds != nil {
		t.Fatalf("value already protected counted again: new %d, typed %v, err %v", counts.New, counts.TypedKinds, err)
	}
}

func TestImagesAndFilesAreSentUnchecked(t *testing.T) {
	const data = "iVBORw0KGgoTEST0000000000001"
	const titled, inText = "title@example.test", "text@example.test"
	body := func(value any) []byte {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	image := map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": data}}
	for _, tc := range []struct {
		name  string
		mask  func([]byte, *mask.Session, string, []string) ([]byte, mask.Counts, error)
		body  []byte
		sent  int
		audio []byte
	}{
		{"anthropic messages", MaskAgentMessages, body(map[string]any{"messages": []any{
			map[string]any{"role": "user", "content": []any{image}},
			map[string]any{"role": "assistant", "content": "ok"},
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": []any{
				image,
				map[string]any{"type": "document", "title": "Invoice for " + titled, "source": map[string]any{"type": "base64", "media_type": "application/pdf", "data": data}},
				map[string]any{"type": "document", "source": map[string]any{"type": "text", "media_type": "text/plain", "data": "Contact " + inText}},
			}}}},
		}}), 3, body(map[string]any{"messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "future_audio", "source": map[string]any{"type": "base64", "data": data}}}}}})},
		{"openai chat", MaskOpenAIChat, body(map[string]any{"messages": []any{
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + data}}}},
			map[string]any{"role": "assistant", "content": "ok"},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + data}},
				map[string]any{"type": "file", "file": map[string]any{"filename": "report.pdf", "file_data": data}},
				map[string]any{"type": "text", "text": "Contact " + inText},
			}},
		}}), 2, body(map[string]any{"messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": data}}}}}})},
		{"openai responses", MaskOpenAIResponses, body(map[string]any{"input": []any{
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + data}}},
			map[string]any{"type": "function_call", "call_id": "c1", "name": "view_image", "arguments": "{}"},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + data},
				map[string]any{"type": "input_file", "filename": "report.pdf", "file_data": data},
				map[string]any{"type": "input_text", "text": "Contact " + inText},
			}},
		}}), 2, body(map[string]any{"input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": data}}}}}})},
	} {
		masked, counts, err := tc.mask(tc.body, agentSession(), "api.example.test", nil)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if counts.Attachments != tc.sent || !strings.Contains(string(masked), data) || strings.Contains(string(masked), titled) || strings.Contains(string(masked), inText) {
			t.Fatalf("%s: attachments %d, out %s", tc.name, counts.Attachments, masked)
		}
		if _, _, err := tc.mask(tc.audio, agentSession(), "api.example.test", nil); !errors.Is(err, ErrAttachment) {
			t.Fatalf("%s: audio accepted: %v", tc.name, err)
		}
	}
}
