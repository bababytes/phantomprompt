package protocol

import "testing"

func TestTurnDetection(t *testing.T) {
	for _, tc := range []struct {
		name      string
		got, want bool
	}{
		{"agent prompt", AgentNewTurn([]byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)), true},
		{"agent string prompt", AgentNewTurn([]byte(`{"messages":[{"role":"user","content":"hi"}]}`)), true},
		{"agent tool result", AgentNewTurn([]byte(`{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"tool_use"}]},{"role":"user","content":[{"type":"tool_result","content":"x"},{"type":"text","text":"<system-reminder>"}]}]}`)), false},
		{"agent prefill", AgentNewTurn([]byte(`{"messages":[{"role":"assistant","content":"x"}]}`)), false},
		{"chat prompt", OpenAIChatNewTurn([]byte(`{"messages":[{"role":"system","content":"s"},{"role":"user","content":"hi"}]}`)), true},
		{"chat tool", OpenAIChatNewTurn([]byte(`{"messages":[{"role":"user","content":"hi"},{"role":"tool","content":"x"}]}`)), false},
		{"responses string", OpenAIResponsesNewTurn([]byte(`{"input":"hi"}`)), true},
		{"responses message", OpenAIResponsesNewTurn([]byte(`{"input":[{"role":"user","content":"hi"}]}`)), true},
		{"responses tool output", OpenAIResponsesNewTurn([]byte(`{"input":[{"role":"user","content":"hi"},{"type":"function_call_output","output":"x"}]}`)), false},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

func TestAgentRefusedToolsAreTheErroredCallsOfTheLastMessage(t *testing.T) {
	data := []byte(`{"messages":[
		{"role":"user","content":"look at my keys"},
		{"role":"assistant","content":[{"type":"text","text":"ok"},{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/home/me/.ssh/id_ed25519"}},{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"ls","options":["-la","~/.ssh"]}},{"type":"tool_use","id":"t3","name":"Read","input":{"file_path":"/home/me/README.md"}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"denied"},{"type":"tool_result","tool_use_id":"t2","is_error":true,"content":[{"type":"text","text":"denied"}]},{"type":"tool_result","tool_use_id":"t3","content":"# readme"}]}]}`)
	refused := AgentRefusedTools(data)
	if len(refused) != 2 || len(refused["t1"]) != 1 || refused["t1"][0] != "/home/me/.ssh/id_ed25519" || len(refused["t2"]) != 3 {
		t.Fatalf("refused %q", refused)
	}
	for _, other := range []string{
		`{"messages":[{"role":"user","content":"hi"}]}`,
		`{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t1","input":{"path":"~/.ssh"}}]},{"role":"user","content":"next question"}]}`,
		`not json`,
	} {
		if refused := AgentRefusedTools([]byte(other)); len(refused) != 0 {
			t.Fatalf("%s: %q", other, refused)
		}
	}
}
