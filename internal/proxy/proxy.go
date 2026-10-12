package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elazarl/goproxy"
	"phantomprompt/internal/applog"
	"phantomprompt/internal/mask"
	"phantomprompt/internal/protocol"
)

const MaxBody = 1 << 20
const SessionHeader = "X-PhantomPrompt-Session"

// maxModelRequests bounds model requests in flight across the app; a streamed answer holds its
// slot until it ends, so parallel agents and subagents each keep one.
const maxModelRequests = 128
const AccessHeader = "X-PhantomPrompt-Access"

type Options struct {
	Roots       *x509.CertPool
	DialContext func(context.Context, string, string) (net.Conn, error)
	RulesFile   string
	// Log receives the events that matter; the caller opens and closes it.
	Log *applog.Log
}

type App struct {
	Config         Config
	CA             *Authority
	Sessions       *mask.Store
	Proxy          *goproxy.ProxyHttpServer
	Transport      *http.Transport
	ModelTransport *http.Transport
	access         string
	slots          chan struct{}
	Forwarded      atomic.Uint64
	Blocked        atomic.Uint64
	MaskedVIN      atomic.Uint64
	MaskedPESEL    atomic.Uint64
	MaskedEmail    atomic.Uint64
	MaskedGUID     atomic.Uint64
	MaskedFields   atomic.Uint64
	MaskedCustom   atomic.Uint64
	MaskedSecret   atomic.Uint64
	MaskedFile     atomic.Uint64
	MaskedNew      atomic.Uint64
	MaskedTyped    atomic.Uint64
	MaskedContext  atomic.Uint64
	PromptsSent    atomic.Uint64
	TokenOverhead  atomic.Int64
	InputTokens    atomic.Int64
	UsageOverhead  atomic.Int64
	resets         atomic.Uint64
	SecondInstance atomic.Bool
	Events         Events
	stop           chan struct{}
	once           sync.Once
	policy         mask.Policy
	policyMu       sync.RWMutex
	editMu         sync.Mutex
	rulesFile      string
	filesMu        sync.Mutex
	filesSignature string
	filesStatus    []ProtectedPathStatus
	scanned        map[string]scannedFile
	scannedRules   string
	webMu          sync.Mutex
	webSessions    map[string]string
	gatewayKey     string
	detected       detections

	checks        promptChecks
	discovered    shapes
	turns         turnLog
	customRules   ruleStats
	cliSessions   cliRegistry
	protectionOff atomic.Bool
	shutdown      chan struct{}
	shutdownOnce  sync.Once
}

func (a *App) Protection() bool { return !a.protectionOff.Load() }

func (a *App) SetProtection(on bool) {
	if a.protectionOff.Swap(!on) == on {
		reason := "protection_enabled"
		if !on {
			reason = "protection_disabled"
		}
		a.Events.Add(Event{Phase: "control", Status: 200, Reason: reason})
	}
}

func (a *App) RequestShutdown() {
	a.shutdownOnce.Do(func() { close(a.shutdown) })
}

func (a *App) ShutdownRequested() <-chan struct{} { return a.shutdown }

type tunnel struct{ authority, adapter, session string }

var proxySession = regexp.MustCompile(`^phantomprompt(?:\+([0-9a-f]{32}))?$`)

func (a *App) proxyCredentials(header string) (string, bool) {
	encoded, ok := strings.CutPrefix(header, "Basic ")
	if !ok {
		return "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", false
	}
	user, password, ok := strings.Cut(string(decoded), ":")
	match := proxySession.FindStringSubmatch(user)
	if !ok || match == nil {
		return "", false
	}
	if a.Authorized(password) || match[1] != "" && a.cliSessions.authorized(match[1], password) {
		return match[1], true
	}
	return "", false
}

type certificateCache struct {
	mu      sync.Mutex
	entries map[string]*tls.Certificate
}

func (c *certificateCache) Fetch(host string, generate func() (*tls.Certificate, error)) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cert := c.entries[host]; cert != nil && (cert.Leaf == nil || time.Until(cert.Leaf.NotAfter) > time.Hour) {
		return cert, nil
	}
	cert, err := generate()
	if err == nil {
		c.entries[host] = cert
	}
	return cert, err
}

func New(config Config, access string, options Options) (*App, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if len(access) < 32 {
		return nil, fmt.Errorf("local access token must contain at least 32 characters")
	}
	ca, err := NewAuthority("PhantomPrompt ephemeral local CA")
	if err != nil {
		return nil, err
	}
	policy, err := FieldPolicy(config)
	if err != nil {
		return nil, err
	}
	if options.RulesFile != "" {
		saved, found, err := LoadRules(options.RulesFile)
		if err != nil {
			return nil, fmt.Errorf("%w: fix or delete %s", err, options.RulesFile)
		}
		if found {
			policy = saved
		}
	}
	dial := options.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	tr := &http.Transport{
		Proxy:                  nil,
		DialContext:            dial,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: options.Roots},
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  30 * time.Second,
		MaxResponseHeaderBytes: 32 << 10,
		IdleConnTimeout:        60 * time.Second,
		MaxIdleConns:           64,
		MaxIdleConnsPerHost:    16,
		MaxConnsPerHost:        32,
		DisableCompression:     true,
		ForceAttemptHTTP2:      true,
	}
	// Without streaming, the API sends headers only after the whole answer; a header timeout would
	// fail the request while the upstream keeps generating (and billing), then the client retries.
	model := tr.Clone()
	model.ResponseHeaderTimeout = 0
	a := &App{Config: config, CA: ca, Sessions: mask.NewStore(30*time.Minute, 4*time.Hour, maxCLISessions, policy), Transport: tr, ModelTransport: model, access: access, slots: make(chan struct{}, maxModelRequests), stop: make(chan struct{}), shutdown: make(chan struct{}), policy: policy, rulesFile: options.RulesFile, webSessions: make(map[string]string)}
	a.Events.log = options.Log
	p := goproxy.NewProxyHttpServer()
	a.Proxy = p
	p.Verbose = false
	p.Logger = log.New(io.Discard, "", 0)
	p.Tr = tr
	p.ConnectDial = nil
	p.ConnectDialWithReq = nil
	p.AllowHTTP2 = false
	p.CertStore = &certificateCache{entries: make(map[string]*tls.Certificate)}
	p.ConnectionErrHandler = func(w io.Writer, _ *goproxy.ProxyCtx, _ error) {
		io.WriteString(w, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
	}
	p.NonproxyHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Use the dedicated local gateway or an explicit proxy", http.StatusBadRequest)
	})
	p.OnRequest().HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		adapter, inspect, passThrough := a.connectPolicy(host)
		if passThrough {
			return &goproxy.ConnectAction{Action: goproxy.ConnectAccept}, host
		}
		if !inspect {
			ctx.Resp = failure(ctx.Req, 403, "destination_not_allowed")
			return goproxy.RejectConnect, host
		}
		session, _ := a.proxyCredentials(ctx.Req.Header.Get("Proxy-Authorization"))
		ctx.UserData = tunnel{authority: host, adapter: adapter, session: session}
		return &goproxy.ConnectAction{Action: goproxy.ConnectMitm, TLSConfig: func(host string, ctx *goproxy.ProxyCtx) (*tls.Config, error) {
			cfg, err := goproxy.TLSConfigFromCA(&a.CA.Certificate)(host, ctx)
			if err != nil {
				return nil, err
			}
			cfg.MinVersion = tls.VersionTLS12
			cfg.NextProtos = []string{"http/1.1"}
			expected, _, _ := net.SplitHostPort(host)
			cfg.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
				if hello.ServerName != "" && !strings.EqualFold(hello.ServerName, expected) {
					return nil, fmt.Errorf("TLS server name mismatch")
				}
				return nil, nil
			}
			return cfg, nil
		}}, host
	})
	p.OnRequest().DoFunc(func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		if t, ok := ctx.UserData.(tunnel); ok {
			if r.URL.Scheme != "https" || authority(r.URL) != t.authority || !sameHost(r.Host, t.authority, "https") {
				a.Blocked.Add(1)
				return r, failure(r, 403, "tunnel_target_mismatch")
			}
			if _, cli := cliIntegrations[t.adapter]; cli {
				return a.CLI(r, t)
			}
		} else if r.URL.Scheme != "http" {
			a.Blocked.Add(1)
			return r, failure(r, 403, "CONNECT_required")
		} else if !a.ruleOrigin(r.URL) {
			if a.passThrough(strings.ToLower(r.URL.Hostname())) {
				return r, nil
			}
			return r, failure(r, 403, "destination_not_allowed")
		}
		return r, a.Protected(r)
	})
	p.OnResponse().DoFunc(func(response *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		if response != nil {
			response.Proto, response.ProtoMajor, response.ProtoMinor = "HTTP/1.1", 1, 1
			if response.StatusCode < 200 || response.StatusCode == http.StatusNoContent || response.StatusCode == http.StatusNotModified {
				response.Body, response.ContentLength, response.TransferEncoding = http.NoBody, 0, nil
				response.Header.Set("Content-Length", "0")
				return response
			}
		}
		if t, ok := ctx.UserData.(tunnel); ok {
			if _, cli := cliIntegrations[t.adapter]; cli {
				return a.observeCLIResponse(response, t.adapter)
			}
		}
		return response
	})
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				a.Sessions.Sweep()
				a.sweepTerminals()
				a.refreshProtectedFiles(false)
			case <-a.stop:
				return
			}
		}
	}()
	a.refreshProtectedFiles(true)
	return a, nil
}

func sameHost(host, expected, scheme string) bool {
	u := &url.URL{Host: host, Scheme: scheme}
	return authority(u) == expected
}
func (a *App) allowedTunnel(host string) bool {
	for _, rule := range a.Config.Rules {
		u, _ := url.Parse(rule.Origin)
		if u.Scheme == "https" && authority(u) == host {
			return true
		}
	}
	return false
}
func (a *App) Authorized(value string) bool {
	return subtle.ConstantTimeCompare([]byte(value), []byte(a.access)) == 1
}
func (a *App) ProxyAuthorization() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("phantomprompt:"+a.access))
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.proxyCredentials(r.Header.Get("Proxy-Authorization")); !ok {
		w.Header().Set("Proxy-Authenticate", `Basic realm="PhantomPrompt"`)
		http.Error(w, "Proxy authentication required", 407)
		return
	}
	a.Proxy.ServeHTTP(w, r)
}

func (a *App) Close() {
	a.once.Do(func() {
		close(a.stop)
		a.Transport.CloseIdleConnections()
		a.ModelTransport.CloseIdleConnections()
		a.Sessions.Clear()
	})
}

const slotWait = 10 * time.Second

// acquireSlot waits briefly for a free slot instead of failing a burst of parallel agents at once.
func (a *App) acquireSlot(ctx context.Context) bool {
	select {
	case a.slots <- struct{}{}:
		return true
	default:
	}
	timer := time.NewTimer(slotWait)
	defer timer.Stop()
	select {
	case a.slots <- struct{}{}:
		return true
	case <-timer.C:
		return false
	case <-ctx.Done():
		return false
	}
}

func (a *App) ruleOrigin(target *url.URL) bool {
	for _, rule := range a.Config.Rules {
		origin, _ := url.Parse(rule.Origin)
		if strings.EqualFold(target.Scheme, origin.Scheme) && strings.EqualFold(authority(target), authority(origin)) {
			return true
		}
	}
	return false
}

func (a *App) rule(r *http.Request) (Rule, bool) {
	if r.URL == nil || r.URL.User != nil || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.Fragment != "" || r.URL.RawPath != "" {
		return Rule{}, false
	}
	for _, rule := range a.Config.Rules {
		u, _ := url.Parse(rule.Origin)
		if r.URL.Scheme == u.Scheme && authority(r.URL) == authority(u) && sameHost(r.Host, authority(u), u.Scheme) && r.URL.Path == rule.Path {
			return rule, true
		}
	}
	return Rule{}, false
}

func failure(r *http.Request, code int, category string) *http.Response {
	body := `{"error":"` + category + `"}`
	return &http.Response{StatusCode: code, Status: strconv.Itoa(code) + " " + http.StatusText(code), Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Request: r, Header: http.Header{"Content-Type": []string{"application/json"}, "Cache-Control": []string{"no-store"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Close: true}
}

func readBody(body io.Reader, encoding string) ([]byte, error) {
	compressed, err := io.ReadAll(io.LimitReader(body, MaxBody+1))
	if err != nil || len(compressed) > MaxBody {
		return nil, fmt.Errorf("body_limit_or_read_failure")
	}
	if encoding == "" || encoding == "identity" {
		return compressed, nil
	}
	if encoding != "gzip" {
		return nil, fmt.Errorf("unsupported_encoding")
	}
	z, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("invalid_compression")
	}
	defer z.Close()
	decoded, err := io.ReadAll(io.LimitReader(z, MaxBody+1))
	if err != nil || len(decoded) > MaxBody {
		return nil, fmt.Errorf("decoded_body_limit_or_read_failure")
	}
	return decoded, nil
}

func (a *App) Protected(r *http.Request) (result *http.Response) {
	requestStarted := time.Now()
	event := Event{Phase: "request", Reason: "accepted"}
	emit := true
	defer func() {
		if !emit {
			return
		}
		event.Status = result.StatusCode
		event.ElapsedMS = milliseconds(time.Since(requestStarted))
		if event.Status < 400 {
			a.checks.time(event.OverheadMS)
		}
		a.logPrompt(event)
	}()
	reject := func(code int, category string) *http.Response {
		event.Reason = category
		a.Blocked.Add(1)
		return failure(r, code, category)
	}
	rule, ok := a.rule(r)
	if !ok || r.Method != http.MethodPost || r.Header.Get("Upgrade") != "" {
		return reject(403, "route_or_method_not_supported")
	}
	event.Adapter = rule.Adapter
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return reject(415, "JSON_required")
	}
	if !a.acquireSlot(r.Context()) {
		return reject(503, "concurrency_limit")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	var releaseSession func()
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			cancel()
			if releaseSession != nil {
				releaseSession()
			}
			<-a.slots
		})
	}
	success := false
	defer func() {
		if !success {
			cleanup()
		}
	}()
	session, release, err := a.Sessions.Acquire(r.Header.Get(SessionHeader), rule.Origin+"|"+rule.Adapter)
	if err != nil {
		return reject(409, "session_expired_or_wrong_origin")
	}
	releaseSession = release
	data, err := readBody(r.Body, r.Header.Get("Content-Encoding"))
	if err != nil {
		return reject(413, "body_limit_or_encoding_error")
	}
	event.InputBytes = len(data)
	started := time.Now()
	masked, counts, stream, err := protocol.MaskRequest(rule.Adapter, data, session)
	if err != nil {
		return reject(422, "unsupported_content_or_masking_limit")
	}
	if len(masked) > MaxBody {
		return reject(413, "masked_body_limit")
	}
	maskDuration := time.Since(started)
	event.MaskedBytes, event.Stream = len(masked), stream
	a.setMatches(&event, counts)
	event.MaskMS = float64(maskDuration.Microseconds()) / 1000
	out, err := http.NewRequestWithContext(ctx, http.MethodPost, rule.Origin+rule.Path, bytes.NewReader(masked))
	if err != nil {
		return reject(500, "request_failed")
	}
	out.Header.Set("Content-Type", "application/json")
	out.Header.Set("Accept-Encoding", "identity")
	out.Header.Set("User-Agent", "PhantomPrompt/0.1")
	for _, name := range []string{"Authorization", "X-Api-Key", "Anthropic-Version", "Anthropic-Beta"} {
		if value := r.Header.Get(name); value != "" {
			out.Header.Set(name, value)
		}
	}
	if stream {
		out.Header.Set("Accept", "text/event-stream")
	} else {
		out.Header.Set("Accept", "application/json")
	}
	prepared := time.Since(requestStarted)
	event.OverheadMS = milliseconds(prepared)
	response, err := a.ModelTransport.RoundTrip(out)
	if err != nil {
		return reject(502, "upstream_TLS_or_connection_failed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return reject(502, "upstream_rejected_request")
	}
	responseMedia, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil {
		response.Body.Close()
		return reject(502, "unsupported_upstream_response")
	}
	if encoding := response.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		response.Body.Close()
		return reject(502, "compressed_upstream_response_not_supported")
	}
	response.Header = http.Header{"Content-Type": []string{responseMedia}, "Cache-Control": []string{"no-store"}, "X-PhantomPrompt-Vin-Count": []string{strconv.Itoa(counts.VIN)}, "X-PhantomPrompt-Pesel-Count": []string{strconv.Itoa(counts.PESEL)}, "X-PhantomPrompt-Guid-Count": []string{strconv.Itoa(counts.GUID)}, "X-PhantomPrompt-Email-Count": []string{strconv.Itoa(counts.Email)}, "X-PhantomPrompt-Fields-Count": []string{strconv.Itoa(counts.Fields)}, "X-PhantomPrompt-Mask-Ms": []string{fmt.Sprintf("%.3f", float64(maskDuration.Microseconds())/1000)}}
	a.recordMasked(counts)
	response.Header.Set("X-PhantomPrompt-Custom-Count", strconv.Itoa(counts.Custom))
	if stream {
		if responseMedia != "text/event-stream" {
			response.Body.Close()
			return reject(502, "SSE_required")
		}
		upstream := response.Body
		reader, writer := io.Pipe()
		emit = false
		streamEvent := event
		streamEvent.Status, streamEvent.Reason = 200, "stream_complete"
		go func() {
			defer upstream.Close()
			in := &timedReader{Reader: io.LimitReader(upstream, 16*MaxBody)}
			out := &timedWriter{Writer: writer}
			started := time.Now()
			err := protocol.TransformSSE(rule.Adapter, in, out, session, MaxBody)
			processing := time.Since(started) - in.waited - out.waited
			if err != nil {
				streamEvent.Status, streamEvent.Reason = 502, "stream_invalid_or_interrupted"
				a.Blocked.Add(1)
				protocol.WriteEvent(writer, protocol.Event{Name: "error", Data: `{"error":"upstream_stream_invalid_or_interrupted"}`})
			}
			streamEvent.OverheadMS = milliseconds(prepared + processing)
			streamEvent.ElapsedMS = milliseconds(time.Since(requestStarted))
			if streamEvent.Status < 400 {
				a.checks.time(streamEvent.OverheadMS)
			}
			a.logPrompt(streamEvent)
			writer.CloseWithError(err)
		}()
		response.Body = &managedBody{ReadCloser: reader, cleanup: func() { cleanup(); upstream.Close() }}
		response.ContentLength = -1
		response.TransferEncoding = nil
	} else {
		if responseMedia != "application/json" {
			response.Body.Close()
			return reject(502, "JSON_response_required")
		}
		body, err := readBody(response.Body, "")
		response.Body.Close()
		if err != nil {
			return reject(502, "response_limit")
		}
		restoreStarted := time.Now()
		restored, err := protocol.RestoreResponse(rule.Adapter, body, session)
		if err != nil {
			return reject(502, "unsupported_response_content")
		}
		event.OverheadMS = milliseconds(prepared + time.Since(restoreStarted))
		response.Body = &managedBody{ReadCloser: io.NopCloser(bytes.NewReader(restored)), cleanup: cleanup}
		response.ContentLength = int64(len(restored))
		response.TransferEncoding = nil
	}
	response.Request = r
	success = true
	return response
}

func (a *App) logPrompt(event Event) uint64 {
	logEvent(a.Events.log, event)
	return a.keepPrompt(event)
}

func (a *App) keepPrompt(event Event) uint64 {
	if event.Matches.New == 0 && event.Matches.Attachments == 0 && event.Status < 400 {
		return 0
	}
	return a.Events.keep(event)
}

func (a *App) LogFolder() string { return a.Events.log.Folder() }

func (a *App) PromptStats() PromptStats { return a.checks.Snapshot() }

func (a *App) recordMasked(counts mask.Counts) {
	a.checks.check(counts.Total() > 0)
	a.Forwarded.Add(1)
	a.MaskedVIN.Add(uint64(counts.VIN))
	a.MaskedPESEL.Add(uint64(counts.PESEL))
	a.MaskedEmail.Add(uint64(counts.Email))
	a.MaskedGUID.Add(uint64(counts.GUID))
	a.MaskedFields.Add(uint64(counts.Fields))
	a.MaskedCustom.Add(uint64(counts.Custom))
	a.MaskedSecret.Add(uint64(counts.Secret))
	a.MaskedFile.Add(uint64(counts.File))
	a.MaskedNew.Add(uint64(counts.New))
	a.MaskedTyped.Add(uint64(sum(counts.TypedKinds)))
	a.MaskedContext.Add(uint64(sum(counts.ContextKinds)))
	a.TokenOverhead.Add(int64(counts.TokenOverhead))
	a.customRules.record(counts)
}

func sum(counts map[string]int) int {
	total := 0
	for _, n := range counts {
		total += n
	}
	return total
}

func (a *App) resetTotals() {
	for _, counter := range []*atomic.Uint64{&a.Forwarded, &a.Blocked, &a.MaskedVIN, &a.MaskedPESEL, &a.MaskedEmail, &a.MaskedGUID, &a.MaskedFields, &a.MaskedCustom, &a.MaskedSecret, &a.MaskedFile, &a.MaskedNew, &a.MaskedTyped, &a.MaskedContext, &a.PromptsSent} {
		counter.Store(0)
	}
	for _, counter := range []*atomic.Int64{&a.TokenOverhead, &a.InputTokens, &a.UsageOverhead} {
		counter.Store(0)
	}
	a.cliSessions.resetCounters()
	a.checks.reset()
	a.detected.resetCounters()
	a.resets.Add(1)
}

func (a *App) recordInputTokens(session, service string, tokens, overhead int, period uint64) {
	if tokens <= 0 || period != a.resets.Load() {
		return
	}
	a.InputTokens.Add(int64(tokens))
	a.UsageOverhead.Add(int64(overhead))
	a.cliSessions.record(session, service, func(s *CLISession) {
		s.InputTokens += int64(tokens)
		s.UsageOverhead += int64(overhead)
	})
}

type managedBody struct {
	io.ReadCloser
	cleanup func()
	once    sync.Once
	done    error
}

func (b *managedBody) Read(p []byte) (int, error) {
	if b.done != nil {
		return 0, b.done
	}
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.done = err
		b.Close()
	}
	return n, err
}
func (b *managedBody) Close() error { err := b.ReadCloser.Close(); b.once.Do(b.cleanup); return err }

func WriteResponse(w http.ResponseWriter, response *http.Response) {
	defer response.Body.Close()
	for name, values := range response.Header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	buf := make([]byte, 8192)
	for {
		n, err := response.Body.Read(buf)
		if n > 0 {
			if _, e := w.Write(buf[:n]); e != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}
