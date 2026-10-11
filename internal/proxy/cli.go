package proxy

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"phantomprompt/internal/mask"
	"phantomprompt/internal/protocol"
)

const CopilotCLI = "copilot-cli"
const ClaudeCode = "claude-code"
const CodexCLI = "codex-cli"

var clientIdentityHeaders = []string{
	"X-Claude-Code-Session-Id", "X-Claude-Code-Prompt-Id", "X-Claude-Code-Agent-Id", "X-Claude-Code-Parent-Agent-Id", "X-Claude-Remote-Session-Id", "X-Claude-Remote-Container-Id",
	"X-Client-Session-Id", "X-Interaction-Id", "X-Agent-Task-Id", "X-Parent-Agent-Id",
}

func clientIdentifiers(header http.Header) []string {
	var ids []string
	for _, name := range clientIdentityHeaders {
		ids = append(ids, mask.GUIDs(header.Get(name))...)
	}
	return ids
}

// Long Claude Code conversations (1M-token context) send several MiB on every turn.
const maxCLIBody = 32 << 20
const maxShapes = 100

// A terminal keeps its session alive every 30 s; the lifetime only stops abandoned ones.
const cliLifetime = 7 * 24 * time.Hour
const cliModelTimeout = time.Hour

type cliIntegration struct {
	Service string
	Inspect func(host string) bool
	Support func(host string) bool
}

var cliIntegrations = map[string]cliIntegration{
	CopilotCLI: {Service: "GitHub Copilot CLI", Inspect: copilotAPIHost, Support: githubSupportHost},
	ClaudeCode: {Service: "Claude Code", Inspect: anthropicAPIHost, Support: anthropicSupportHost},
	// Codex is protected only through its gateway route: its built-in provider speaks WebSocket
	// with compressed frames, which the HTTPS proxy does not inspect, so it is left to the
	// known-AI-host rule (rejected) there.
	CodexCLI: {Service: "Codex CLI", Inspect: func(string) bool { return false }, Support: func(string) bool { return false }},
}

// Hosts that carry model traffic or tool data are inspected; login, update and Chrome bridge
// hosts are tunneled. Other *.anthropic.com hosts (for example telemetry) are rejected.
func anthropicAPIHost(host string) bool {
	return host == "api.anthropic.com" || host == "mcp-proxy.anthropic.com"
}

func anthropicSupportHost(host string) bool {
	switch host {
	case "claude.ai", "platform.claude.com", "console.anthropic.com":
		return true
	}
	return strings.HasSuffix(host, ".claude.ai") || strings.HasSuffix(host, ".claudeusercontent.com")
}

func copilotAPIHost(host string) bool {
	return host == "githubcopilot.com" || strings.HasSuffix(host, ".githubcopilot.com")
}

func githubSupportHost(host string) bool {
	return host == "github.com" || host == "api.github.com" || strings.HasSuffix(host, ".githubusercontent.com")
}

func knownIntegration(name string) bool {
	_, cli := cliIntegrations[name]
	return cli
}

var (
	arrayIndex  = regexp.MustCompile(`\[\d+\]`)
	enumValue   = regexp.MustCompile(`^[A-Za-z0-9_.:/\-]{1,64}$`)
	pathSegment = regexp.MustCompile(`^[a-z][a-z0-9_.\-]{0,40}$`)
	enumKeys    = map[string]bool{"type": true, "role": true, "object": true, "event": true, "finish_reason": true, "stop_reason": true, "model": true, "status": true, "tool_choice": true, "reasoning_effort": true}
)

type Shape struct {
	Time      time.Time `json:"time"`
	Direction string    `json:"direction"`
	Method    string    `json:"method,omitempty"`
	Path      string    `json:"path"`
	Media     string    `json:"media,omitempty"`
	Status    int       `json:"status,omitempty"`
	Event     string    `json:"event,omitempty"`
	Body      any       `json:"body,omitempty"`
	Note      string    `json:"note,omitempty"`
}

type shapes struct {
	mu      sync.Mutex
	items   []Shape
	seen    map[string]bool
	sampled map[string]int
}

// responsesPerPath bounds how many responses per route are parsed only to learn their shape.
const responsesPerPath = 20

func (s *shapes) sample(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sampled == nil {
		s.sampled = make(map[string]int)
	}
	s.sampled[path]++
	return s.sampled[path] <= responsesPerPath
}

func (s *shapes) add(shape Shape) {
	key, _ := json.Marshal(Shape{Direction: shape.Direction, Method: shape.Method, Path: shape.Path, Media: shape.Media, Event: shape.Event, Body: shape.Body, Note: shape.Note})
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = make(map[string]bool)
	}
	if s.seen[string(key)] || len(s.items) >= maxShapes {
		return
	}
	s.seen[string(key)] = true
	shape.Time = time.Now().UTC()
	s.items = append(s.items, shape)
}

func (s *shapes) Snapshot() []Shape {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Shape{}, s.items...)
}

func (a *App) Discovery() []Shape { return a.discovered.Snapshot() }

func normalizedPath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part != "" && !pathSegment.MatchString(part) {
			parts[i] = ":id"
		}
	}
	return strings.Join(parts, "/")
}

// userData marks keys whose values come from the user or the model, not from the protocol.
var userData = map[string]bool{"input": true, "arguments": true, "params": true, "content": true, "text": true}

func skeleton(value any, key, parent string, depth int) any {
	return shapeOf(value, key, parent, depth, false)
}

func shapeOf(value any, key, parent string, depth int, user bool) any {
	if depth > 10 {
		return "…"
	}
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			name := k
			if !pathSegment.MatchString(strings.ToLower(k)) {
				name = ":key"
			}
			out[name] = shapeOf(item, k, key, depth+1, user || userData[k] && !isBlockList(item))
		}
		return out
	case []any:
		if len(v) == 0 {
			return []any{}
		}
		variants := make(map[string]any)
		for _, item := range v {
			shape := shapeOf(item, key, parent, depth+1, user)
			encoded, _ := json.Marshal(shape)
			variants[string(encoded)] = shape
		}
		keys := make([]string, 0, len(variants))
		for k := range variants {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make([]any, 0, min(len(keys), 6))
		for _, k := range keys[:min(len(keys), 6)] {
			out = append(out, variants[k])
		}
		return out
	case string:
		if !user && (enumKeys[key] || key == "name" && parent == "tools") && enumValue.MatchString(v) {
			return "=" + v
		}
		if _, ok := decodeJSONString(v); ok {
			return "json-string"
		}
		return "string"
	case json.Number:
		return "number"
	case bool:
		return "bool"
	case nil:
		return nil
	}
	return "?"
}

// isBlockList reports whether content is a list of typed blocks, whose types are protocol data.
func isBlockList(value any) bool {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return false
	}
	first, ok := items[0].(map[string]any)
	_, typed := first["type"]
	return ok && typed
}

func decodeJSONString(text string) (any, bool) {
	if !strings.HasPrefix(text, "{") && !strings.HasPrefix(text, "[") {
		return nil, false
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, false
	}
	return value, true
}

func decodeJSON(data []byte) (any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, false
	}
	return value, true
}

func exposedStrings(value any, session *mask.Session, path string) (string, string, error) {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if where, kind, err := exposedStrings(v[k], session, path+"."+k); where != "" || err != nil {
				return where, kind, err
			}
		}
	case []any:
		for i, item := range v {
			if where, kind, err := exposedStrings(item, session, fmt.Sprintf("%s[%d]", path, i)); where != "" || err != nil {
				return where, kind, err
			}
		}
	case string:
		if nested, ok := decodeJSONString(v); ok {
			return exposedStrings(nested, session, path+"(json)")
		}
		return exposedText(v, session, path)
	case json.Number:
		return exposedText(v.String(), session, path)
	}
	return "", "", nil
}

func exposedText(text string, session *mask.Session, path string) (string, string, error) {
	kinds, err := session.Unmasked(text)
	if err != nil {
		return path, "", err
	}
	for _, kind := range kinds {
		if kind != "GUID" {
			return path, kind, nil
		}
	}
	return "", "", nil
}

func exposedQuery(query url.Values, session *mask.Session) (string, string, error) {
	names := slices.Sorted(maps.Keys(query))
	for _, name := range names {
		for _, value := range query[name] {
			if where, kind, err := exposedText(value, session, normalizedPath(name)); where != "" || err != nil {
				return where, kind, err
			}
		}
	}
	return "", "", nil
}

func keyPath(path string) string { return arrayIndex.ReplaceAllString(path, "[]") }

func (a *App) cliSession(id string) (*mask.Session, func(), error) {
	if id == "" {
		return a.sharedCLISession()
	}
	return a.Sessions.Acquire(id, "cli")
}

// sharedCLISession keeps one map for clients that use the main token (a proxy set by hand),
// so a value gets the same placeholder on every turn and the provider's prompt cache keeps hitting.
func (a *App) sharedCLISession() (*mask.Session, func(), error) {
	a.webMu.Lock()
	defer a.webMu.Unlock()
	for range 2 {
		id := a.webSessions["cli"]
		if id == "" {
			a.policyMu.RLock()
			policy := clonePolicy(a.policy)
			policy.LenientFields = true
			created, err := a.Sessions.CreateFor(policy, 65536, 8<<20, cliLifetime)
			a.policyMu.RUnlock()
			if err != nil {
				return nil, nil, err
			}
			a.webSessions["cli"] = created
			id = created
		}
		session, release, err := a.Sessions.Acquire(id, "cli")
		if err == nil {
			return session, release, nil
		}
		delete(a.webSessions, "cli")
	}
	return nil, nil, mask.ErrSession
}

func (a *App) CreateCLISession(label string, pid int) (string, string, error) {
	secret, err := mask.RandomID()
	if err != nil {
		return "", "", err
	}
	a.policyMu.RLock()
	policy := clonePolicy(a.policy)
	policy.LenientFields = true
	id, err := a.Sessions.CreateFor(policy, 65536, 8<<20, cliLifetime)
	a.policyMu.RUnlock()
	if err != nil {
		return "", "", err
	}
	if !a.cliSessions.add(id, secret, label, pid) {
		a.Sessions.Delete(id)
		return "", "", mask.ErrLimit
	}
	return id, secret, nil
}

func readCLIBody(r *http.Request) ([]byte, []byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxCLIBody+1))
	r.Body.Close()
	if err != nil || len(raw) > maxCLIBody {
		return nil, nil, errors.New("body_limit")
	}
	switch encoding := r.Header.Get("Content-Encoding"); encoding {
	case "", "identity":
		return raw, raw, nil
	case "gzip":
		z, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, nil, errors.New("invalid_compression")
		}
		defer z.Close()
		decoded, err := io.ReadAll(io.LimitReader(z, maxCLIBody+1))
		if err != nil || len(decoded) > maxCLIBody {
			return nil, nil, errors.New("body_limit")
		}
		return raw, decoded, nil
	}
	return nil, nil, errors.New("unsupported_encoding")
}

func inspectCLIBody(media string, data []byte, session *mask.Session) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	if media == "application/x-json-stream" || media == "application/x-ndjson" {
		for i, line := range bytes.Split(data, []byte("\n")) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			value, ok := decodeJSON(line)
			if !ok {
				return fmt.Sprintf("line %d", i), errors.New("unsupported_content")
			}
			if where, kind, err := exposedStrings(value, session, "$"); where != "" || err != nil {
				return fmt.Sprintf("line[].%s %s", strings.TrimPrefix(keyPath(where), "$."), strings.ToLower(kind)), errOrExposed(err)
			}
		}
		return "", nil
	}
	value, ok := decodeJSON(data)
	if !ok {
		return "non-JSON request body", errors.New("unsupported_content")
	}
	where, kind, err := exposedStrings(value, session, "$")
	if where != "" || err != nil {
		return "protected " + strings.ToLower(kind) + " value at " + keyPath(where), errOrExposed(err)
	}
	return "", nil
}

func errOrExposed(err error) error {
	if err != nil {
		return err
	}
	return protocol.ErrExposed
}

func (a *App) CLI(r *http.Request, t tunnel) (*http.Request, *http.Response) {
	if a.protectionOff.Load() {
		return r, nil
	}
	adapter := t.adapter
	integration := cliIntegrations[adapter]
	path := normalizedPath(r.URL.Path)
	a.detected.record(integration.Service, true, func(*Integration) {})
	request := Shape{Direction: "request", Method: r.Method, Path: path}
	block := func(code int, reason, note string) (*http.Request, *http.Response) {
		request.Note = note
		a.discovered.add(request)
		a.Blocked.Add(1)
		a.detected.record(integration.Service, true, func(i *Integration) { i.Blocked++ })
		a.cliSessions.record(t.session, integration.Service, func(s *CLISession) { s.Blocked++ })
		a.Events.Add(Event{Phase: "request", Adapter: adapter, Route: path, Status: code, Reason: reason, Folder: a.cliSessions.folder(t.session)})
		return r, cliFailure(r, code, reason)
	}
	if r.Header.Get("Upgrade") != "" {
		return block(403, "upgrade_not_supported", "websocket upgrade requested")
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		if r.URL.RawQuery != "" {
			session, release, err := a.cliSession(t.session)
			if err != nil {
				return block(409, "session_expired_restart_cli", "session unavailable")
			}
			where, kind, err := exposedQuery(r.URL.Query(), session)
			release()
			if where != "" || err != nil {
				code, reason := webRejection(errOrExposed(err))
				if err == nil {
					reason = "sensitive_value_not_masked_yet"
				}
				return block(code, reason, "protected "+strings.ToLower(kind)+" value in query parameter "+where)
			}
		}
		a.discovered.add(request)
		return r, nil
	}
	media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	request.Media = media
	raw, data, err := readCLIBody(r)
	if err != nil {
		return block(413, err.Error(), "request body rejected: "+err.Error())
	}
	if value, ok := decodeJSON(data); ok {
		request.Body = skeleton(value, "", "", 0)
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/v1/messages"):
		a.discovered.add(request)
		return r, a.cliModel(r, t, data, anthropicFormat)
	case strings.HasSuffix(r.URL.Path, "/chat/completions"):
		a.discovered.add(request)
		return r, a.cliModel(r, t, data, chatFormat)
	case strings.HasSuffix(r.URL.Path, "/responses"):
		a.discovered.add(request)
		return r, a.cliModel(r, t, data, responsesFormat)
	}
	session, release, err := a.cliSession(t.session)
	if err != nil {
		return block(409, "session_expired_restart_cli", "session unavailable")
	}
	defer release()
	body := raw
	if strings.HasPrefix(r.URL.Path, "/mcp/") && media == "application/json" {
		masked, counts, err := protocol.MaskToolArguments(data, session)
		if err != nil {
			code, reason := webRejection(err)
			return block(code, reason, "tool call arguments rejected")
		}
		if counts.Total() > 0 {
			a.cliSessions.record(t.session, integration.Service, func(s *CLISession) { s.Masked += uint64(counts.New) })
		}
		if counts.New > 0 {
			event := Event{Phase: "request", Adapter: adapter, Route: path, Status: 200, Reason: "tool_arguments_masked", InputBytes: len(data), MaskedBytes: len(masked)}
			a.setMatches(&event, counts)
			a.Events.Add(event)
		}
		body = masked
	} else if note, err := inspectCLIBody(media, data, session); err != nil {
		code, reason := webRejection(err)
		if errors.Is(err, protocol.ErrExposed) {
			reason = "sensitive_value_not_masked_yet"
		}
		return block(code, reason, note)
	}
	a.discovered.add(request)
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Del("Content-Length")
	return r, nil
}

type modelFormat struct {
	mask          func([]byte, *mask.Session, string, []string) ([]byte, mask.Counts, error)
	restore       func([]byte, *mask.Session) ([]byte, map[string]bool, error)
	restoreStream func(io.Reader, io.Writer, *mask.Session, int) (protocol.StreamStats, error)
	inputTokens   func([]byte) int
	newTurn       func([]byte) bool
	refusedTools  func([]byte) map[string][]string
}

var (
	anthropicFormat = modelFormat{protocol.MaskAgentMessages, protocol.RestoreAgentMessages, protocol.RestoreAgentStream, protocol.AgentInputTokens, protocol.AgentNewTurn, protocol.AgentRefusedTools}
	chatFormat      = modelFormat{protocol.MaskOpenAIChat, protocol.RestoreOpenAI, protocol.RestoreOpenAIChatStream, protocol.OpenAIInputTokens, protocol.OpenAIChatNewTurn, nil}
	responsesFormat = modelFormat{protocol.MaskOpenAIResponses, protocol.RestoreOpenAI, protocol.RestoreOpenAIResponsesStream, protocol.OpenAIInputTokens, protocol.OpenAIResponsesNewTurn, nil}
)

func (a *App) cliModel(r *http.Request, t tunnel, data []byte, format modelFormat) (result *http.Response) {
	newTurn := format.newTurn(data)
	prompt := r.Header.Get("X-Claude-Code-Prompt-Id")
	if len(prompt) > 128 {
		prompt = ""
	}
	integration := cliIntegrations[t.adapter]
	path := normalizedPath(r.URL.Path)
	requestStarted := time.Now()
	event := Event{Phase: "request", Adapter: t.adapter, Route: path, Reason: "masked_and_restored", InputBytes: len(data)}
	emit := true
	defer func() {
		if !emit {
			return
		}
		event.Status = result.StatusCode
		event.ElapsedMS = milliseconds(time.Since(requestStarted))
		a.logTurn(event, t.session, prompt, newTurn)
	}()
	reject := func(code int, reason string) *http.Response {
		event.Reason = reason
		a.Blocked.Add(1)
		a.detected.record(integration.Service, true, func(i *Integration) { i.Blocked++ })
		a.cliSessions.record(t.session, integration.Service, func(s *CLISession) { s.Blocked++ })
		return cliFailure(r, code, reason)
	}
	if !a.acquireSlot(r.Context()) {
		return reject(503, "concurrency_limit")
	}
	ctx, cancel := context.WithTimeout(r.Context(), cliModelTimeout)
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
	session, release, err := a.cliSession(t.session)
	if err != nil {
		return reject(409, "session_expired_restart_cli")
	}
	releaseSession = release
	masked, counts, err := format.mask(data, session, t.authority, clientIdentifiers(r.Header))
	if err != nil {
		code, reason := webRejection(err)
		if strings.Contains(err.Error(), "content block") {
			a.discovered.add(Shape{Direction: "request", Method: r.Method, Path: path, Note: strings.TrimPrefix(err.Error(), protocol.ErrFormat.Error()+": ")})
		}
		return reject(code, reason)
	}
	if format.refusedTools != nil && len(a.Policy().BlockedPaths) > 0 {
		counts = counts.Add(session.RefusedAccess(format.refusedTools(data)))
	}
	event.MaskedBytes = len(masked)
	a.setMatches(&event, counts)
	event.MaskMS = milliseconds(time.Since(requestStarted))
	for kind := range counts.UnknownBlocks {
		a.discovered.add(Shape{Direction: "request", Method: r.Method, Path: path, Note: "content block " + kind + " masked generically"})
		a.Events.Add(Event{Phase: "request", Adapter: t.adapter, Route: path, Status: 200, Reason: "new_content_type_masked", Folder: a.cliSessions.folder(t.session)})
	}
	out := r.Clone(ctx)
	out.RequestURI = ""
	out.Body = io.NopCloser(bytes.NewReader(masked))
	out.ContentLength = int64(len(masked))
	out.TransferEncoding = nil
	for _, name := range []string{"Content-Length", "Content-Encoding", "Accept-Encoding", "Connection", "Keep-Alive", "Proxy-Connection", "Proxy-Authorization", "Te", "Trailer", "Upgrade"} {
		out.Header.Del(name)
	}
	out.Header.Set("Accept-Encoding", "identity")
	prepared := time.Since(requestStarted)
	event.OverheadMS = milliseconds(prepared)
	response, err := a.ModelTransport.RoundTrip(out)
	if err != nil {
		return reject(502, "upstream_TLS_or_connection_failed")
	}
	a.detected.record(integration.Service, true, func(i *Integration) { i.Requests++ })
	a.cliSessions.record(t.session, integration.Service, func(s *CLISession) {
		s.Prompts++
		s.Masked += uint64(counts.New)
		s.TokenOverhead += int64(counts.TokenOverhead)
	})
	a.recordMasked(counts)
	media, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode < 200 || response.StatusCode > 299 {
		event.Reason = "passed_without_restore"
		response.Body = &managedBody{ReadCloser: response.Body, cleanup: cleanup}
		success = true
		return response
	}
	switch response.Header.Get("Content-Encoding") {
	case "", "identity":
	case "gzip":
		decoded, err := gzip.NewReader(response.Body)
		if err != nil {
			response.Body.Close()
			return reject(502, "compressed_upstream_response_not_supported")
		}
		response.Body = struct {
			io.Reader
			io.Closer
		}{decoded, response.Body}
		response.Header.Del("Content-Encoding")
	default:
		response.Body.Close()
		return reject(502, "compressed_upstream_response_not_supported")
	}
	if media == "" && requestsStream(data) {
		media = "text/event-stream"
	}
	if media != "text/event-stream" {
		body, err := io.ReadAll(io.LimitReader(response.Body, maxCLIBody+1))
		response.Body.Close()
		if err != nil || len(body) > maxCLIBody {
			return reject(502, "response_limit")
		}
		restoreStarted := time.Now()
		restored, kept := body, map[string]bool(nil)
		if len(bytes.TrimSpace(body)) > 0 {
			if restored, kept, err = format.restore(body, session); err != nil {
				return reject(502, "unsupported_response_content")
			}
		}
		if event.InputTokens = format.inputTokens(body); event.InputTokens > 0 {
			a.cliSessions.record(t.session, integration.Service, func(s *CLISession) { s.InputTokens += int64(event.InputTokens) })
		}
		event.OverheadMS = milliseconds(prepared + time.Since(restoreStarted))
		a.checks.time(event.OverheadMS)
		leftovers := session.Leftovers()
		leftovers.Write(restored)
		a.reportUnrestored(t, path, leftovers.Count(kept))
		response.Body = &managedBody{ReadCloser: io.NopCloser(bytes.NewReader(restored)), cleanup: cleanup}
		response.ContentLength = int64(len(restored))
		response.Header.Del("Content-Length")
		success = true
		return response
	}
	upstream := response.Body
	reader, writer := io.Pipe()
	emit = false
	streamEvent := event
	streamEvent.Stream, streamEvent.Status = true, 200
	go func() {
		defer upstream.Close()
		in := &timedReader{Reader: upstream}
		out := &timedWriter{Writer: writer}
		restoreStarted := time.Now()
		leftovers := session.Leftovers()
		stats, err := format.restoreStream(in, io.MultiWriter(out, leftovers), session, 4<<20)
		processing := time.Since(restoreStarted) - in.waited - out.waited
		if streamEvent.InputTokens = stats.InputTokens; stats.InputTokens > 0 {
			a.cliSessions.record(t.session, integration.Service, func(s *CLISession) { s.InputTokens += int64(stats.InputTokens) })
		}
		if err != nil && !stats.Completed {
			streamEvent.Status, streamEvent.Reason = 502, "stream_interrupted"
			protocol.WriteEvent(out, streamFailure(r))
		}
		if stats.Withheld > 0 {
			a.Events.Add(Event{Phase: "response", Adapter: t.adapter, Route: path, Status: 200, Reason: "secret_kept_masked_in_tool", Folder: a.cliSessions.folder(t.session)})
		}
		a.reportUnrestored(t, path, leftovers.Count(stats.Kept))
		streamEvent.OverheadMS = milliseconds(prepared + processing)
		streamEvent.ElapsedMS = milliseconds(time.Since(requestStarted))
		if streamEvent.Status < 400 {
			a.checks.time(streamEvent.OverheadMS)
		}
		a.logTurn(streamEvent, t.session, prompt, newTurn)
		writer.CloseWithError(err)
	}()
	response.Body = &managedBody{ReadCloser: reader, cleanup: func() { cleanup(); upstream.Close() }}
	response.ContentLength = -1
	response.Header.Del("Content-Length")
	success = true
	return response
}

// reportUnrestored records an answer that still holds placeholders the model mangled, so the
// user knows some values in it did not come back.
func (a *App) reportUnrestored(t tunnel, path string, count int) {
	if count > 0 {
		a.Events.Add(Event{Phase: "response", Adapter: t.adapter, Route: path, Status: 200, Reason: "placeholders_not_restored", Unrestored: count, Folder: a.cliSessions.folder(t.session)})
	}
}

// requestsStream reports a request that asked for a streamed answer; the Codex backend sends
// its SSE stream without a Content-Type header.
func requestsStream(data []byte) bool {
	var request struct {
		Stream bool `json:"stream"`
	}
	return json.Unmarshal(data, &request) == nil && request.Stream
}

func (a *App) observeCLIResponse(response *http.Response, adapter string) *http.Response {
	if response == nil || response.Request == nil || response.Request.Method != http.MethodPost {
		return response
	}
	path := normalizedPath(response.Request.URL.Path)
	if !a.discovered.sample(path) {
		return response
	}
	media, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	base := Shape{Direction: "response", Path: path, Media: media, Status: response.StatusCode}
	if encoding := response.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		base.Note = "compressed response: " + encoding
		a.discovered.add(base)
		return response
	}
	upstream := response.Body
	reader, writer := io.Pipe()
	go func() {
		defer upstream.Close()
		tee := io.TeeReader(upstream, writer)
		if media == "text/event-stream" {
			recordSSE(tee, base, a.discovered.add)
		} else {
			data, _ := io.ReadAll(io.LimitReader(tee, 1<<20))
			shape := base
			if value, ok := decodeJSON(data); ok {
				shape.Body = skeleton(value, "", "", 0)
			}
			a.discovered.add(shape)
		}
		_, err := io.Copy(writer, upstream)
		writer.CloseWithError(err)
	}()
	response.Body = reader
	return response
}

func recordSSE(input io.Reader, base Shape, add func(Shape)) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	name, data, events := "", "", 0
	for scanner.Scan() && events < 400 {
		line := scanner.Text()
		if line == "" {
			if data != "" {
				shape := base
				shape.Event = name
				if value, ok := decodeJSON([]byte(data)); ok {
					shape.Body = skeleton(value, "", "", 0)
				} else if data == "[DONE]" {
					shape.Note = "[DONE]"
				} else {
					shape.Note = "non-JSON data"
				}
				add(shape)
				events++
			}
			name, data = "", ""
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			if enumValue.MatchString(value) {
				name = value
			} else {
				name = ":event"
			}
		case "data":
			data += value
		}
	}
}
