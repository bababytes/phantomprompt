package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"phantomprompt/internal/mask"
	"phantomprompt/internal/protocol"
)

// pipeListener exercises the HTTP and TLS stacks without opening system ports.
type pipeListener struct {
	connections chan net.Conn
	done        chan struct{}
	once        sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{connections: make(chan net.Conn), done: make(chan struct{})}
}
func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.connections:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *pipeListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *pipeListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 443} }
func (l *pipeListener) Dial(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.connections <- server:
		return client, nil
	case <-ctx.Done():
		client.Close()
		server.Close()
		return nil, ctx.Err()
	case <-l.done:
		client.Close()
		server.Close()
		return nil, net.ErrClosed
	}
}

func TestPipeHTTPSRoundTrip(t *testing.T) {
	ca, err := NewAuthority("Synthetic upstream CA")
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.Issue("demo.phantomprompt.test")
	if err != nil {
		t.Fatal(err)
	}
	upstreamListener := newPipeListener()
	upstream := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req protocol.DemoRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		if strings.Contains(req.Text, syntheticVIN) || strings.Contains(req.Text, "123e4567-e89b-12d3-a456-426614174000") || strings.Contains(req.Text, "contact@example.test") || strings.Contains(req.Text, "Synthetic User") || strings.Contains(req.Text, "9007199254740993") {
			t.Error("raw VIN reached upstream")
		}
		if strings.Contains(req.Text, "SECRET.ORION") {
			t.Error("custom phrase reached upstream")
		}
		if !req.Stream {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(protocol.DemoResponse{Text: req.Text, Received: req.Text})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		receipt, _ := json.Marshal(map[string]string{"text": req.Text})
		protocol.WriteEvent(w, protocol.Event{Name: "receipt", Data: string(receipt)})
		w.(http.Flusher).Flush()
		for _, r := range req.Text {
			data, _ := json.Marshal(map[string]string{"text": string(r)})
			protocol.WriteEvent(w, protocol.Event{Name: "delta", Data: string(data)})
			w.(http.Flusher).Flush()
		}
		protocol.WriteEvent(w, protocol.Event{Name: "done", Data: `{}`})
	})}
	go upstream.Serve(tls.NewListener(upstreamListener, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}))
	t.Cleanup(func() { upstream.Close(); upstreamListener.Close() })
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca.PEM)
	app, err := New(Config{Rules: []Rule{{Origin: "https://demo.phantomprompt.test", Path: "/echo", Adapter: protocol.Demo}}, CustomRules: []mask.RegexRule{{Pattern: `SECRET\.ORION`}}}, localSecret, Options{Roots: roots, DialContext: upstreamListener.Dial})
	if err != nil {
		t.Fatal(err)
	}
	proxyListener := newPipeListener()
	tracked := Track(proxyListener, 8)
	proxyServer := &http.Server{Handler: app}
	go proxyServer.Serve(tracked)
	t.Cleanup(func() { tracked.Close(); proxyServer.Close(); app.Close() })
	proxyURL, _ := url.Parse("http://proxy.test:8080")
	proxyURL.User = url.UserPassword("phantomprompt", localSecret)
	proxyRoots := x509.NewCertPool()
	proxyRoots.AppendCertsFromPEM(app.CA.PEM)
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DialContext: proxyListener.Dial, TLSClientConfig: &tls.Config{RootCAs: proxyRoots, MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: time.Second, ResponseHeaderTimeout: time.Second}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	for _, stream := range []bool{false, true} {
		name := "JSON"
		if stream {
			name = "SSE"
		}
		t.Run(name, func(t *testing.T) {
			data, _ := json.Marshal(protocol.DemoRequest{Text: "SECRET.ORION " + syntheticVIN + ` 123e4567-e89b-12d3-a456-426614174000 contact@example.test {"firstName":"Synthetic User","accountNumber":9007199254740993}`, Stream: stream})
			response, err := client.Post("https://demo.phantomprompt.test/echo", "application/json", strings.NewReader(string(data)))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != 200 {
				t.Fatalf("status %d: %s", response.StatusCode, body)
			}
			if response.Header.Get("X-PhantomPrompt-Custom-Count") != "1" || !strings.Contains(string(body), "SECRET.ORION") {
				t.Fatal("custom rule count or restoration failed")
			}
			if !strings.Contains(string(body), syntheticVIN) || !strings.Contains(string(body), "Synthetic User") || !strings.Contains(string(body), "9007199254740993") {
				t.Fatal("VIN not restored")
			}
			if stream && !strings.Contains(string(body), "event: done") {
				t.Fatal("stream did not finish")
			}
		})
	}
	t.Run("Concurrent", func(t *testing.T) {
		for i := 0; i < 8; i++ {
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				t.Parallel()
				id, err := app.Sessions.Create(mask.Policy{CustomRules: []mask.RegexRule{{Pattern: `SECRET\.ORION`}}})
				if err != nil {
					t.Fatal(err)
				}
				defer app.Sessions.Delete(id)
				text := fmt.Sprintf("thread-%d SECRET.ORION", i)
				body, _ := json.Marshal(protocol.DemoRequest{Text: text})
				req, _ := http.NewRequest(http.MethodPost, "https://demo.phantomprompt.test/echo", strings.NewReader(string(body)))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set(SessionHeader, id)
				response, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				var result protocol.DemoResponse
				if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&result) != nil || result.Text != text || strings.Contains(result.Received, "SECRET.ORION") {
					t.Fatal("parallel session masking or restoration failed")
				}
			})
		}
	})
	if app.MaskedCustom.Load() != 10 || len(app.Events.Snapshot()) < 10 {
		t.Fatal("proxy counters or event history missing")
	}
}
