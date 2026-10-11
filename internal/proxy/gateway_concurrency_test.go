package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var concurrentEmailToken = regexp.MustCompile(`\[PP_EMAIL_[a-z2-9]{6}\]`)

type concurrentUpstream struct {
	hold   time.Duration
	leaked atomic.Int64
	active atomic.Int64
	peak   atomic.Int64
}

func (u *concurrentUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if strings.Contains(string(body), "@example.com") {
		u.leaked.Add(1)
	}
	n := u.active.Add(1)
	defer u.active.Add(-1)
	for {
		p := u.peak.Load()
		if n <= p || u.peak.CompareAndSwap(p, n) {
			break
		}
	}
	token := concurrentEmailToken.FindString(string(body))
	flusher := w.(http.Flusher)
	if r.Host == "api.anthropic.com" {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":7}}}\n\n")
		io.WriteString(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		flusher.Flush()
		time.Sleep(u.hold)
		io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"mail is "+token+"\"}}\n\n")
		io.WriteString(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		return
	}
	w.Header()["Content-Type"] = nil
	io.WriteString(w, "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"sequence_number\":0,\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_1\"}}\n\n")
	flusher.Flush()
	time.Sleep(u.hold)
	io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":\"mail is "+token+"\"}\n\n")
	io.WriteString(w, "event: response.output_text.done\ndata: {\"type\":\"response.output_text.done\",\"sequence_number\":2,\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"text\":\"mail is "+token+"\"}\n\n")
	io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":3,\"response\":{\"usage\":{\"input_tokens\":9}}}\n\n")
}

func concurrentFixture(t *testing.T, hold time.Duration) (*App, http.Handler, *concurrentUpstream) {
	t.Helper()
	ca, err := NewAuthority("Synthetic multi CA")
	if err != nil {
		t.Fatal(err)
	}
	var certificates []tls.Certificate
	for _, host := range []string{"api.anthropic.com", "chatgpt.com", "api.openai.com"} {
		cert, err := ca.Issue(host)
		if err != nil {
			t.Fatal(err)
		}
		certificates = append(certificates, cert)
	}
	upstream := &concurrentUpstream{hold: hold}
	listener := newPipeListener()
	server := &http.Server{Handler: upstream}
	go server.Serve(tls.NewListener(listener, &tls.Config{Certificates: certificates, MinVersion: tls.VersionTLS12}))
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca.PEM)
	app, err := New(Config{Integrations: []string{ClaudeCode, CodexCLI}}, localSecret, Options{Roots: roots, DialContext: listener.Dial})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close(); server.Close(); listener.Close() })
	return app, app.Gateway("127.0.0.1:18081"), upstream
}

type concurrentCall struct {
	path, body string
	header     http.Header
	want       string
}

func concurrentRequests(t *testing.T, app *App, terminals, parallel int) []concurrentCall {
	var calls []concurrentCall
	for i := 0; i < terminals; i++ {
		for _, agent := range []string{"claude", "codex"} {
			session, secret, err := app.CreateCLISession(agent, 0)
			if err != nil {
				t.Fatal(err)
			}
			for j := 0; j < parallel; j++ {
				email := fmt.Sprintf("%s.t%d.r%d", agent, i, j) + "@" + "example.com"
				if agent == "claude" {
					base := strings.TrimPrefix(ClaudeBaseURL("127.0.0.1:18081", session, secret), "http://127.0.0.1:18081")
					calls = append(calls, concurrentCall{path: base + "/v1/messages?beta=true", want: email,
						body: `{"model":"m","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"Write to ` + email + `"}]}`})
				} else {
					base := strings.TrimPrefix(CodexBaseURL("127.0.0.1:18081", session, secret, "chatgpt"), "http://127.0.0.1:18081")
					calls = append(calls, concurrentCall{path: base + "/responses", want: email,
						body: `{"model":"m","instructions":"rules","stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Write to ` + email + `"}]}]}`})
				}
			}
		}
	}
	return calls
}

// TestManyAgentsAtOnce runs Claude Code and Codex terminals plus App Store gateway sessions in
// parallel while the rules change: every answer must get its own session's value back.
func TestManyAgentsAtOnce(t *testing.T) {
	app, handler, upstream := concurrentFixture(t, 300*time.Millisecond)
	app.SetGatewayKey(strings.Repeat("k", 40))
	calls := concurrentRequests(t, app, 8, 6)
	for i := 0; i < 4; i++ {
		for j := 0; j < 6; j++ {
			email := fmt.Sprintf("mas.s%d.r%d", i, j) + "@" + "example.com"
			calls = append(calls, concurrentCall{path: "/claude/k/" + strings.Repeat("k", 40) + "/v1/messages", want: email,
				header: http.Header{"X-Claude-Code-Session-Id": {fmt.Sprintf("00000000-0000-4000-8000-%012d", i)}},
				body:   `{"model":"m","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"Write to ` + email + `"}]}`})
		}
	}
	stop := make(chan struct{})
	var background sync.WaitGroup
	background.Add(1)
	go func() {
		defer background.Done()
		policy := app.Policy()
		for {
			select {
			case <-stop:
				return
			default:
			}
			policy.PESEL = !policy.PESEL
			if err := app.SetPolicy(policy); err != nil {
				t.Error(err)
				return
			}
			app.CLISessions()
			app.Events.Snapshot()
			time.Sleep(5 * time.Millisecond)
		}
	}()
	started := time.Now()
	var wg sync.WaitGroup
	var failures atomic.Int64
	for _, call := range calls {
		wg.Add(1)
		go func(call concurrentCall) {
			defer wg.Done()
			w := gatewayCall(handler, call.path, call.body, call.header)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "mail is "+call.want) {
				failures.Add(1)
				t.Errorf("%s -> %d %.300s", call.path[:12], w.Code, w.Body.String())
			}
		}(call)
	}
	wg.Wait()
	close(stop)
	background.Wait()
	t.Logf("%d requests (16 terminals + 4 App Store sessions) in %v, upstream peak concurrency %d, failures %d, raw emails upstream %d",
		len(calls), time.Since(started).Round(time.Millisecond), upstream.peak.Load(), failures.Load(), upstream.leaked.Load())
	if upstream.leaked.Load() != 0 {
		t.Fatal("raw value reached upstream")
	}
	if peak := upstream.peak.Load(); peak > maxModelRequests {
		t.Fatalf("upstream saw %d requests at once, limit %d", peak, maxModelRequests)
	}
}

// TestSessionCapacity checks that masking sessions run out together with the terminal registry,
// not before it.
func TestSessionCapacity(t *testing.T) {
	app, _, _ := concurrentFixture(t, 0)
	created := 0
	var last error
	for i := 0; i < maxCLISessions+2; i++ {
		if _, _, err := app.CreateCLISession("claude", 0); err != nil {
			last = err
			break
		}
		created++
	}
	if created != maxCLISessions || last == nil {
		t.Fatalf("created %d CLI sessions before refusal (%v), want %d", created, last, maxCLISessions)
	}
}
