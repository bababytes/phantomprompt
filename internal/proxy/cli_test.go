package proxy

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"phantomprompt/internal/mask"
	"phantomprompt/internal/protocol"
)

type copilotUpstream struct {
	mu       sync.Mutex
	prompts  []string
	mcp      []string
	openai   []string
	model    atomic.Int64
	analytic atomic.Int64
}

func (u *copilotUpstream) openAI(w http.ResponseWriter, path string, body []byte) {
	var req struct {
		Input    string `json:"input"`
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &req) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	prompt := req.Input
	if len(req.Messages) > 0 {
		prompt = req.Messages[len(req.Messages)-1].Content
	}
	u.mu.Lock()
	u.openai = append(u.openai, string(body))
	u.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(name string, value any) {
		data, _ := json.Marshal(value)
		protocol.WriteEvent(w, protocol.Event{Name: name, Data: string(data)})
		w.(http.Flusher).Flush()
	}
	reply := "Echo: " + prompt
	for i := 0; i < len(reply); i += 4 {
		piece := reply[i:min(i+4, len(reply))]
		if path == "/responses" {
			send("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "sequence_number": i / 4, "item_id": "m1", "output_index": 0, "content_index": 0, "delta": piece})
		} else {
			send("", map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": piece}, "finish_reason": nil}}})
		}
	}
	if path == "/responses" {
		send("response.completed", map[string]any{"type": "response.completed", "sequence_number": len(reply), "response": map[string]any{"usage": map[string]any{"input_tokens": 30}}})
		return
	}
	send("", map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 70}})
	io.WriteString(w, "data: [DONE]\n\n")
}

func (u *copilotUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	switch {
	case r.Host == "api.github.com":
		io.WriteString(w, `{"login":"synthetic"}`)
	case r.Method == http.MethodGet && r.URL.Path == "/models":
		io.WriteString(w, `{"data":[]}`)
	case r.URL.Path == "/telemetry":
		u.analytic.Add(1)
		w.WriteHeader(204)
	case r.URL.Path == "/chat/completions" || r.URL.Path == "/responses":
		u.openAI(w, r.URL.Path, body)
	case r.URL.Path == "/mcp/readonly":
		u.mu.Lock()
		u.mcp = append(u.mcp, string(body))
		u.mu.Unlock()
		io.WriteString(w, `{"jsonrpc":"2.0","id":3,"result":{"content":[]}}`)
	case r.URL.Path == "/v1/messages":
		u.model.Add(1)
		var req struct {
			Messages []struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"messages"`
		}
		if json.Unmarshal(body, &req) != nil || r.Header.Get("Accept-Encoding") != "identity" {
			http.Error(w, "bad request", 400)
			return
		}
		prompt := req.Messages[len(req.Messages)-1].Content[0].Text
		u.mu.Lock()
		u.prompts = append(u.prompts, prompt)
		u.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(name string, value any) {
			data, _ := json.Marshal(value)
			protocol.WriteEvent(w, protocol.Event{Name: name, Data: string(data)})
			w.(http.Flusher).Flush()
		}
		send("message_start", map[string]any{"type": "message_start", "message": map[string]any{"content": []any{}, "usage": map[string]any{"input_tokens": 120, "cache_read_input_tokens": 30}}})
		send("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
		reply := "Echo: " + prompt
		for i := 0; i < len(reply); i += 4 {
			send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": reply[i:min(i+4, len(reply))]}})
		}
		send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
		send("ping", map[string]any{"type": "ping"})
		send("content_block_start", map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "tool_use", "id": "toolu_1", "name": "edit", "input": map[string]any{}, "caller": map[string]any{"type": "direct"}}})
		token := prompt[strings.Index(prompt, mask.Prefix):]
		token = token[:strings.IndexByte(token, ']')+1]
		input, _ := json.Marshal(map[string]string{"path": "notes.txt", "new_str": "Checked " + token})
		for i := 0; i < len(input); i += 5 {
			send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(input[i:min(i+5, len(input))])}})
		}
		send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 1})
		send("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use"}, "copilot_usage": map[string]any{"total_nano_aiu": 5}})
		send("message_stop", map[string]any{"type": "message_stop"})
		io.WriteString(w, "data: [DONE]\n\n")
	default:
		http.Error(w, "unexpected", 418)
	}
}

func gzipped(text string) []byte {
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	z.Write([]byte(text))
	z.Close()
	return b.Bytes()
}

func TestCopilotThroughProxy(t *testing.T) {
	upstreamCA, err := NewAuthority("Synthetic GitHub CA")
	if err != nil {
		t.Fatal(err)
	}
	var certificates []tls.Certificate
	for _, host := range []string{"api.individual.githubcopilot.com", "api.github.com"} {
		cert, err := upstreamCA.Issue(host)
		if err != nil {
			t.Fatal(err)
		}
		certificates = append(certificates, cert)
	}
	upstream := &copilotUpstream{}
	upstreamListener := newPipeListener()
	upstreamServer := &http.Server{Handler: upstream}
	go upstreamServer.Serve(tls.NewListener(upstreamListener, &tls.Config{Certificates: certificates, MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}}))
	t.Cleanup(func() { upstreamServer.Close(); upstreamListener.Close() })
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(upstreamCA.PEM)
	app, err := New(Config{Integrations: []string{CopilotCLI}}, localSecret, Options{Roots: roots, DialContext: upstreamListener.Dial})
	if err != nil {
		t.Fatal(err)
	}
	session, secret, err := app.CreateCLISession("copilot", 4242)
	if err != nil {
		t.Fatal(err)
	}
	proxyListener := newPipeListener()
	tracked := Track(proxyListener, 16)
	proxyServer := &http.Server{Handler: app}
	go proxyServer.Serve(tracked)
	t.Cleanup(func() { tracked.Close(); proxyServer.Close(); app.Close() })
	proxyURL, _ := url.Parse("http://proxy.test:8080")
	proxyURL.User = url.UserPassword("phantomprompt+"+session, secret)
	clientRoots := x509.NewCertPool()
	clientRoots.AppendCertsFromPEM(app.CA.PEM)
	clientRoots.AppendCertsFromPEM(upstreamCA.PEM)
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DialContext: proxyListener.Dial, TLSClientConfig: &tls.Config{RootCAs: clientRoots, MinVersion: tls.VersionTLS12}, ResponseHeaderTimeout: 2 * time.Second, DisableCompression: true}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	call := func(method, target string, body []byte, header http.Header) (*http.Response, string) {
		t.Helper()
		r, _ := http.NewRequest(method, target, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		for name, values := range header {
			r.Header[name] = values
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatalf("%s %s: incomplete response: %v", method, target, err)
		}
		return response, string(data)
	}
	api := "https://api.individual.githubcopilot.com"
	request := func(text string) []byte {
		body, _ := json.Marshal(map[string]any{"model": "synthetic", "stream": true, "system": []any{map[string]any{"type": "text", "text": "Agent rules: name: \"x\" + y"}}, "messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}}}})
		return body
	}

	t.Run("prompt masked, reply and file edit restored", func(t *testing.T) {
		for range 2 {
			response, body := call(http.MethodPost, api+"/v1/messages", request("Read notes.txt with VIN "+syntheticVIN), nil)
			if response.StatusCode != 200 {
				t.Fatalf("status %d: %s", response.StatusCode, body)
			}
			var text, input strings.Builder
			for _, line := range strings.Split(body, "\n") {
				data, ok := strings.CutPrefix(line, "data: ")
				if !ok {
					continue
				}
				var event struct {
					Delta struct{ Type, Text, PartialJSON string } `json:"delta"`
				}
				var raw map[string]any
				json.Unmarshal([]byte(data), &raw)
				json.Unmarshal([]byte(data), &event)
				if delta, ok := raw["delta"].(map[string]any); ok {
					if v, ok := delta["text"].(string); ok {
						text.WriteString(v)
					}
					if v, ok := delta["partial_json"].(string); ok {
						input.WriteString(v)
					}
				}
			}
			var edit map[string]string
			if text.String() != "Echo: Read notes.txt with VIN "+syntheticVIN || json.Unmarshal([]byte(input.String()), &edit) != nil || edit["new_str"] != "Checked "+syntheticVIN {
				t.Fatalf("restored text %q input %q", text.String(), input.String())
			}
		}
		upstream.mu.Lock()
		defer upstream.mu.Unlock()
		if len(upstream.prompts) != 2 || upstream.prompts[0] != upstream.prompts[1] || strings.Contains(upstream.prompts[0], syntheticVIN) || !strings.Contains(upstream.prompts[0], mask.Prefix) {
			t.Fatalf("upstream prompts %q", upstream.prompts)
		}
	})

	t.Run("client session identifier stays readable and is not logged", func(t *testing.T) {
		const claudeSession, copilotSession, otherVIN = "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002", "TEST0000000000002"
		header := http.Header{"X-Claude-Code-Session-Id": {claudeSession}, "X-Client-Session-Id": {copilotSession}}
		text := "Scratchpad /tmp/" + claudeSession + "/x, state " + copilotSession + ", VIN " + otherVIN + " and again " + syntheticVIN
		if response, body := call(http.MethodPost, api+"/v1/messages", request(text), header); response.StatusCode != 200 {
			t.Fatalf("status %d: %s", response.StatusCode, body)
		}
		upstream.mu.Lock()
		prompt := upstream.prompts[len(upstream.prompts)-1]
		upstream.mu.Unlock()
		if !strings.Contains(prompt, "Scratchpad /tmp/"+claudeSession+"/x, state "+copilotSession+", VIN [PP_VIN_") || strings.Contains(prompt, otherVIN) || strings.Contains(prompt, syntheticVIN) {
			t.Fatalf("upstream prompt %q", prompt)
		}
		events := app.Events.Snapshot()
		if last := events[len(events)-1]; last.Matches.GUID != 0 || last.Matches.NewKinds["vin"] != 2 || last.Requests != 3 || last.InputTokens != 450 || last.Matches.TokenOverhead == 0 {
			t.Fatalf("logged event %+v", last)
		}
	})

	t.Run("helper models in OpenAI formats are masked and restored", func(t *testing.T) {
		for _, tc := range []struct{ path, body string }{
			{"/chat/completions", `{"model":"gpt-4o-mini","stream":true,"snippy":{"enabled":true},"messages":[{"role":"user","content":"VIN ` + syntheticVIN + `"}]}`},
			{"/responses", `{"model":"gpt-5.6-luna-utility","stream":true,"input":"VIN ` + syntheticVIN + `"}`},
		} {
			response, body := call(http.MethodPost, api+tc.path, []byte(tc.body), nil)
			if response.StatusCode != 200 {
				t.Fatalf("%s: status %d: %s", tc.path, response.StatusCode, body)
			}
			var text strings.Builder
			for _, line := range strings.Split(body, "\n") {
				data, ok := strings.CutPrefix(line, "data: ")
				if !ok || data == "[DONE]" {
					continue
				}
				var value struct {
					Delta   any `json:"delta"`
					Choices []struct {
						Delta struct{ Content string } `json:"delta"`
					} `json:"choices"`
				}
				json.Unmarshal([]byte(data), &value)
				if delta, ok := value.Delta.(string); ok {
					text.WriteString(delta)
				}
				for _, choice := range value.Choices {
					text.WriteString(choice.Delta.Content)
				}
			}
			if text.String() != "Echo: VIN "+syntheticVIN {
				t.Fatalf("%s: restored %q", tc.path, text.String())
			}
		}
		upstream.mu.Lock()
		defer upstream.mu.Unlock()
		if len(upstream.openai) != 2 || strings.Contains(strings.Join(upstream.openai, ""), syntheticVIN) || !strings.Contains(upstream.openai[0], `"snippy":{"enabled":true}`) {
			t.Fatalf("upstream OpenAI bodies %q", upstream.openai)
		}
	})

	t.Run("MCP tool arguments masked", func(t *testing.T) {
		if response, _ := call(http.MethodPost, api+"/mcp/readonly", []byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_issues","arguments":{"query":"`+syntheticVIN+`"}}}`), nil); response.StatusCode != 200 {
			t.Fatal("MCP call failed")
		}
		upstream.mu.Lock()
		defer upstream.mu.Unlock()
		if len(upstream.mcp) != 1 || strings.Contains(upstream.mcp[0], syntheticVIN) || !strings.Contains(upstream.mcp[0], mask.Prefix) {
			t.Fatalf("MCP upstream received %q", upstream.mcp)
		}
	})

	t.Run("telemetry inspected after decompression", func(t *testing.T) {
		header := http.Header{"Content-Encoding": {"gzip"}, "Content-Type": {"application/x-json-stream"}}
		if response, _ := call(http.MethodPost, api+"/telemetry", gzipped("{\"name\":\"event\",\"data\":{\"ok\":true}}\n{\"name\":\"other\"}\n"), header); response.StatusCode != 204 {
			t.Fatalf("clean telemetry status %d", response.StatusCode)
		}
		if response, _ := call(http.MethodPost, api+"/telemetry", gzipped("{\"message\":\"failed for "+syntheticVIN+" today\"}\n"), header); response.StatusCode != 403 {
			t.Fatalf("sensitive telemetry status %d", response.StatusCode)
		}
		if upstream.analytic.Load() != 1 {
			t.Fatal("sensitive telemetry reached upstream")
		}
	})

	t.Run("unsupported channels fail closed", func(t *testing.T) {
		before := upstream.model.Load()
		upstream.mu.Lock()
		openAIBefore := len(upstream.openai)
		upstream.mu.Unlock()
		for _, tc := range []struct {
			path   string
			body   []byte
			header http.Header
			status int
		}{
			{"/chat/completions", []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"x"}}]}]}`), nil, 422},
			{"/responses", []byte(`{"input":[{"role":"user","content":[{"type":"input_image","image_url":"x"}]}]}`), nil, 422},
			{"/v1/messages", []byte(`{"messages":[{"role":"user","content":[{"type":"image","source":{}}]}]}`), nil, 422},
			{"/v1/messages", []byte(`{"input":"x"}`), http.Header{"Upgrade": {"websocket"}, "Connection": {"Upgrade"}}, 403},
		} {
			if response, _ := call(http.MethodPost, api+tc.path, tc.body, tc.header); response.StatusCode != tc.status {
				t.Fatalf("%s: status %d, want %d", tc.path, response.StatusCode, tc.status)
			}
		}
		upstream.mu.Lock()
		openAIAfter := len(upstream.openai)
		upstream.mu.Unlock()
		if upstream.model.Load() != before || openAIAfter != openAIBefore {
			t.Fatal("blocked request reached the model API")
		}
	})

	if response, body := call(http.MethodGet, "https://api.github.com/user", nil, nil); response.StatusCode != 200 || !strings.Contains(body, "synthetic") || response.TLS.PeerCertificates[0].Issuer.CommonName != "Synthetic GitHub CA" {
		t.Fatal("GitHub API was not tunneled end to end")
	}
	report, _ := json.Marshal(app.Discovery())
	for _, secret := range []string{syntheticVIN, "Read notes.txt", "Agent rules"} {
		if strings.Contains(string(report), secret) {
			t.Fatalf("discovery report contains %q", secret)
		}
	}
	if stats := app.PromptStats(); stats.Checked != 5 || stats.Sensitive != 5 {
		t.Fatalf("prompt statistics %+v", stats)
	}
	sessions := app.CLISessions()
	if len(sessions) != 1 || sessions[0].Label != "copilot" || sessions[0].PID != 4242 || !sessions[0].Active || sessions[0].Prompts != 5 || sessions[0].Masked != 2 || sessions[0].Blocked < 3 || len(sessions[0].Services) != 1 || sessions[0].Services[0] != "GitHub Copilot CLI" || sessions[0].InputTokens != 550 || sessions[0].TokenOverhead == 0 {
		t.Fatalf("CLI sessions %+v", sessions)
	}
	integrations := app.Integrations()
	if len(integrations) != 1 || integrations[0].Service != "GitHub Copilot CLI" || !integrations[0].Protected || integrations[0].Requests != 5 {
		t.Fatalf("integrations %+v", integrations)
	}
	for host, want := range map[string]bool{"api.githubcopilot.com:443": true, "github.com:443": false, "example.test:443": false} {
		adapter, inspect, _ := app.connectPolicy(host)
		if inspect != want || inspect && adapter != CopilotCLI {
			t.Fatalf("%s: adapter %q inspect %v", host, adapter, inspect)
		}
	}
}

func TestClaudeCodeConnectPolicy(t *testing.T) {
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	for host, want := range map[string]string{
		"api.anthropic.com:443":            "inspect",
		"claude.ai:443":                    "tunnel",
		"mcp-proxy.anthropic.com:443":      "inspect",
		"console.anthropic.com:443":        "tunnel",
		"platform.claude.com:443":          "tunnel",
		"bridge.claudeusercontent.com:443": "tunnel",
		"statsig.anthropic.com:443":        "reject",
		"downloads.claude.ai:443":          "tunnel",
		"o1158394.ingest.us.sentry.io:443": "reject",
		"api.githubcopilot.com:443":        "reject",
		"example.test:443":                 "tunnel",
	} {
		adapter, inspect, tunnel := app.connectPolicy(host)
		got := "reject"
		if inspect && adapter == ClaudeCode {
			got = "inspect"
		} else if tunnel {
			got = "tunnel"
		}
		if got != want {
			t.Errorf("%s: got %s, want %s", host, got, want)
		}
	}
}
