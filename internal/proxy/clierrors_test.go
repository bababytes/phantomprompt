package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestCLIFailureSpeaksTheClientAPI(t *testing.T) {
	for _, tc := range []struct {
		path, reason, retry string
		code                int
		openAI              bool
	}{
		{"/v1/messages", "unsupported_content", "false", 422, false},
		{"/v1/messages", "session_expired_restart_cli", "false", 409, false},
		{"/v1/messages", "concurrency_limit", "true", 503, false},
		{"/chat/completions", "attachments_not_supported", "false", 422, true},
	} {
		request := &http.Request{URL: &url.URL{Path: tc.path}}
		response := cliFailure(request, tc.code, tc.reason)
		data, _ := io.ReadAll(response.Body)
		var body struct {
			Type  string `json:"type"`
			Error struct {
				Type, Message, Code string
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != tc.code || response.Header.Get("X-Should-Retry") != tc.retry || !strings.Contains(body.Error.Message, "PhantomPrompt:") || !strings.Contains(body.Error.Message, tc.reason) {
			t.Fatalf("%s %s: %d %v %s", tc.path, tc.reason, response.StatusCode, response.Header, data)
		}
		if tc.openAI != (body.Type == "") || tc.openAI && body.Error.Code != tc.reason {
			t.Fatalf("%s: wrong envelope %s", tc.path, data)
		}
	}
}

func TestMainTokenCLIRequestsShareOneMap(t *testing.T) {
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	mask := func() string {
		session, release, err := app.cliSession("")
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		out, _, err := session.Mask([]string{"owner test.user@example.test"})
		if err != nil {
			t.Fatal(err)
		}
		return out[0]
	}
	if first, second := mask(), mask(); first != second {
		t.Fatalf("same value, two turns, different placeholders: %q vs %q", first, second)
	}
}

func TestDiscoverySkipsUserDataValues(t *testing.T) {
	request := map[string]any{"model": "m", "messages": []any{map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "tool_use", "input": map[string]any{"type": "TEST0000000000001", "status": "secret-status"}},
	}}}}
	shape, _ := json.Marshal(skeleton(request, "", "", 0))
	if strings.Contains(string(shape), "TEST0000000000001") || strings.Contains(string(shape), "secret-status") || !strings.Contains(string(shape), "=tool_use") || !strings.Contains(string(shape), "=user") {
		t.Fatalf("discovery shape: %s", shape)
	}
}

func TestDiscoverySamplesResponsesPerPath(t *testing.T) {
	var s shapes
	for i := range responsesPerPath {
		if !s.sample("/v1/messages") {
			t.Fatalf("response %d not sampled", i)
		}
	}
	if s.sample("/v1/messages") || !s.sample("/v1/other") {
		t.Fatal("sampling is not bounded per path")
	}
}
