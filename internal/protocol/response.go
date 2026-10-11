package protocol

import (
	"bytes"
	"encoding/json"
	"phantomprompt/internal/mask"
)

func object(data []byte) (map[string]json.RawMessage, error) {
	var value map[string]json.RawMessage
	if json.Unmarshal(data, &value) != nil || value == nil {
		return nil, ErrFormat
	}
	return value, nil
}

func transformText(obj map[string]json.RawMessage, key string, transform func(string) string) error {
	data, ok := obj[key]
	if !ok || bytes.Equal(data, []byte("null")) {
		return nil
	}
	var text string
	if json.Unmarshal(data, &text) != nil {
		return ErrFormat
	}
	out, err := json.Marshal(transform(text))
	if err != nil {
		return err
	}
	obj[key] = out
	return nil
}

func RestoreResponse(kind string, data []byte, session *mask.Session) ([]byte, error) {
	if kind == Demo {
		var response DemoResponse
		if Decode(data, &response) != nil {
			return nil, ErrFormat
		}
		response.Text = session.Restore(response.Text)
		return json.Marshal(response)
	}
	root, err := object(data)
	if err != nil {
		return nil, err
	}
	switch kind {
	case OpenAI:
		var choices []map[string]json.RawMessage
		if json.Unmarshal(root["choices"], &choices) != nil || len(choices) == 0 {
			return nil, ErrFormat
		}
		for _, c := range choices {
			m, err := object(c["message"])
			if err != nil {
				return nil, err
			}
			if _, exists := m["tool_calls"]; exists {
				return nil, ErrFormat
			}
			if _, exists := m["function_call"]; exists {
				return nil, ErrFormat
			}
			for _, key := range []string{"content", "refusal"} {
				if err := transformText(m, key, session.Restore); err != nil {
					return nil, err
				}
			}
			c["message"], err = json.Marshal(m)
			if err != nil {
				return nil, err
			}
		}
		root["choices"], err = json.Marshal(choices)
	case Anthropic:
		var blocks []textBlock
		if Decode(root["content"], &blocks) != nil {
			return nil, ErrFormat
		}
		for i := range blocks {
			if blocks[i].Type != "text" {
				return nil, ErrFormat
			}
			blocks[i].Text = session.Restore(blocks[i].Text)
		}
		root["content"], err = json.Marshal(blocks)
	default:
		return nil, ErrFormat
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(root)
}
