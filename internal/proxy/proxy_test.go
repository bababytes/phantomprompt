package proxy

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"phantomprompt/internal/mask"
	"phantomprompt/internal/protocol"
)

const localSecret = "synthetic-test-access-token-00000000000000"
const syntheticVIN = "TEST0000000000001"

type fixture struct {
	app         *App
	client      *http.Client
	origin      string
	proxyURL    string
	seen        atomic.Int64
	mu          sync.Mutex
	lastBody    string
	lastHeaders http.Header
}

func setup(t testing.TB, useTLS bool, trustUpstream bool) *fixture {
	t.Helper()
	f := &fixture{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read failed", 400)
			return
		}
		f.seen.Add(1)
		f.mu.Lock()
		f.lastBody = string(data)
		f.lastHeaders = r.Header.Clone()
		f.mu.Unlock()
		var req protocol.DemoRequest
		if json.Unmarshal(data, &req) != nil {
			http.Error(w, "JSON required", 400)
			return
		}
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			receipt, _ := json.Marshal(map[string]string{"text": req.Text})
			protocol.WriteEvent(w, protocol.Event{Name: "receipt", Data: string(receipt)})
			for _, r := range req.Text {
				data, _ := json.Marshal(map[string]string{"text": string(r)})
				protocol.WriteEvent(w, protocol.Event{Name: "delta", Data: string(data)})
				w.(http.Flusher).Flush()
			}
			protocol.WriteEvent(w, protocol.Event{Name: "done", Data: `{}`})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(protocol.DemoResponse{Text: req.Text, Received: req.Text})
	})
	upstream := httptest.NewUnstartedServer(handler)
	if useTLS {
		upstream.StartTLS()
	} else {
		upstream.Start()
	}
	t.Cleanup(upstream.Close)
	f.origin = upstream.URL
	roots := x509.NewCertPool()
	if useTLS && trustUpstream {
		roots.AddCert(upstream.Certificate())
	}
	app, err := New(Config{Rules: []Rule{{Origin: upstream.URL, Path: "/echo", Adapter: protocol.Demo}}, GatewayOrigin: upstream.URL}, localSecret, Options{Roots: roots})
	if err != nil {
		t.Fatal(err)
	}
	f.app = app
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tracked := Track(listener, 64)
	server := &http.Server{Handler: app, ReadHeaderTimeout: time.Second, MaxHeaderBytes: 32 << 10}
	go server.Serve(tracked)
	t.Cleanup(func() { tracked.Close(); server.Close(); app.Close() })
	f.proxyURL = "http://" + listener.Addr().String()
	u, _ := url.Parse(f.proxyURL)
	u.User = url.UserPassword("phantomprompt", localSecret)
	mitmRoots := x509.NewCertPool()
	mitmRoots.AppendCertsFromPEM(app.CA.PEM)
	transport := &http.Transport{Proxy: http.ProxyURL(u), TLSClientConfig: &tls.Config{RootCAs: mitmRoots, MinVersion: tls.VersionTLS12}, DisableCompression: true}
	t.Cleanup(transport.CloseIdleConnections)
	f.client = &http.Client{Transport: transport, Timeout: 5 * time.Second}
	return f
}

func (f *fixture) request(t testing.TB, data []byte, session string) *http.Response {
	t.Helper()
	r, err := http.NewRequest(http.MethodPost, f.origin+"/echo", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(SessionHeader, session)
	r.Header.Set(AccessHeader, localSecret)
	response, err := f.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func readResponse(t testing.TB, r *http.Response) []byte {
	t.Helper()
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestHTTPAndHTTPSRoundTrip(t *testing.T) {
	for _, secure := range []bool{false, true} {
		name := "HTTP"
		if secure {
			name = "HTTPS_CONNECT"
		}
		t.Run(name, func(t *testing.T) {
			f := setup(t, secure, true)
			response := f.request(t, []byte(`{"text":"`+syntheticVIN+` and 00210100004"}`), "")
			data := readResponse(t, response)
			if response.StatusCode != 200 {
				t.Fatalf("status %d: %s", response.StatusCode, data)
			}
			var result protocol.DemoResponse
			if json.Unmarshal(data, &result) != nil {
				t.Fatal("invalid JSON response")
			}
			if result.Text != syntheticVIN+" and 00210100004" {
				t.Fatal("original not restored")
			}
			if strings.Contains(result.Received, syntheticVIN) || !strings.Contains(result.Received, mask.Prefix) {
				t.Fatal("upstream received raw VIN")
			}
			if !strings.Contains(result.Received, "00210100004") {
				t.Fatal("PESEL was masked despite default VIN-only policy")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if strings.Contains(f.lastBody, syntheticVIN) {
				t.Fatal("raw VIN crossed network boundary")
			}
			for _, name := range []string{SessionHeader, AccessHeader, "Proxy-Authorization"} {
				if f.lastHeaders.Get(name) != "" {
					t.Fatal("local credential forwarded upstream")
				}
			}
		})
	}
}

func TestHTTPSRejectsUntrustedUpstream(t *testing.T) {
	f := setup(t, true, false)
	r := f.request(t, []byte(`{"text":"`+syntheticVIN+`"}`), "")
	data := readResponse(t, r)
	if r.StatusCode != 502 || f.seen.Load() != 0 {
		t.Fatal("untrusted TLS endpoint received a request")
	}
	if bytes.Contains(data, []byte(syntheticVIN)) || bytes.Contains(data, []byte(f.origin)) {
		t.Fatal("diagnostic leaked sensitive request data")
	}
}

func TestFailClosedFormatsAndLimits(t *testing.T) {
	f := setup(t, true, true)
	for _, body := range []string{`{"text":"` + syntheticVIN + `","image":"unsupported"}`, `not json`, strings.Repeat("a", MaxBody+1)} {
		r := f.request(t, []byte(body), "")
		readResponse(t, r)
		if r.StatusCode < 400 {
			t.Fatal("unsupported request accepted")
		}
	}
	if f.seen.Load() != 0 {
		t.Fatal("blocked content was sent upstream")
	}
}

func TestMaskExpansionLimitBlocksUpstream(t *testing.T) {
	f := setup(t, false, true)
	text := strings.Repeat("x", 940000) + strings.Repeat(" a@b.cc", 8000)
	data, err := json.Marshal(protocol.DemoRequest{Text: text})
	if err != nil || len(data) >= MaxBody {
		t.Fatal("fixture must fit the input limit")
	}
	expanded, _, err := mask.NewSession(4096, 512<<10, mask.DefaultPolicy()).Mask([]string{text})
	if err != nil || len(expanded[0]) <= MaxBody {
		t.Fatal("fixture must expand beyond the limit when masked")
	}
	r := f.request(t, data, "")
	readResponse(t, r)
	if r.StatusCode != 413 || f.seen.Load() != 0 {
		t.Fatal("expanded masked body was sent upstream")
	}
}

func TestSessionIsolationAcrossTLSRequests(t *testing.T) {
	f := setup(t, true, true)
	a, _ := f.app.Sessions.Create(mask.Policy{VIN: true, PESEL: true})
	b, _ := f.app.Sessions.Create(mask.DefaultPolicy())
	var responses []protocol.DemoResponse
	for _, id := range []string{a, a, b} {
		r := f.request(t, []byte(`{"text":"`+syntheticVIN+`"}`), id)
		data := readResponse(t, r)
		if r.StatusCode != 200 {
			t.Fatalf("unexpected status %d", r.StatusCode)
		}
		var value protocol.DemoResponse
		json.Unmarshal(data, &value)
		responses = append(responses, value)
	}
	if responses[0].Received != responses[1].Received {
		t.Fatal("same session generated different tokens")
	}
	if responses[0].Received == responses[2].Received {
		t.Fatal("sessions shared tokens")
	}
	f.app.Sessions.Delete(a)
	r := f.request(t, []byte(`{"text":"`+syntheticVIN+`"}`), a)
	readResponse(t, r)
	if r.StatusCode != 409 || f.seen.Load() != 3 {
		t.Fatal("deleted session was reused")
	}
}

func TestHTTPSStreaming(t *testing.T) {
	f := setup(t, true, true)
	r := f.request(t, []byte(`{"text":"`+syntheticVIN+`","stream":true}`), "")
	data := readResponse(t, r)
	if r.StatusCode != 200 || !bytes.Contains(data, []byte(syntheticVIN)) || !bytes.Contains(data, []byte("event: done")) {
		t.Fatalf("stream failed: status=%d", r.StatusCode)
	}
}

func TestGzipAndCompressionBomb(t *testing.T) {
	f := setup(t, false, true)
	for _, tooLarge := range []bool{false, true} {
		plain := `{"text":"` + syntheticVIN + `"}`
		if tooLarge {
			plain = strings.Repeat("x", MaxBody+1)
		}
		var compressed bytes.Buffer
		z := gzip.NewWriter(&compressed)
		z.Write([]byte(plain))
		z.Close()
		r, _ := http.NewRequest(http.MethodPost, f.origin+"/echo", &compressed)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Content-Encoding", "gzip")
		response, err := f.client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		readResponse(t, response)
		if tooLarge && response.StatusCode != 413 || !tooLarge && response.StatusCode != 200 {
			t.Fatal("incorrect compression handling")
		}
	}
	if f.seen.Load() != 1 {
		t.Fatal("decompression limit did not block upstream")
	}
}

func TestAuthAndHostRestrictions(t *testing.T) {
	f := setup(t, false, true)
	pURL, _ := url.Parse(f.proxyURL)
	tr := &http.Transport{Proxy: http.ProxyURL(pURL)}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: time.Second}
	r, _ := http.NewRequest(http.MethodPost, f.origin+"/echo", strings.NewReader(`{"text":"hello"}`))
	r.Header.Set("Content-Type", "application/json")
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	readResponse(t, response)
	if response.StatusCode != 407 {
		t.Fatal("unauthenticated client accepted")
	}
	r, _ = http.NewRequest(http.MethodPost, f.origin+"/echo?vin="+syntheticVIN, strings.NewReader(`{"text":"hello"}`))
	r.Header.Set("Content-Type", "application/json")
	response, err = f.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	readResponse(t, response)
	if response.StatusCode != 403 || f.seen.Load() != 0 {
		t.Fatal("sensitive query was sent upstream")
	}
}

func TestLocalOriginGuard(t *testing.T) {
	for _, tc := range []struct {
		host, origin, site string
		want               bool
	}{
		{"127.0.0.1:8090", "", "", true},
		{"127.0.0.1:8090", "http://127.0.0.1:8090", "same-origin", true},
		{"evil.example", "", "", false},
		{"127.0.0.1:8090", "https://evil.example", "cross-site", false},
	} {
		r := httptest.NewRequest(http.MethodPost, "/sessions", nil)
		r.Host = tc.host
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Sec-Fetch-Site", tc.site)
		if LocalRequest(r, "127.0.0.1:8090") != tc.want {
			t.Fatal("incorrect local origin decision")
		}
	}
}

func TestOrdinaryTrafficPassesAndUninspectedAIIsRejected(t *testing.T) {
	f := setup(t, false, true)
	var hits atomic.Int64
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.WriteString(w, "package index")
	}))
	defer registry.Close()
	response, err := f.client.Get(registry.URL + "/left-pad")
	if err != nil {
		t.Fatal(err)
	}
	if body := readResponse(t, response); response.StatusCode != 200 || string(body) != "package index" || hits.Load() != 1 {
		t.Fatalf("ordinary HTTP was not passed through: %d %q", response.StatusCode, body)
	}
	for _, target := range []string{"http://api.openai.com/v1/chat/completions", "http://o1.ingest.sentry.io/api/1/store/"} {
		response, err := f.client.Get(target)
		if err != nil {
			t.Fatal(err)
		}
		readResponse(t, response)
		if response.StatusCode != 403 {
			t.Fatalf("%s: HTTP %d, want 403", target, response.StatusCode)
		}
	}
	for host, want := range map[string]bool{
		"registry.npmjs.org:443":                         true,
		"pypi.org:443":                                   true,
		"internal.example:8443":                          true,
		"notopenai.com:443":                              true,
		"cloudcode-pa.googleapis.com:443":                false,
		"generativelanguage.googleapis.com:443":          false,
		"europe-west4-aiplatform.googleapis.com:443":     false,
		"bedrock-runtime.eu-central-1.amazonaws.com:443": false,
		"auth.openai.com:443":                            false,
		"api.x.ai:443":                                   false,
		"chatgpt.com:8443":                               false,
		"o1.ingest.us.sentry.io:443":                     false,
	} {
		if _, inspect, tunnel := f.app.connectPolicy(host); inspect || tunnel != want {
			t.Errorf("%s: inspect %v tunnel %v, want tunnel %v", host, inspect, tunnel, want)
		}
	}
}
