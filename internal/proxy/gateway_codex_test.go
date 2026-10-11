package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"phantomprompt/internal/mask"
)

type codexUpstream struct {
	mu      sync.Mutex
	bodies  []string
	paths   []string
	headers []http.Header
}

func (u *codexUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	u.mu.Lock()
	u.bodies, u.paths, u.headers = append(u.bodies, string(body)), append(u.paths, r.Host+r.URL.Path), append(u.headers, r.Header.Clone())
	u.mu.Unlock()
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"models":[{"slug":"synthetic-model"}]}`)
		return
	}
	token := regexp.MustCompile(`\[PP_VIN_[a-z2-9]{6}\]`).FindString(string(body))
	w.Header()["Content-Type"] = nil
	io.WriteString(w, "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"sequence_number\":0,\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_1\"}}\n\n")
	io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":\"VIN is "+token+"\"}\n\n")
	io.WriteString(w, "event: response.output_text.done\ndata: {\"type\":\"response.output_text.done\",\"sequence_number\":2,\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"text\":\"VIN is "+token+"\"}\n\n")
	io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":3,\"response\":{\"usage\":{\"input_tokens\":9}}}\n\n")
}

func codexFixture(t *testing.T) (*App, http.Handler, *codexUpstream) {
	t.Helper()
	ca, err := NewAuthority("Synthetic OpenAI CA")
	if err != nil {
		t.Fatal(err)
	}
	var certificates []tls.Certificate
	for _, host := range []string{"chatgpt.com", "api.openai.com"} {
		cert, err := ca.Issue(host)
		if err != nil {
			t.Fatal(err)
		}
		certificates = append(certificates, cert)
	}
	upstream := &codexUpstream{}
	listener := newPipeListener()
	server := &http.Server{Handler: upstream}
	go server.Serve(tls.NewListener(listener, &tls.Config{Certificates: certificates, MinVersion: tls.VersionTLS12}))
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca.PEM)
	app, err := New(Config{Integrations: []string{CodexCLI}}, localSecret, Options{Roots: roots, DialContext: listener.Dial})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close(); server.Close(); listener.Close() })
	return app, app.Gateway("127.0.0.1:18081"), upstream
}

const codexRequest = `{"model":"m","instructions":"Agent rules","stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Check TEST0000000000001"}]}]}`

func TestCodexGatewayMasksAndRestores(t *testing.T) {
	app, handler, upstream := codexFixture(t)
	session, secret, err := app.CreateCLISession("codex", 0)
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimPrefix(CodexBaseURL("127.0.0.1:18081", session, secret, "chatgpt"), "http://127.0.0.1:18081")
	header := http.Header{"Authorization": {"Bearer synthetic-chatgpt-token"}, "Chatgpt-Account-Id": {"synthetic-account"}, "Session-Id": {"00000000-0000-4000-8000-000000000001"}}
	w := gatewayCall(handler, base+"/responses", codexRequest, header)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "VIN is TEST0000000000001") || strings.Contains(w.Body.String(), "[PP_VIN_") {
		t.Fatalf("codex response %d: %s", w.Code, w.Body.String())
	}
	if upstream.paths[0] != "chatgpt.com/backend-api/codex/responses" || strings.Contains(upstream.bodies[0], "TEST0000000000001") || !strings.Contains(upstream.bodies[0], "[PP_VIN_") {
		t.Fatalf("upstream %s got %s", upstream.paths[0], upstream.bodies[0])
	}
	if upstream.headers[0].Get("Authorization") != "Bearer synthetic-chatgpt-token" || upstream.headers[0].Get("Chatgpt-Account-Id") != "synthetic-account" {
		t.Fatalf("login headers not forwarded: %v", upstream.headers[0])
	}
	request := httptest.NewRequest(http.MethodGet, base+"/models", nil)
	request.Host = "127.0.0.1:18081"
	models := httptest.NewRecorder()
	handler.ServeHTTP(models, request)
	if models.Code != 200 || !strings.Contains(models.Body.String(), "synthetic-model") || upstream.paths[1] != "chatgpt.com/backend-api/codex/models" {
		t.Fatalf("models: %d %s", models.Code, models.Body.String())
	}
	apiBase := strings.TrimPrefix(CodexBaseURL("127.0.0.1:18081", session, secret, "openai"), "http://127.0.0.1:18081")
	if w := gatewayCall(handler, apiBase+"/responses", codexRequest, nil); w.Code != 200 || upstream.paths[2] != "api.openai.com/v1/responses" {
		t.Fatalf("API-key route: %d %v", w.Code, upstream.paths)
	}
	if gatewayCall(handler, base[:len(base)-len("backend-api/codex")]+"elsewhere/responses", codexRequest, nil).Code != http.StatusNotFound {
		t.Fatal("unknown upstream accepted")
	}
}

func TestCodexGatewayKeySeparatesCodexSessions(t *testing.T) {
	app, handler, upstream := codexFixture(t)
	app.SetGatewayKey("synthetic-gateway-key-0000000000000000")
	base := strings.TrimPrefix(CodexKeyURL("127.0.0.1:18081", "synthetic-gateway-key-0000000000000000", "chatgpt"), "http://127.0.0.1:18081")
	for _, codex := range []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"} {
		if w := gatewayCall(handler, base+"/responses", codexRequest, http.Header{"Session-Id": {codex}}); w.Code != 200 {
			t.Fatalf("key route: %d %s", w.Code, w.Body.String())
		}
	}
	vin := regexp.MustCompile(`\[PP_VIN_[a-z2-9]{6}\]`)
	first, again, other := vin.FindString(upstream.bodies[0]), vin.FindString(upstream.bodies[1]), vin.FindString(upstream.bodies[2])
	if first == "" || first != again || first == other {
		t.Fatalf("placeholders %q %q %q: one map per Codex session expected", first, again, other)
	}
}

func TestCodexGatewayForwardsOnlyKnownRoutes(t *testing.T) {
	app, handler, upstream := codexFixture(t)
	session, secret, err := app.CreateCLISession("codex", 0)
	if err != nil {
		t.Fatal(err)
	}
	cliSession, release, err := app.cliSession(session)
	if err != nil {
		t.Fatal(err)
	}
	password := "Zq8" + "vLm2pX7!"
	if _, _, err := cliSession.Mask([]string{"password: " + password}); err != nil {
		t.Fatal(err)
	}
	release()
	base := strings.TrimPrefix(CodexBaseURL("127.0.0.1:18081", session, secret, "chatgpt"), "http://127.0.0.1:18081")
	apiBase := strings.TrimPrefix(CodexBaseURL("127.0.0.1:18081", session, secret, "openai"), "http://127.0.0.1:18081")
	call := func(method, path, body string) int {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Host = "127.0.0.1:18081"
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, apiBase + "/embeddings", `{"input":"` + password + `"}`},
		{http.MethodPost, apiBase + "/images/generations", `{"prompt":"` + password + `"}`},
		{http.MethodPost, base + "/responses/compact", codexRequest},
		{http.MethodPut, base + "/responses", codexRequest},
		{http.MethodGet, base + "/responses", ""},
		{http.MethodPost, base + "/models", `{"input":"x"}`},
		{http.MethodGet, base + "/files", ""},
	} {
		if code := call(c.method, c.path, c.body); code != http.StatusNotFound {
			t.Fatalf("%s %s: %d, want 404", c.method, c.path, code)
		}
	}
	if code := call(http.MethodGet, base+"/models?q="+password, ""); code != http.StatusForbidden {
		t.Fatalf("a known password in the query: %d, want 403", code)
	}
	if len(upstream.paths) != 0 {
		t.Fatalf("rejected requests reached the upstream: %v", upstream.paths)
	}
	if code := call(http.MethodGet, base+"/models?client_version=0.162.0", ""); code != 200 || len(upstream.paths) != 1 {
		t.Fatalf("models list: %d, upstream %v", code, upstream.paths)
	}
}

func TestAuxiliaryRequestsAreInspectedCompletely(t *testing.T) {
	policy := mask.DefaultPolicy()
	policy.LenientFields = true
	policy.CustomFields = []string{"accountNumber"}
	session := mask.NewSession(4096, 1<<20, policy)
	password := "Zq8" + "vLm2pX7!"
	if _, _, err := session.Mask([]string{"password: " + password, "accountNumber: 4111" + "222233334444"}); err != nil {
		t.Fatal(err)
	}
	github := "ghp" + "_" + strings.Repeat("Ab1", 12)
	for name, body := range map[string]string{
		"known secret as a single string": `{"input":"` + password + `"}`,
		"new secret of a known format":    `{"input":"` + github + `"}`,
		"known value as a number":         `{"input":4111` + `222233334444}`,
		"nested JSON":                     `{"input":"{\"a\":[\"` + password + `\"]}"}`,
		"a second JSON document":          `{"model":"m"} {"input":"` + password + `"}`,
	} {
		if _, err := inspectCLIBody("application/json", []byte(body), session); err == nil {
			t.Fatalf("%s: not rejected", name)
		}
	}
	for _, body := range []string{`{"model":"gpt-5","stream":true,"n":3}`, `{"input":"ordinary words only"}`} {
		if note, err := inspectCLIBody("application/json", []byte(body), session); err != nil {
			t.Fatalf("%s rejected: %s %v", body, note, err)
		}
	}
}
