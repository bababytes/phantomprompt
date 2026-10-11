package demo

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"phantomprompt/internal/mask"
	"phantomprompt/internal/proxy"
)

func TestDemoRulesAndCustomSession(t *testing.T) {
	const access = "synthetic-access-token-00000000000000"
	app, err := proxy.New(proxy.Config{Rules: []proxy.Rule{{Origin: Origin, Path: "/echo", Adapter: "demo"}}, SensitiveFields: []string{"employeeId"}}, access, proxy.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	handler, closeClient, err := Handler(app, "127.0.0.1:18080", "127.0.0.1:18090", access, PanelOptions{Logins: NewLogins()})
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Host = "127.0.0.1:18090"
		r.Header.Set(proxy.AccessHeader, access)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := request(http.MethodGet, "/rules", "")
	var rules struct {
		Defaults []string              `json:"defaults"`
		Secrets  []mask.SecretDetector `json:"secrets"`
		Patterns map[string]string     `json:"patterns"`
		Policy   mask.Policy           `json:"policy"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &rules) != nil || !rules.Policy.GUID || !rules.Policy.Fields || len(rules.Policy.CustomFields) != 1 || len(rules.Secrets) == 0 || len(rules.Patterns) != 4 {
		t.Fatal("configured rules were not exposed to the demo")
	}
	w = request(http.MethodPost, "/sessions", `{"vin":true,"guid":true,"email":true,"fields":true,"custom_fields":["employeeId"]}`)
	var created struct {
		Session string `json:"session"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &created) != nil {
		t.Fatal("custom session could not be created")
	}
	session, release, err := app.Sessions.Acquire(created.Session, Origin+"|demo")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	out, counts, err := session.Mask([]string{"employeeId: TEST-EMPLOYEE"})
	if err != nil || counts.Fields != 1 || strings.Contains(out[0], "TEST-EMPLOYEE") {
		t.Fatal("custom field policy did not reach the session")
	}
	w = request(http.MethodGet, "/", "")
	bundle := request(http.MethodGet, "/assets/app.js", "").Body.String()
	if w.Code != 200 || !strings.Contains(bundle, "guid") || !strings.Contains(bundle, "custom-fields") || !strings.Contains(w.Body.String(), `data-author="`+Author+`"`) {
		t.Fatal("demo controls or author missing")
	}
}

func TestWorkspaceRegexAndHistory(t *testing.T) {
	const access = "synthetic-access-token-00000000000000"
	app, err := proxy.New(proxy.Config{Rules: []proxy.Rule{{Origin: Origin, Path: "/echo", Adapter: "demo"}}}, access, proxy.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	handler, closeClient, err := Handler(app, "127.0.0.1:18080", "127.0.0.1:18090", access, PanelOptions{Logins: NewLogins()})
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	request := func(method, path, body, auth, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Host = "127.0.0.1:18090"
		r.Header.Set(proxy.AccessHeader, auth)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := request("POST", "/api/regex/generate", `{"text":"SECRET.ORION+","case_sensitive":false}`, access, "")
	var rule mask.RegexRule
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &rule) != nil || rule.Pattern != `(?i:SECRET\.ORION\+)` {
		t.Fatal("generator did not escape a literal")
	}
	w = request("POST", "/api/regex/generate", `{"text":"abdsfsdb-ksabsdbvf-ahsgd","case_sensitive":false,"shape":true}`, access, "")
	var shape struct{ Pattern, Description string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &shape) != nil || !regexp.MustCompile(shape.Pattern).MatchString("qwertyui-asdfghjkl-zxcvb") || !strings.HasPrefix(shape.Description, "8 letters") {
		t.Fatalf("shape generator: %d %s", w.Code, w.Body.String())
	}
	if w = request("POST", "/api/regex/generate", `{"text":"hello","shape":true}`, access, ""); w.Code != 422 || !strings.Contains(w.Body.String(), "no layout") {
		t.Fatalf("shape without a layout: %d %s", w.Code, w.Body.String())
	}
	body, _ := json.Marshal(map[string]any{"text": "SECRET.ORION+ secret.orion+", "policy": mask.Policy{CustomRules: []mask.RegexRule{rule}}})
	w = request("POST", "/api/regex/test", string(body), access, "")
	var result struct {
		Masked   string
		Restored string
		Total    int
		Matches  mask.Counts
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Total != 2 || result.Matches.Custom != 2 || result.Restored != "SECRET.ORION+ secret.orion+" || strings.Contains(result.Masked, "ORION") {
		t.Fatal("preview did not use the reversible engine")
	}
	if app.Forwarded.Load() != 0 || len(app.Events.Snapshot()) != 0 {
		t.Fatal("preview polluted proxy counters or logs")
	}
	for _, path := range []string{"/api/regex/generate", "/api/regex/test", "/events"} {
		if request("POST", path, "{}", "", "").Code != 401 || request("POST", path, "{}", access, "https://external.test").Code != 403 {
			t.Fatal("workspace endpoint missed authentication or origin guard")
		}
	}
	if request("POST", "/api/regex/test", `{"text":"sample","policy":{"custom_rules":[{"pattern":"("}]}}`, access, "").Code != 422 {
		t.Fatal("invalid custom regex accepted")
	}
	app.Events.Add(proxy.Event{Reason: "accepted", Status: 200})
	if w = request("GET", "/events", "", access, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "accepted") {
		t.Fatal("diagnostic events unavailable")
	}
	if request("DELETE", "/events", "", access, "").Code != 204 || len(app.Events.Snapshot()) != 0 {
		t.Fatal("diagnostic history could not be cleared")
	}
	w = request("GET", "/assets/app.js", "", access, "")
	for _, id := range []string{"demo-window", "generate-regex", "test-regex", "event-rows", "total-custom"} {
		if !strings.Contains(w.Body.String(), id) {
			t.Fatal("workspace control missing")
		}
	}
}

func TestProtectedTerminalLauncher(t *testing.T) {
	const access = "synthetic-access-token-00000000000000"
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, "work", "it's here"), 0700)
	os.WriteFile(filepath.Join(home, "notes.txt"), []byte("x"), 0600)
	app, err := proxy.New(proxy.Config{Rules: []proxy.Rule{{Origin: Origin, Path: "/echo", Adapter: "demo"}}, Integrations: []string{proxy.ClaudeCode}}, access, proxy.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	var opened []string
	var failure error
	for _, launcher := range []func(string) error{nil, func(folder string) error { opened = append(opened, folder); return failure }} {
		handler, closeClient, err := Handler(app, "127.0.0.1:18080", "127.0.0.1:18090", access, PanelOptions{OpenTerminal: launcher, Logins: NewLogins()})
		if err != nil {
			t.Fatal(err)
		}
		defer closeClient()
		request := func(method, path, auth, body string) *httptest.ResponseRecorder {
			r := httptest.NewRequest(method, path, strings.NewReader(body))
			r.Host = "127.0.0.1:18090"
			r.Header.Set(proxy.AccessHeader, auth)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			return w
		}
		page := request(http.MethodGet, "/", access, "").Body.String()
		bundle := request(http.MethodGet, "/assets/app.js", access, "").Body.String()
		if strings.Contains(bundle, "open-gemini") || strings.Contains(bundle, "proxy-password") || request(http.MethodPost, "/api/browser", access, "").Code != 404 {
			t.Fatal("the protected Gemini window is still offered")
		}
		if launcher == nil {
			if request(http.MethodPost, "/api/terminal", access, `{"path":"~"}`).Code != 404 || !strings.Contains(page, `data-terminal="false"`) {
				t.Fatal("terminal launcher exposed where it cannot run")
			}
			continue
		}
		if !strings.Contains(page, `data-terminal="true"`) || !strings.Contains(bundle, "open-terminal") || !strings.Contains(bundle, "integration-rows") || !strings.Contains(bundle, "cli-rows") || !strings.Contains(bundle, "/api/terminal/close") {
			t.Fatal("terminal controls missing")
		}
		if request(http.MethodPost, "/api/terminal", "", `{"path":"~"}`).Code != 401 || len(opened) != 0 {
			t.Fatal("unauthenticated request opened a terminal")
		}
		for _, bad := range []struct {
			body string
			code int
		}{{`{"path":"work"}`, 400}, {`{"path":"~/missing"}`, 404}, {`{"path":"~/notes.txt"}`, 400}, {`not json`, 400}} {
			if code := request(http.MethodPost, "/api/terminal", access, bad.body).Code; code != bad.code {
				t.Fatalf("%s: HTTP %d, want %d", bad.body, code, bad.code)
			}
		}
		if len(opened) != 0 {
			t.Fatalf("invalid folders were opened: %q", opened)
		}
		w := request(http.MethodPost, "/api/terminal", access, `{"path":"~/work/it's here"}`)
		if w.Code != 200 || len(opened) != 1 || opened[0] != filepath.Join(home, "work", "it's here") || !strings.Contains(w.Body.String(), `"~/work/it's here"`) {
			t.Fatalf("open terminal: %d %s %q", w.Code, w.Body, opened)
		}
		failure = errors.New("synthetic missing Terminal")
		if request(http.MethodPost, "/api/terminal", access, `{"path":"~"}`).Code != 503 {
			t.Fatal("launcher failure not reported")
		}
	}
	if _, _, err := app.CreateCLISession("claude", 42); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/status", nil)
	r.Header.Set(proxy.AccessHeader, access)
	app.Control(w, r)
	var status struct {
		Integrations []proxy.Integration `json:"integrations"`
		CLISessions  []proxy.CLISession  `json:"cli_sessions"`
	}
	if json.Unmarshal(w.Body.Bytes(), &status) != nil || len(status.Integrations) != 0 {
		t.Fatalf("services listed before any traffic: %s", w.Body)
	}
	if len(status.CLISessions) != 1 || status.CLISessions[0].Label != "claude" || !status.CLISessions[0].Active {
		t.Fatalf("protected CLI session missing from status: %s", w.Body)
	}
}

func TestPanelControlEndpoints(t *testing.T) {
	const access = "synthetic-access-token-00000000000000"
	app, err := proxy.New(proxy.Config{Rules: []proxy.Rule{{Origin: Origin, Path: "/echo", Adapter: "demo"}}, Integrations: []string{proxy.ClaudeCode}}, access, proxy.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	handler, closeClient, err := Handler(app, "127.0.0.1:18080", "127.0.0.1:18090", access, PanelOptions{Logins: NewLogins()})
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	request := func(method, path, body, auth string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Host = "127.0.0.1:18090"
		r.Header.Set(proxy.AccessHeader, auth)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if request(http.MethodPost, "/api/protection", `{"on":false}`, "").Code != 401 {
		t.Fatal("protection toggle must require authentication")
	}
	if app.Protection() != true {
		t.Fatal("protection should start on")
	}
	if request(http.MethodPost, "/api/protection", `{"on":false}`, access).Code != 204 || app.Protection() {
		t.Fatal("protection was not turned off")
	}
	if request(http.MethodPost, "/api/protection", `{"on":true}`, access).Code != 204 || !app.Protection() {
		t.Fatal("protection was not turned on")
	}
	if _, _, err := app.CreateCLISession("zsh", 4242); err != nil {
		t.Fatal(err)
	}
	terminal := `{"id":"` + app.CLISessions()[0].ID + `"}`
	if request(http.MethodPost, "/api/terminal/close", terminal, "").Code != 401 || len(app.CLISessions()) != 1 {
		t.Fatal("closing a terminal must require authentication")
	}
	for body, code := range map[string]int{`not json`: 400, `{"id":"unknown"}`: 404} {
		if got := request(http.MethodPost, "/api/terminal/close", body, access).Code; got != code {
			t.Fatalf("%s: HTTP %d, want %d", body, got, code)
		}
	}
	if request(http.MethodPost, "/api/terminal/close", terminal, access).Code != 204 || len(app.CLISessions()) != 0 {
		t.Fatal("terminal not closed")
	}
	if request(http.MethodPost, "/api/terminal/close", terminal, access).Code != 404 {
		t.Fatal("closed terminal closed again")
	}
	if request(http.MethodPost, "/api/shutdown", "", "").Code != 401 {
		t.Fatal("shutdown must require authentication")
	}
	if request(http.MethodPost, "/api/shutdown", "", access).Code != 204 {
		t.Fatal("shutdown request rejected")
	}
	select {
	case <-app.ShutdownRequested():
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown was not requested")
	}
	page := request(http.MethodGet, "/", "", access).Body.String()
	bundle := request(http.MethodGet, "/assets/app.js", "", access).Body.String()
	if !strings.Contains(page, `<script type="module" src="/assets/app.js">`) || !strings.Contains(page, `id="app"`) {
		t.Fatal("panel page does not load the bundled app")
	}
	for _, id := range []string{"protection", "stop-app", "protection-banner"} {
		if !strings.Contains(bundle, id) {
			t.Fatalf("control %q missing from the panel bundle", id)
		}
	}
}

func TestPanelAssets(t *testing.T) {
	const access = "synthetic-access-token-00000000000000"
	app, err := proxy.New(proxy.Config{Rules: []proxy.Rule{{Origin: Origin, Path: "/echo", Adapter: "demo"}}}, access, proxy.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	handler, closeClient, err := Handler(app, "127.0.0.1:18080", "127.0.0.1:18090", access, PanelOptions{Logins: NewLogins()})
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Host = "127.0.0.1:18090"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for path, media := range map[string]string{"/assets/app.js": "text/javascript", "/assets/app.css": "text/css"} {
		w := get(path)
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), media) || w.Body.Len() == 0 || !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") {
			t.Fatalf("%s: %d %q", path, w.Code, w.Header().Get("Content-Type"))
		}
	}
	for _, path := range []string{"/assets/", "/assets/missing.js", "/app.js", "/style.css", "/dist/assets/app.js", "/index.html"} {
		if code := get(path).Code; code != 404 {
			t.Fatalf("%s: status %d, want 404", path, code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	r.Host = "evil.test"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("foreign host served assets: %d", w.Code)
	}
}

func TestLiveRulesEndpoint(t *testing.T) {
	const access = "synthetic-access-token-00000000000000"
	rules := filepath.Join(t.TempDir(), "rules.json")
	app, err := proxy.New(proxy.Config{Rules: []proxy.Rule{{Origin: Origin, Path: "/echo", Adapter: "demo"}}}, access, proxy.Options{RulesFile: rules})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	handler, closeClient, err := Handler(app, "127.0.0.1:18080", "127.0.0.1:18090", access, PanelOptions{Logins: NewLogins()})
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	request := func(method, path, body, auth string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Host = "127.0.0.1:18090"
		r.Header.Set(proxy.AccessHeader, auth)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	update := `{"vin":true,"fields":true,"custom_fields":["employeeId"],"disabled_fields":["phone"]}`
	if request(http.MethodPut, "/api/rules", update, "").Code != 401 || len(app.Policy().CustomFields) != 0 {
		t.Fatal("rules changed without authentication")
	}
	for _, invalid := range []string{`{}`, `{"vin":true,"disabled_fields":["employeeId"]}`, `{"vin":true,"lenient":true}`, `{"vin":true,"custom_fields":["1x"]}`} {
		if code := request(http.MethodPut, "/api/rules", invalid, access).Code; code != 400 {
			t.Fatalf("%s: status %d", invalid, code)
		}
	}
	if w := request(http.MethodPut, "/api/rules", update, access); w.Code != 200 || !strings.Contains(w.Body.String(), `"disabled_fields":["phone"]`) {
		t.Fatalf("update: %d %s", w.Code, w.Body)
	}
	var current struct {
		Policy  mask.Policy `json:"policy"`
		Startup mask.Policy `json:"startup"`
	}
	if request(http.MethodGet, "/rules", "", "").Code != 401 {
		t.Fatal("rules readable without authentication")
	}
	if json.Unmarshal(request(http.MethodGet, "/rules", "", access).Body.Bytes(), &current) != nil || len(current.Policy.CustomFields) != 1 || len(current.Startup.CustomFields) != 0 {
		t.Fatalf("rules view %+v", current)
	}
	if _, err := os.Stat(rules); err != nil {
		t.Fatal("rules were not saved")
	}
	trusted := t.TempDir()
	if err := app.SetTrustedPath(trusted, true); err != nil {
		t.Fatal(err)
	}
	if request(http.MethodPut, "/api/rules", update, access).Code != 200 || !slices.Equal(app.Policy().TrustedPaths, []string{trusted}) {
		t.Fatalf("a panel without trusted folders dropped them: %v", app.Policy().TrustedPaths)
	}
	if request(http.MethodPut, "/api/rules", `{"vin":true,"trusted_paths":[]}`, access).Code != 200 || len(app.Policy().TrustedPaths) != 0 {
		t.Fatalf("a panel that sends trusted folders could not clear them: %v", app.Policy().TrustedPaths)
	}
}

func TestPanelRequiresOneTimeLogin(t *testing.T) {
	const access = "synthetic-access-token-00000000000000"
	const setup = "export ANTHROPIC_BASE_URL=http://127.0.0.1:47820/claude/k/synthetic-gateway-key"
	app, err := proxy.New(proxy.Config{Rules: []proxy.Rule{{Origin: Origin, Path: "/echo", Adapter: "demo"}}}, access, proxy.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	logins := NewLogins()
	clock := time.Now()
	logins.now = func() time.Time { return clock }
	handler, closeClient, err := Handler(app, "127.0.0.1:18080", "127.0.0.1:18090", access, PanelOptions{Logins: logins, ClaudeSetup: setup})
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	get := func(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Host = "127.0.0.1:18090"
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	secretFree := func(name string, w *httptest.ResponseRecorder) {
		t.Helper()
		if body := w.Body.String(); strings.Contains(body, access) || strings.Contains(body, "synthetic-gateway-key") {
			t.Fatalf("%s handed out the access token or the gateway key", name)
		}
	}
	page := get("/", nil)
	secretFree("the panel page without a login", page)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "/assets/app.js") {
		t.Fatalf("the panel page did not load the app: %d", page.Code)
	}

	link := logins.Issue()
	login := get(link, nil)
	if body := login.Body.String(); login.Code != http.StatusOK || !strings.Contains(body, access) || !strings.Contains(body, "synthetic-gateway-key") {
		t.Fatalf("login link did not open the panel: %d", login.Code)
	}
	if cookies := login.Header().Values("Set-Cookie"); len(cookies) != 0 {
		t.Fatalf("login set cookies that every local port would receive: %v", cookies)
	}
	if login.Header().Get("Cache-Control") != "no-store" || login.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("login page may be cached or leak its link: %v", login.Header())
	}
	replayed := get(link, nil)
	secretFree("a replayed login link", replayed)
	if replayed.Code != http.StatusForbidden {
		t.Fatalf("login link worked twice: %d", replayed.Code)
	}

	for _, cookie := range []*http.Cookie{{Name: "phantomprompt_panel", Value: "forged"}, {Name: "phantomprompt_panel", Value: access}} {
		secretFree("a request with an old panel cookie", get("/", cookie))
		if code := get("/rules", cookie).Code; code != http.StatusUnauthorized {
			t.Fatalf("a cookie opened the rules: %d", code)
		}
	}

	expired := logins.Issue()
	clock = clock.Add(loginLifetime + time.Second)
	if w := get(expired, nil); w.Code != http.StatusForbidden {
		t.Fatalf("an expired login link worked: %d", w.Code)
	}
	secretFree("an expired login link", get(expired, nil))
}

func TestClaudeSetupIsShownOnlyWhenConfigured(t *testing.T) {
	const access = "synthetic-access-token-00000000000000"
	app, err := proxy.New(proxy.Config{Rules: []proxy.Rule{{Origin: Origin, Path: "/echo", Adapter: "demo"}}}, access, proxy.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	for _, setup := range []string{"", "export ANTHROPIC_BASE_URL=http://127.0.0.1:47820/claude/k/key"} {
		handler, closeClient, err := Handler(app, "127.0.0.1:18080", "127.0.0.1:18090", access, PanelOptions{Logins: NewLogins(), ClaudeSetup: setup})
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Host = "127.0.0.1:18090"
		r.Header.Set(proxy.AccessHeader, access)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		closeClient()
		if !strings.Contains(w.Body.String(), `data-claude-setup="`+template.HTMLEscapeString(setup)+`"`) {
			t.Fatalf("setup %q not passed to the panel: %s", setup, w.Body.String())
		}
	}
}

func TestFolderBrowser(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, ".ssh"), 0700)
	os.WriteFile(filepath.Join(home, ".ssh", "id_synthetic"), []byte("SYNTHETIC-PRIVATE-KEY-CONTENT"), 0600)
	os.MkdirAll(filepath.Join(home, "Projects"), 0700)
	os.WriteFile(filepath.Join(home, "notes.txt"), []byte("note content"), 0600)
	os.Symlink(filepath.Join(home, "Projects"), filepath.Join(home, "link-to-projects"))
	const access = "synthetic-access-token-00000000000000"
	app, err := proxy.New(proxy.Config{Rules: []proxy.Rule{{Origin: Origin, Path: "/echo", Adapter: "demo"}}}, access, proxy.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	handler, closeClient, err := Handler(app, "127.0.0.1:18080", "127.0.0.1:18090", access, PanelOptions{Logins: NewLogins()})
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	list := func(path, auth string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"path": path})
		r := httptest.NewRequest(http.MethodPost, "/api/folders", strings.NewReader(string(body)))
		r.Host = "127.0.0.1:18090"
		r.Header.Set(proxy.AccessHeader, auth)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if list("~", "").Code != 401 {
		t.Fatal("folders listed without authentication")
	}
	var listing struct {
		Path, Parent string
		Entries      []struct {
			Name string
			Dir  bool
		}
	}
	w := list("~", access)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &listing) != nil {
		t.Fatalf("home listing: %d %s", w.Code, w.Body.String())
	}
	var names []string
	for _, entry := range listing.Entries {
		names = append(names, fmt.Sprintf("%s:%v", entry.Name, entry.Dir))
	}
	if listing.Path != "~" || listing.Parent == "" || strings.Join(names, ",") != ".ssh:true,Projects:true,link-to-projects:false,notes.txt:false" {
		t.Fatalf("listing %+v", listing)
	}
	w = list("~/.ssh", access)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"id_synthetic"`) || strings.Contains(w.Body.String(), "SYNTHETIC-PRIVATE-KEY-CONTENT") || !strings.Contains(w.Body.String(), `"path":"~/.ssh"`) {
		t.Fatalf("hidden folder listing: %s", w.Body.String())
	}
	for path, code := range map[string]int{"relative/path": 400, "~/missing": 404, "~/notes.txt": 400} {
		if got := list(path, access).Code; got != code {
			t.Fatalf("%s: status %d, want %d", path, got, code)
		}
	}
}

func TestPanelAnswersUnderItsLocalName(t *testing.T) {
	const access = "synthetic-access-token-00000000000000"
	app, err := proxy.New(proxy.Config{Rules: []proxy.Rule{{Origin: Origin, Path: "/echo", Adapter: "demo"}}}, access, proxy.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	logins := NewLogins()
	handler, closeClient, err := Handler(app, "127.0.0.1:18080", "127.0.0.1:18090", access, PanelOptions{Logins: logins})
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	if host := PanelHost("127.0.0.1:18090"); host != "phantomprompt.localhost:18090" {
		t.Fatalf("panel host %q", host)
	}
	call := func(host, origin, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	login := call("phantomprompt.localhost:18090", "", logins.Issue(), nil)
	if login.Code != http.StatusOK || !strings.Contains(login.Body.String(), access) {
		t.Fatalf("login under the local name: %d", login.Code)
	}
	if call("phantomprompt.localhost:18090", "http://phantomprompt.localhost:18090", "/", nil).Code != http.StatusOK {
		t.Fatal("same-origin request under the local name rejected")
	}
	for _, blocked := range []struct{ host, origin string }{
		{"evil.localhost:18090", ""},
		{"phantomprompt.localhost:9999", ""},
		{"phantomprompt.localhost:18090", "https://evil.example"},
		{"phantomprompt.localhost:18090", "http://127.0.0.1:18090"},
		{"127.0.0.1:18090", "http://phantomprompt.localhost:18090"},
	} {
		if call(blocked.host, blocked.origin, logins.Issue(), nil).Code != http.StatusForbidden {
			t.Errorf("host %q with origin %q was not blocked", blocked.host, blocked.origin)
		}
	}
}

func TestTrustedFoldersFromThePanel(t *testing.T) {
	const access = "synthetic-access-token-00000000000000"
	app, err := proxy.New(proxy.Config{Rules: []proxy.Rule{{Origin: Origin, Path: "/echo", Adapter: "demo"}}}, access, proxy.Options{RulesFile: filepath.Join(t.TempDir(), "rules.json")})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	handler, closeClient, err := Handler(app, "127.0.0.1:18080", "127.0.0.1:18090", access, PanelOptions{Logins: NewLogins()})
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	request := func(method, path, body, auth string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Host = "127.0.0.1:18090"
		r.Header.Set(proxy.AccessHeader, auth)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	trust := func(path string, on bool) string {
		body, _ := json.Marshal(map[string]any{"path": path, "trusted": on})
		return string(body)
	}
	folder := t.TempDir()
	file := filepath.Join(folder, "fixture.json")
	os.WriteFile(file, []byte(`{"contact": "synthetic.value"}`+"\n"), 0600)
	home, _ := os.UserHomeDir()

	if request(http.MethodPost, "/api/trusted", trust(folder, true), "").Code != 401 || len(app.Policy().TrustedPaths) != 0 {
		t.Fatal("folder trusted without authentication")
	}
	for body, code := range map[string]int{trust(file, true): 400, trust(folder+"/missing", true): 404, trust("src/test", true): 400, trust(home, true): 400, trust("~", true): 400, `{"path":`: 400} {
		if got := request(http.MethodPost, "/api/trusted", body, access).Code; got != code {
			t.Fatalf("%s: status %d, want %d", body, got, code)
		}
	}
	if w := request(http.MethodPost, "/api/trusted", trust(folder, true), access); w.Code != 200 || !strings.Contains(w.Body.String(), `"files":1`) {
		t.Fatalf("trust: %d %s", w.Code, w.Body.String())
	}
	var status struct {
		Trusted   []proxy.ProtectedPathStatus `json:"trusted_paths"`
		Protected []proxy.ProtectedPathStatus `json:"protected_paths"`
	}
	if json.Unmarshal(request(http.MethodGet, "/status", "", access).Body.Bytes(), &status) != nil || len(status.Trusted) != 1 || status.Trusted[0].Path != folder || len(status.Protected) != 0 {
		t.Fatalf("status %+v", status)
	}
	if w := request(http.MethodPost, "/api/trusted", trust(folder, false), access); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" || len(app.Policy().TrustedPaths) != 0 {
		t.Fatalf("untrust: %d %s", w.Code, w.Body.String())
	}
}

func TestLogFolderShownInFinder(t *testing.T) {
	const access = "synthetic-access-token-00000000000000"
	app, err := proxy.New(proxy.Config{Rules: []proxy.Rule{{Origin: Origin, Path: "/echo", Adapter: "demo"}}}, access, proxy.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	shown := 0
	var failure error
	for _, show := range []func() error{nil, func() error { shown++; return failure }} {
		handler, closeClient, err := Handler(app, "127.0.0.1:18080", "127.0.0.1:18090", access, PanelOptions{ShowLogs: show, Logins: NewLogins()})
		if err != nil {
			t.Fatal(err)
		}
		defer closeClient()
		request := func(method, path, auth string) *httptest.ResponseRecorder {
			r := httptest.NewRequest(method, path, nil)
			r.Host = "127.0.0.1:18090"
			r.Header.Set(proxy.AccessHeader, auth)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			return w
		}
		page := request(http.MethodGet, "/", access).Body.String()
		if show == nil {
			if request(http.MethodPost, "/api/logs/show", access).Code != 404 || !strings.Contains(page, `data-logs="false"`) {
				t.Fatal("Finder button offered where it cannot run")
			}
			continue
		}
		bundle := request(http.MethodGet, "/assets/app.js", access).Body.String()
		if !strings.Contains(page, `data-logs="true"`) || !strings.Contains(bundle, "/api/logs/show") || !strings.Contains(bundle, "show-logs") {
			t.Fatal("Finder button missing")
		}
		if request(http.MethodPost, "/api/logs/show", "").Code != 401 || request(http.MethodGet, "/api/logs/show", access).Code == http.StatusNoContent || shown != 0 {
			t.Fatal("log folder opened without a signed-in POST")
		}
		if request(http.MethodPost, "/api/logs/show", access).Code != http.StatusNoContent || shown != 1 {
			t.Fatal("log folder not opened")
		}
		failure = errors.New("open failed")
		if w := request(http.MethodPost, "/api/logs/show", access); w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "Could not open the log folder") {
			t.Fatalf("failure not reported: %d %s", w.Code, w.Body)
		}
	}
}
