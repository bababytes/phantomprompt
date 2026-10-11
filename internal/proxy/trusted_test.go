package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func trustedGatewayCall(t *testing.T, app *App, handler http.Handler, content string) {
	t.Helper()
	session, secret, err := app.CreateCLISession("claude", 0)
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimPrefix(ClaudeBaseURL("127.0.0.1:18081", session, secret), "http://127.0.0.1:18081")
	body, _ := json.Marshal(map[string]any{"model": "m", "max_tokens": 10, "stream": true, "messages": []any{map[string]string{"role": "user", "content": content}}})
	if w := gatewayCall(handler, base+"/v1/messages", string(body), nil); w.Code != 200 {
		t.Fatalf("gateway response %d %s", w.Code, w.Body.String())
	}
}

func TestTrustedFolderValuesReachTheModelAsIs(t *testing.T) {
	app, handler, upstream := gatewayFixture(t)
	folder := t.TempDir()
	if err := os.WriteFile(filepath.Join(folder, "CarTest.java"), []byte("String expected = \"TEST0000000000002\";\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := app.SetTrustedPath(folder, true); err != nil {
		t.Fatal(err)
	}
	if status := app.TrustedPathStatus(); len(status) != 1 || status[0].Files != 1 || status[0].Values != 1 || len(app.ProtectedPathStatus()) != 0 {
		t.Fatalf("trusted %+v protected %+v", status, app.ProtectedPathStatus())
	}
	trustedGatewayCall(t, app, handler, "Compare TEST0000000000002 from the test with TEST0000000000001")
	if !strings.Contains(upstream.bodies[0], "TEST0000000000002") || strings.Contains(upstream.bodies[0], "TEST0000000000001") {
		t.Fatalf("upstream got %s", upstream.bodies[0])
	}

	if err := app.SetTrustedPath(folder, false); err != nil || len(app.TrustedPathStatus()) != 0 {
		t.Fatalf("untrust: %v %+v", err, app.TrustedPathStatus())
	}
	trustedGatewayCall(t, app, handler, "Compare TEST0000000000002 from the test with TEST0000000000001")
	if strings.Contains(upstream.bodies[1], "TEST0000000000002") {
		t.Fatalf("folder still trusted after removal: %s", upstream.bodies[1])
	}
}

func TestProtectedFolderInsideATrustedOneIsNotScanned(t *testing.T) {
	app, handler, upstream := gatewayFixture(t)
	folder := t.TempDir()
	secrets := filepath.Join(folder, "secrets")
	os.MkdirAll(secrets, 0700)
	os.WriteFile(filepath.Join(folder, "CarTest.java"), []byte("String expected = \"TEST0000000000002\";\n"), 0600)
	os.WriteFile(filepath.Join(secrets, "real.env"), []byte("CAR=TEST0000000000003\n"), 0600)
	policy := app.Policy()
	policy.ProtectedPaths = []string{secrets}
	policy.TrustedPaths = []string{folder}
	if err := app.SetPolicy(policy); err != nil {
		t.Fatal(err)
	}
	if status := app.TrustedPathStatus(); len(status) != 1 || status[0].Files != 1 || status[0].Values != 1 {
		t.Fatalf("trusted %+v", status)
	}
	trustedGatewayCall(t, app, handler, "Compare TEST0000000000002 with TEST0000000000003")
	if !strings.Contains(upstream.bodies[0], "TEST0000000000002") || strings.Contains(upstream.bodies[0], "TEST0000000000003") {
		t.Fatalf("upstream got %s", upstream.bodies[0])
	}
}

func TestTrustedValuesFollowFileAndRuleChanges(t *testing.T) {
	app, handler, upstream := gatewayFixture(t)
	folder := t.TempDir()
	fixture := filepath.Join(folder, "fixture.yaml")
	os.WriteFile(fixture, []byte("car: TEST0000000000002\n"), 0600)
	if err := app.SetTrustedPath(folder, true); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(fixture, []byte("car: TEST0000000000004\ncarCode: Fixture-Code-77\n"), 0600)
	app.refreshProtectedFiles(false)
	policy := app.Policy()
	policy.CustomFields = []string{"carCode"}
	if err := app.SetPolicy(policy); err != nil {
		t.Fatal(err)
	}
	trustedGatewayCall(t, app, handler, "New car TEST0000000000004, old car TEST0000000000002, code Fixture-Code-77, carCode: Other-Code-55")
	sent := upstream.bodies[0]
	if !strings.Contains(sent, "TEST0000000000004") || strings.Contains(sent, "TEST0000000000002") || !strings.Contains(sent, "Fixture-Code-77") || strings.Contains(sent, "Other-Code-55") {
		t.Fatalf("upstream got %s", sent)
	}
}

func TestHomeFolderCannotBeTrusted(t *testing.T) {
	app, _, _ := gatewayFixture(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	for _, path := range []string{home, home + "/", "~/"} {
		if err := app.SetTrustedPath(path, true); err == nil {
			t.Fatalf("%q trusted", path)
		}
	}
}

func TestTrustedFoldersOverTheLocalSocket(t *testing.T) {
	app, _, _ := gatewayFixture(t)
	handler := app.Extension(LocalAddresses{})
	call := func(method, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/trusted", strings.NewReader(body))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	folder := t.TempDir()
	add, _ := json.Marshal(map[string]any{"path": folder, "trusted": true})
	if w := call(http.MethodPost, string(add)); w.Code != 200 || !strings.Contains(w.Body.String(), folder) {
		t.Fatalf("add: %d %s", w.Code, w.Body.String())
	}
	if w := call(http.MethodGet, ""); w.Code != 200 || !strings.Contains(w.Body.String(), folder) {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	for _, rejected := range []string{`{"path":"src/test","trusted":true}`, `{"path":"/","trusted":true}`, `{"path":"/x","trusted":true,"extra":1}`} {
		if w := call(http.MethodPost, rejected); w.Code != 400 {
			t.Fatalf("%s: %d", rejected, w.Code)
		}
	}
	if w := call(http.MethodPost, `{"path":"`+folder+`","trusted":false}`); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("remove: %d %s", w.Code, w.Body.String())
	}
}

func TestAProtectedFolderReachedThroughAnAliasIsNotTrusted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	app, handler, upstream := gatewayFixture(t)
	real := filepath.Join(home, "audit", "real")
	private := filepath.Join(real, "fixtures", "private")
	os.MkdirAll(private, 0700)
	os.WriteFile(filepath.Join(real, "fixtures", "CarTest.java"), []byte("String expected = \"TEST0000000000002\";\n"), 0600)
	os.WriteFile(filepath.Join(private, "real.env"), []byte("CAR=TEST0000000000003\n"), 0600)
	alias := filepath.Join(home, "audit", "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	check := func(name string, trusted, protected []string) {
		t.Helper()
		policy := app.Policy()
		policy.ProtectedPaths, policy.TrustedPaths = protected, trusted
		if err := app.SetPolicy(policy); err != nil {
			t.Fatal(err)
		}
		if status := app.TrustedPathStatus(); len(status) != 1 || status[0].Values != 1 {
			t.Fatalf("%s: trusted %+v", name, status)
		}
		before := len(upstream.bodies)
		trustedGatewayCall(t, app, handler, "Compare TEST0000000000002 with TEST0000000000003")
		if body := upstream.bodies[before]; !strings.Contains(body, "TEST0000000000002") || strings.Contains(body, "TEST0000000000003") {
			t.Fatalf("%s: a protected value became trusted", name)
		}
	}
	check("alias of an ancestor", []string{filepath.Join(alias, "fixtures")}, []string{private})
	check("protected through the alias", []string{filepath.Join(real, "fixtures")}, []string{filepath.Join(alias, "fixtures", "private")})
	check("~ and an absolute path", []string{"~/audit/alias/fixtures"}, []string{private})
	os.MkdirAll(filepath.Join(private, "deeper"), 0700)
	os.WriteFile(filepath.Join(private, "deeper", "more.env"), []byte("CAR=TEST0000000000003\n"), 0600)
	check("nested protected folders", []string{filepath.Join(alias, "fixtures")}, []string{filepath.Join(alias, "fixtures", "private", "deeper"), private})

	elsewhere := filepath.Join(home, "elsewhere")
	os.MkdirAll(filepath.Join(elsewhere, "fixtures", "private"), 0700)
	os.WriteFile(filepath.Join(elsewhere, "fixtures", "CarTest.java"), []byte("String expected = \"TEST0000000000002\";\n"), 0600)
	os.WriteFile(filepath.Join(elsewhere, "fixtures", "private", "real.env"), []byte("CAR=TEST0000000000003\n"), 0600)
	os.Remove(alias)
	if err := os.Symlink(elsewhere, alias); err != nil {
		t.Fatal(err)
	}
	check("symlink retargeted", []string{filepath.Join(real, "fixtures")}, []string{filepath.Join(alias, "fixtures", "private"), private})
}
