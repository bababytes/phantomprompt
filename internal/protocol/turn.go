package protocol

import "encoding/json"

type typedBlock struct {
	Type string `json:"type"`
	Role string `json:"role"`
}

func AgentNewTurn(data []byte) bool {
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(data, &req) != nil || len(req.Messages) == 0 {
		return false
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != "user" {
		return false
	}
	var blocks []typedBlock
	if json.Unmarshal(last.Content, &blocks) != nil {
		return true
	}
	for _, block := range blocks {
		if block.Type == "tool_result" {
			return false
		}
	}
	return true
}

func OpenAIChatNewTurn(data []byte) bool {
	var req struct {
		Messages []typedBlock `json:"messages"`
	}
	return json.Unmarshal(data, &req) == nil && len(req.Messages) > 0 && req.Messages[len(req.Messages)-1].Role == "user"
}

func OpenAIResponsesNewTurn(data []byte) bool {
	var req struct {
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(data, &req) != nil {
		return false
	}
	var text string
	if json.Unmarshal(req.Input, &text) == nil {
		return true
	}
	var items []typedBlock
	if json.Unmarshal(req.Input, &items) != nil || len(items) == 0 {
		return false
	}
	last := items[len(items)-1]
	return last.Role == "user" && (last.Type == "" || last.Type == "message")
}

func AgentRefusedTools(data []byte) map[string][]string {
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(data, &req) != nil || len(req.Messages) < 2 {
		return nil
	}
	last, previous := req.Messages[len(req.Messages)-1], req.Messages[len(req.Messages)-2]
	var results []struct {
		Type      string `json:"type"`
		ToolUseID string `json:"tool_use_id"`
		IsError   bool   `json:"is_error"`
	}
	var calls []struct {
		Type  string `json:"type"`
		ID    string `json:"id"`
		Input any    `json:"input"`
	}
	if last.Role != "user" || previous.Role != "assistant" || json.Unmarshal(last.Content, &results) != nil || json.Unmarshal(previous.Content, &calls) != nil {
		return nil
	}
	refused := make(map[string]bool)
	for _, result := range results {
		if result.Type == "tool_result" && result.IsError && result.ToolUseID != "" {
			refused[result.ToolUseID] = true
		}
	}
	inputs := make(map[string][]string)
	for _, call := range calls {
		if call.Type == "tool_use" && refused[call.ID] {
			inputs[call.ID] = stringLeaves(call.Input, nil)
		}
	}
	return inputs
}

func stringLeaves(value any, into []string) []string {
	switch v := value.(type) {
	case string:
		into = append(into, v)
	case map[string]any:
		for _, item := range v {
			into = stringLeaves(item, into)
		}
	case []any:
		for _, item := range v {
			into = stringLeaves(item, into)
		}
	}
	return into
}
