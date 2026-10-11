package proxy

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type anthropicUpstream struct {
	mu       sync.Mutex
	bodies   []string
	queries  []string
	betas    []string
	sessions []string
	mangle   func(string) string
}

func (u *anthropicUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	u.mu.Lock()
	u.bodies = append(u.bodies, string(body))
	u.queries = append(u.queries, r.URL.RawQuery)
	u.betas = append(u.betas, r.Header.Get("Anthropic-Beta"))
	u.mu.Unlock()
	token := regexp.MustCompile(`\[PP_VIN_[a-z2-9]{6}\]`).FindString(string(body))
	if u.mangle != nil {
		token = u.mangle(token)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Anthropic-Ratelimit-Unified-Status", "allowed")
	io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":7}}}\n\n")
	io.WriteString(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
	io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"VIN is "+token+"\"}}\n\n")
	io.WriteString(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
	io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
}

func gatewayFixture(t *testing.T) (*App, http.Handler, *anthropicUpstream) {
	t.Helper()
	upstream := &anthropicUpstream{}
	app, handler := gatewayFixtureWith(t, upstream)
	return app, handler, upstream
}

func gatewayFixtureWith(t *testing.T, upstream http.Handler) (*App, http.Handler) {
	t.Helper()
	return gatewayFixtureOptions(t, upstream, Options{})
}

func gatewayFixtureOptions(t *testing.T, upstream http.Handler, options Options) (*App, http.Handler) {
	t.Helper()
	ca, err := NewAuthority("Synthetic Anthropic CA")
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.Issue("api.anthropic.com")
	if err != nil {
		t.Fatal(err)
	}
	listener := newPipeListener()
	server := &http.Server{Handler: upstream}
	go server.Serve(tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}))
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca.PEM)
	options.Roots, options.DialContext = roots, listener.Dial
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close(); server.Close(); listener.Close() })
	return app, app.Gateway("127.0.0.1:18081")
}

func gatewayCall(handler http.Handler, path, body string, header http.Header) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Host = "127.0.0.1:18081"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Anthropic-Beta", "oauth-2025-04-20,future-capability")
	for name, values := range header {
		r.Header[name] = values
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

const gatewayRequest = `{"model":"m","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"Check TEST0000000000001"}]}`

func TestClaudeGatewayMasksAndRestores(t *testing.T) {
	app, handler, upstream := gatewayFixture(t)
	session, secret, err := app.CreateCLISession("claude", 0)
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimPrefix(ClaudeBaseURL("127.0.0.1:18081", session, secret), "http://127.0.0.1:18081")
	w := gatewayCall(handler, base+"/v1/messages?beta=true", gatewayRequest, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "VIN is TEST0000000000001") || w.Header().Get("Anthropic-Ratelimit-Unified-Status") != "allowed" {
		t.Fatalf("gateway response %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	if strings.Contains(upstream.bodies[0], "TEST0000000000001") || !strings.Contains(upstream.bodies[0], "[PP_VIN_") || upstream.queries[0] != "beta=true" || upstream.betas[0] != "oauth-2025-04-20,future-capability" {
		t.Fatalf("upstream got %q query %q beta %q", upstream.bodies[0], upstream.queries[0], upstream.betas[0])
	}
	if gatewayCall(handler, base[:len(base)-4]+"xxxx/v1/messages", gatewayRequest, nil).Code != http.StatusUnauthorized {
		t.Fatal("wrong secret accepted")
	}
	if gatewayCall(handler, base+"/api/hello", "", nil).Code != http.StatusNotFound {
		t.Fatal("non-model route forwarded")
	}
	if gatewayCall(handler, base+"/v1/messages", gatewayRequest, http.Header{"Origin": {"https://evil.test"}}).Code != http.StatusForbidden {
		t.Fatal("browser request accepted")
	}
	if len(upstream.bodies) != 1 {
		t.Fatalf("blocked requests reached the upstream: %d", len(upstream.bodies))
	}
}

func TestClaudeGatewayKeepsSentTurnsAfterLearningAValue(t *testing.T) {
	app, handler, upstream := gatewayFixture(t)
	session, secret, err := app.CreateCLISession("claude", 0)
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimPrefix(ClaudeBaseURL("127.0.0.1:18081", session, secret), "http://127.0.0.1:18081")
	first := `{"role":"user","content":"Please ask Kowalczyk about the delivery."}`
	for _, messages := range []string{first, first + `,{"role":"assistant","content":"Sure."},{"role":"user","content":"lastName: Kowalczyk"}`} {
		if w := gatewayCall(handler, base+"/v1/messages", `{"model":"m","max_tokens":10,"stream":true,"messages":[`+messages+`]}`, nil); w.Code != 200 {
			t.Fatalf("gateway response %d %s", w.Code, w.Body.String())
		}
	}
	sent := func(body string) []json.RawMessage {
		var root struct {
			Messages []json.RawMessage `json:"messages"`
		}
		if err := json.Unmarshal([]byte(body), &root); err != nil {
			t.Fatal(err)
		}
		return root.Messages
	}
	before, after := sent(upstream.bodies[0]), sent(upstream.bodies[1])
	if !bytes.Equal(before[0], after[0]) {
		t.Fatalf("turn already sent changed:\n%s\n%s", before[0], after[0])
	}
	if strings.Contains(string(after[2]), "Kowalczyk") {
		t.Fatalf("learned value sent in clear in the new turn: %s", after[2])
	}
}

func TestClaudeGatewayKeySeparatesClaudeSessions(t *testing.T) {
	app, handler, upstream := gatewayFixture(t)
	base := "/claude/k/synthetic-gateway-key-0000000000000000"
	if gatewayCall(handler, base+"/v1/messages", gatewayRequest, nil).Code != http.StatusUnauthorized {
		t.Fatal("key route works before a key is set")
	}
	app.SetGatewayKey("synthetic-gateway-key-0000000000000000")
	vin := regexp.MustCompile(`\[PP_VIN_[a-z2-9]{6}\]`)
	for _, claude := range []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"} {
		if w := gatewayCall(handler, base+"/v1/messages", gatewayRequest, http.Header{"X-Claude-Code-Session-Id": {claude}}); w.Code != 200 {
			t.Fatalf("key route: %d %s", w.Code, w.Body.String())
		}
	}
	first, again, other := vin.FindString(upstream.bodies[0]), vin.FindString(upstream.bodies[1]), vin.FindString(upstream.bodies[2])
	if first == "" || first != again || first == other {
		t.Fatalf("placeholders %q %q %q: one map per Claude Code session expected", first, again, other)
	}
}

func TestClaudeGatewayReportsPlaceholdersNotRestored(t *testing.T) {
	app, handler, upstream := gatewayFixture(t)
	session, secret, err := app.CreateCLISession("claude", 0)
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimPrefix(ClaudeBaseURL("127.0.0.1:18081", session, secret), "http://127.0.0.1:18081")
	unrestored := func() []Event {
		var found []Event
		for _, event := range app.Events.Snapshot() {
			if event.Reason == "placeholders_not_restored" {
				found = append(found, event)
			}
		}
		return found
	}
	if w := gatewayCall(handler, base+"/v1/messages", gatewayRequest, nil); w.Code != 200 || len(unrestored()) != 0 {
		t.Fatalf("a restored answer was reported: %d %+v", w.Code, unrestored())
	}
	upstream.mangle = strings.ToUpper
	w := gatewayCall(handler, base+"/v1/messages", gatewayRequest, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "VIN is [PP_VIN_") {
		t.Fatalf("gateway response %d %s", w.Code, w.Body.String())
	}
	if found := unrestored(); len(found) != 1 || found[0].Unrestored != 1 || found[0].Phase != "response" {
		t.Fatalf("unrestored events %+v", found)
	}
}

func TestClaudeGatewayRejectsTooManySecretsWithoutCallingUpstream(t *testing.T) {
	app, handler, upstream := gatewayFixture(t)
	session, secret, err := app.CreateCLISession("claude", 0)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for i := range 4097 {
		fmt.Fprintf(&text, "%s_%036d ", "ghp", i)
	}
	content, _ := json.Marshal(text.String())
	body := `{"model":"m","max_tokens":10,"stream":true,"messages":[{"role":"user","content":` + string(content) + `}]}`
	base := strings.TrimPrefix(ClaudeBaseURL("127.0.0.1:18081", session, secret), "http://127.0.0.1:18081")
	w := gatewayCall(handler, base+"/v1/messages", body, nil)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "masking_limit") {
		t.Fatalf("gateway response %d %s", w.Code, w.Body.String())
	}
	if len(upstream.bodies) != 0 {
		t.Fatalf("a request over the secret limit reached the upstream %d times", len(upstream.bodies))
	}
}

func TestClaudeGatewayEndsAMalformedStreamAndReleasesItsSlot(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":{\"x\":1},\"text\":\"hi\"}}\n\n")
		io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	})
	app, handler := gatewayFixtureWith(t, upstream)
	session, secret, err := app.CreateCLISession("claude", 0)
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimPrefix(ClaudeBaseURL("127.0.0.1:18081", session, secret), "http://127.0.0.1:18081")
	w := gatewayCall(handler, base+"/v1/messages", gatewayRequest, nil)
	if strings.Contains(w.Body.String(), "message_stop") {
		t.Fatalf("a malformed stream was forwarded as complete: %s", w.Body.String())
	}
	if len(app.slots) != 0 {
		t.Fatalf("%d model slots still held", len(app.slots))
	}
	events := app.Events.Snapshot()
	if len(events) == 0 || events[len(events)-1].Status != 502 || events[len(events)-1].Reason != "stream_interrupted" {
		t.Fatalf("events %+v", events)
	}
}

func TestClaudeGatewayReportsIssuedPlaceholdersLeftWithoutReason(t *testing.T) {
	var unknownDelta bool
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		token := regexp.MustCompile(`\[PP_VIN_[a-z2-9]{6}\]`).FindString(string(body))
		email := regexp.MustCompile(`\[PP_EMAIL_[a-z2-9]{6}\]`).FindString(string(body))
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n")
		io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"check "+token+"\"}}\n\n")
		if unknownDelta {
			io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"future_delta\",\"note\":\"mail "+email+"\"}}\n\n")
		}
		io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	})
	app, handler := gatewayFixtureWith(t, upstream)
	session, secret, err := app.CreateCLISession("claude", 0)
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimPrefix(ClaudeBaseURL("127.0.0.1:18081", session, secret), "http://127.0.0.1:18081")
	request := strings.Replace(gatewayRequest, `"Check `, `"Check ana`+"@"+`example.com `, 1)
	unrestored := func() int {
		total := 0
		for _, event := range app.Events.Snapshot() {
			if event.Reason == "placeholders_not_restored" {
				total += event.Unrestored
			}
		}
		return total
	}
	if w := gatewayCall(handler, base+"/v1/messages", request, nil); w.Code != 200 || unrestored() != 0 {
		t.Fatalf("a placeholder kept in signed thinking was reported: %d, %d unrestored", w.Code, unrestored())
	}
	unknownDelta = true
	if w := gatewayCall(handler, base+"/v1/messages", request, nil); w.Code != 200 || unrestored() != 1 {
		t.Fatalf("an issued placeholder left in an unknown delta was not reported: %d, %d unrestored", w.Code, unrestored())
	}
}

func TestTerminalAndGlobalTotalsCountTheSameRequests(t *testing.T) {
	status := http.StatusTooManyRequests
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if status != 200 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			io.WriteString(w, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Encoding", "gzip")
		io.WriteString(w, "not really gzip")
	})
	app, handler := gatewayFixtureWith(t, upstream)
	session, secret, err := app.CreateCLISession("claude", 0)
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimPrefix(ClaudeBaseURL("127.0.0.1:18081", session, secret), "http://127.0.0.1:18081")
	if w := gatewayCall(handler, base+"/v1/messages", gatewayRequest, nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("provider error not passed on: %d", w.Code)
	}
	status = 200
	gatewayCall(handler, base+"/v1/messages", strings.Replace(gatewayRequest, "Check", "Check also 1234abcd-"+"0000-4000-8000-"+"00000000beef in", 1), nil)
	sessions := app.CLISessions()
	if len(sessions) != 1 || sessions[0].Requests != 2 || sessions[0].TokenOverhead != app.TokenOverhead.Load() || uint64(sessions[0].Requests) != app.Forwarded.Load() || sessions[0].Masked != app.MaskedNew.Load() {
		t.Fatalf("terminal %+v, global overhead %d forwarded %d new %d", sessions, app.TokenOverhead.Load(), app.Forwarded.Load(), app.MaskedNew.Load())
	}
}

func TestTokenShareCountsOnlyRequestsWithReportedInputTokens(t *testing.T) {
	status := http.StatusTooManyRequests
	received, finish := make(chan struct{}), make(chan struct{})
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if status != 200 {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":7}}}\n\n")
		w.(http.Flusher).Flush()
		received <- struct{}{}
		<-finish
		io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	})
	app, handler := gatewayFixtureWith(t, upstream)
	session, secret, err := app.CreateCLISession("claude", 0)
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimPrefix(ClaudeBaseURL("127.0.0.1:18081", session, secret), "http://127.0.0.1:18081")
	gatewayCall(handler, base+"/v1/messages", gatewayRequest, nil)
	unreported := app.TokenOverhead.Load()
	if unreported == 0 || app.UsageOverhead.Load() != 0 || app.InputTokens.Load() != 0 {
		t.Fatalf("provider error: overhead %d, reported %d, input %d", unreported, app.UsageOverhead.Load(), app.InputTokens.Load())
	}
	status = 200
	done := make(chan int)
	go func() { done <- gatewayCall(handler, base+"/v1/messages", gatewayRequest, nil).Code }()
	<-received
	for deadline := time.Now().Add(2 * time.Second); app.TokenOverhead.Load() == unreported && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if app.TokenOverhead.Load() == unreported || app.UsageOverhead.Load() != 0 || app.InputTokens.Load() != 0 {
		t.Fatalf("reply in progress: overhead %d, reported %d, input %d", app.TokenOverhead.Load(), app.UsageOverhead.Load(), app.InputTokens.Load())
	}
	finish <- struct{}{}
	if code := <-done; code != 200 {
		t.Fatalf("status %d", code)
	}
	if app.InputTokens.Load() != 7 || app.UsageOverhead.Load() != app.TokenOverhead.Load()-unreported {
		t.Fatalf("reply finished: overhead %d, reported %d, input %d", app.TokenOverhead.Load(), app.UsageOverhead.Load(), app.InputTokens.Load())
	}
	if terminal := app.CLISessions()[0]; terminal.InputTokens != 7 || terminal.UsageOverhead != app.UsageOverhead.Load() || terminal.TokenOverhead != app.TokenOverhead.Load() {
		t.Fatalf("terminal %+v", terminal)
	}

	go func() { done <- gatewayCall(handler, base+"/v1/messages", gatewayRequest, nil).Code }()
	<-received
	for deadline := time.Now().Add(2 * time.Second); app.Forwarded.Load() != 3 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	clear := httptest.NewRequest(http.MethodDelete, "/events", nil)
	clear.Header.Set(AccessHeader, localSecret)
	app.Control(httptest.NewRecorder(), clear)
	finish <- struct{}{}
	if code := <-done; code != 200 {
		t.Fatalf("status %d", code)
	}
	if terminal := app.CLISessions()[0]; app.InputTokens.Load() != 0 || app.UsageOverhead.Load() != 0 || terminal.InputTokens != 0 || terminal.UsageOverhead != 0 {
		t.Fatalf("reply to a request sent before Clear history counted after it: input %d, reported %d, terminal %+v", app.InputTokens.Load(), app.UsageOverhead.Load(), terminal)
	}
}

func TestClaudeGatewayReportsWhatTheReplyReallyWas(t *testing.T) {
	var reply func(w http.ResponseWriter, token string)
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		reply(w, regexp.MustCompile(`\[PP_VIN_[a-z2-9]{6}\]`).FindString(string(body)))
	})
	app, handler := gatewayFixtureWith(t, upstream)
	session, secret, err := app.CreateCLISession("claude", 0)
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimPrefix(ClaudeBaseURL("127.0.0.1:18081", session, secret), "http://127.0.0.1:18081")
	request := strings.Replace(gatewayRequest, `"stream":true,`, "", 1)
	last := func() Event {
		events := app.Events.Snapshot()
		return events[len(events)-1]
	}

	reply = func(w http.ResponseWriter, token string) {
		var body bytes.Buffer
		z := gzip.NewWriter(&body)
		io.WriteString(z, `{"content":[{"type":"text","text":"VIN is `+token+`"}]}`)
		z.Close()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(body.Bytes())
	}
	if w := gatewayCall(handler, base+"/v1/messages", request, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "VIN is TEST0000000000001") || w.Header().Get("Content-Encoding") != "" {
		t.Fatalf("gzip reply: %d %q %s", w.Code, w.Header().Get("Content-Encoding"), w.Body.String())
	}

	reply = func(w http.ResponseWriter, token string) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"content":[{"type":"text","text":"VIN is `+token)
	}
	if w := gatewayCall(handler, base+"/v1/messages", request, nil); w.Code != 502 || strings.Contains(w.Body.String(), "[PP_VIN_") {
		t.Fatalf("malformed 2xx reply: %d %s", w.Code, w.Body.String())
	}
	if event := last(); event.Status != 502 || event.Reason != "unsupported_response_content" {
		t.Fatalf("malformed 2xx reply logged as %d %s", event.Status, event.Reason)
	}

	reply = func(w http.ResponseWriter, token string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`)
	}
	if w := gatewayCall(handler, base+"/v1/messages", request, nil); w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "slow down") {
		t.Fatalf("provider error not passed on: %d %s", w.Code, w.Body.String())
	}

	reply = func(w http.ResponseWriter, token string) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"VIN is "+token+"\"}}\n\n")
	}
	w := gatewayCall(handler, base+"/v1/messages", gatewayRequest, nil)
	if !strings.Contains(w.Body.String(), "VIN is TEST0000000000001") || !strings.Contains(w.Body.String(), "event: error") || !strings.Contains(w.Body.String(), "stream_interrupted") {
		t.Fatalf("a stream cut before message_stop did not end with an error event: %s", w.Body.String())
	}
	if event := last(); event.Status != 502 || event.Reason != "stream_interrupted" {
		t.Fatalf("a cut stream logged as %d %s", event.Status, event.Reason)
	}
}
