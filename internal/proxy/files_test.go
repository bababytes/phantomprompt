package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"phantomprompt/internal/mask"
	"phantomprompt/internal/protocol"
)

func TestProtectedFoldersMaskFileContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	folder := filepath.Join(home, ".ssh")
	os.MkdirAll(filepath.Join(folder, "nested"), 0700)
	os.WriteFile(filepath.Join(folder, "config"), []byte("Host build-server\n  HostName 10.0.0.17\n"), 0600)
	os.WriteFile(filepath.Join(folder, "nested", "known_hosts"), []byte("build-server ssh-ed25519 AAAAC3NzaSYNTHETIC\n"), 0600)
	os.WriteFile(filepath.Join(folder, "binary"), []byte("binary\x00content with a long enough line"), 0600)
	os.WriteFile(filepath.Join(folder, "huge"), make([]byte, maxProtectedFile+1), 0600)
	os.WriteFile(filepath.Join(home, "outside.txt"), []byte("outside line that must stay visible\n"), 0600)
	os.Symlink(filepath.Join(home, "outside.txt"), filepath.Join(folder, "link"))

	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	id, _, err := app.CreateCLISession("claude", 1)
	if err != nil {
		t.Fatal(err)
	}
	policy := mask.DefaultPolicy()
	policy.ProtectedPaths = []string{"~/.ssh", "~/missing"}
	if err := app.SetPolicy(policy); err != nil {
		t.Fatal(err)
	}
	status := app.ProtectedPathStatus()
	if len(status) != 2 || status[0].Files != 2 || status[0].Lines != 3 || status[0].Skipped != 3 || status[0].Error != "" || status[1].Error != "not found" {
		t.Fatalf("status %+v", status)
	}
	session, release, err := app.Sessions.Acquire(id, "cli")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := session.Mask([]string{"anything"}); !errors.Is(err, mask.ErrFolders) {
		t.Fatalf("masking ran while a protected folder is missing: %v", err)
	}
	release()
	policy.ProtectedPaths = []string{"~/.ssh"}
	if err := app.SetPolicy(policy); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"     1\tHost build-server\n     2\t  HostName 10.0.0.17\noutside line that must stay visible"}]}]}`)
	session, release, err = app.Sessions.Acquire(id, "cli")
	if err != nil {
		t.Fatal(err)
	}
	release()
	masked, counts, err := protocol.MaskAgentMessages(body, session, "", nil)
	if err != nil || counts.File != 2 || strings.Contains(string(masked), "10.0.0.17") || strings.Contains(string(masked), "build-server") || !strings.Contains(string(masked), "outside line that must stay visible") {
		t.Fatalf("masked %s counts %+v err %v", masked, counts, err)
	}

	os.WriteFile(filepath.Join(folder, "id_extra"), []byte("a freshly added secret line\n"), 0600)
	app.refreshProtectedFiles(false)
	if status := app.ProtectedPathStatus(); status[0].Files != 3 {
		t.Fatalf("new file not indexed: %+v", status)
	}
	session, release, _ = app.Sessions.Acquire(id, "cli")
	_, counts, err = session.Mask([]string{"a freshly added secret line"})
	release()
	if err != nil || counts.File != 1 {
		t.Fatal("refresh did not reach the open session")
	}
}

func TestBlockedFolderReachesEveryChannel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	folder := filepath.Join(home, ".aws")
	os.MkdirAll(folder, 0700)
	secretLine := "aws_secret_access_key = SYNTHETICblockedLINE0000000000"
	os.WriteFile(filepath.Join(folder, "credentials"), []byte("[default]\n"+secretLine+"\n"), 0600)
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	policy := mask.DefaultPolicy()
	policy.ProtectedPaths = []string{"~/.aws"}
	policy.BlockedPaths = []string{"~/.aws"}
	if err := app.SetPolicy(policy); err != nil {
		t.Fatal(err)
	}
	if status := app.ProtectedPathStatus(); len(status) != 1 || !status[0].Blocked {
		t.Fatalf("status %+v", status)
	}
	session, release, err := app.cliSession("")
	if err != nil {
		t.Fatal(err)
	}
	out, counts, err := session.Mask([]string{"file says:\n" + secretLine})
	release()
	if err != nil || out[0] != mask.BlockedNotice || counts.BlockedTexts != 1 {
		t.Fatalf("CLI text not removed: %q %+v %v", out, counts, err)
	}
	handler := app.Extension(LocalAddresses{Proxy: "127.0.0.1:18080"})
	var settings CLISettings
	if code := extensionGet(t, handler, "/v1/cli", &settings); code != 200 || len(settings.BlockedPaths) != 1 || settings.BlockedPaths[0] != folder {
		t.Fatalf("blocked paths not offered to launchers: %d %+v", code, settings.BlockedPaths)
	}
}

func TestUnblockingAFolderRestoresAccess(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	folder := filepath.Join(home, ".aws")
	os.MkdirAll(folder, 0700)
	file := filepath.Join(folder, "credentials")
	secretLine := "private notes for the staging build server"
	os.WriteFile(file, []byte(secretLine+"\n"), 0600)
	before, _ := os.Stat(file)
	app, err := New(Config{Integrations: []string{ClaudeCode}}, localSecret, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	handler := app.Extension(LocalAddresses{Proxy: "127.0.0.1:18080"})
	text := "file says:\n" + secretLine
	terminal := func() string {
		t.Helper()
		id, _, err := app.CreateCLISession("claude", 1)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	send := func(id string, protected, blocked []string) (string, string, mask.Counts, CLISettings) {
		t.Helper()
		policy := mask.DefaultPolicy()
		policy.ProtectedPaths, policy.BlockedPaths = protected, blocked
		if err := app.SetPolicy(policy); err != nil {
			t.Fatal(err)
		}
		session, release, err := app.Sessions.Acquire(id, "cli")
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		out, counts, err := session.Mask([]string{text})
		if err != nil {
			t.Fatal(err)
		}
		var settings CLISettings
		if code := extensionGet(t, handler, "/v1/cli", &settings); code != 200 {
			t.Fatalf("/v1/cli %d", code)
		}
		return out[0], session.Restore(out[0]), counts, settings
	}

	open := terminal()
	if out, _, _, settings := send(open, []string{"~/.aws"}, []string{"~/.aws"}); out != mask.BlockedNotice || len(settings.BlockedPaths) != 1 {
		t.Fatalf("folder was not blocked: %q %+v", out, settings.BlockedPaths)
	}
	out, restored, counts, settings := send(open, []string{"~/.aws"}, nil)
	if out == mask.BlockedNotice || strings.Contains(out, secretLine) || restored != text || counts.File != 1 || counts.BlockedTexts != 0 || len(settings.BlockedPaths) != 0 {
		t.Fatalf("switching to Masked kept the folder blocked: %q %q %+v %+v", out, restored, counts, settings.BlockedPaths)
	}
	if out, _, counts, _ := send(terminal(), nil, nil); out != text || counts.File != 0 {
		t.Fatalf("removing the folder kept it protected: %q %+v", out, counts)
	}
	after, err := os.Stat(file)
	content, _ := os.ReadFile(file)
	if err != nil || after.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) || string(content) != secretLine+"\n" {
		t.Fatal("blocking changed the file on disk")
	}
}

func extensionGet(t *testing.T, handler http.Handler, path string, reply any) int {
	t.Helper()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), reply); err != nil {
			t.Fatal(err)
		}
	}
	return w.Code
}

func TestFolderAccessIsLoggedWithoutContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, ".ssh"), 0700)
	os.MkdirAll(filepath.Join(home, "notes"), 0700)
	keyLine := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAISyntheticKeyMaterial me@host"
	noteLine := "meeting notes about the merger plan"
	os.WriteFile(filepath.Join(home, ".ssh", "id_synthetic"), []byte(keyLine+"\n"), 0600)
	os.WriteFile(filepath.Join(home, "notes", "plan.txt"), []byte(noteLine+"\n"), 0600)
	app, handler, upstream := gatewayFixture(t)
	policy := mask.DefaultPolicy()
	policy.ProtectedPaths, policy.BlockedPaths = []string{"~/.ssh", "~/notes"}, []string{"~/.ssh"}
	if err := app.SetPolicy(policy); err != nil {
		t.Fatal(err)
	}
	session, secret, err := app.CreateCLISession("claude", 0)
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimPrefix(ClaudeBaseURL("127.0.0.1:18081", session, secret), "http://127.0.0.1:18081")
	tool := func(id, input string) string {
		return `{"role":"assistant","content":[{"type":"tool_use","id":"` + id + `","name":"Bash","input":` + input + `}]}`
	}
	result := func(id, content string, refused bool) string {
		encoded, _ := json.Marshal(content)
		flag := ""
		if refused {
			flag = `"is_error":true,`
		}
		return `{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + id + `",` + flag + `"content":` + string(encoded) + `}]}`
	}
	send := func(messages []string) {
		t.Helper()
		body := `{"model":"m","max_tokens":10,"stream":true,"messages":[` + strings.Join(messages, ",") + `]}`
		if w := gatewayCall(handler, base+"/v1/messages", body, nil); w.Code != 200 {
			t.Fatalf("gateway %d %s", w.Code, w.Body.String())
		}
	}

	history := []string{`{"role":"user","content":"check my setup"}`, tool("t1", `{"command":"cat ~/.ssh/id_synthetic"}`), result("t1", "Permission denied by a deny rule", true)}
	send(history)
	send(history)
	history = append(history, tool("t2", `{"command":"python3 read_key.py"}`), result("t2", keyLine, false))
	send(history)
	history = append(history, tool("t3", `{"file_path":"`+home+`/notes/plan.txt"}`), result("t3", "     1\t"+noteLine, false))
	send(history)
	send(history)

	for i, body := range upstream.bodies {
		if strings.Contains(body, keyLine) || strings.Contains(body, noteLine) {
			t.Fatalf("request %d sent folder content", i)
		}
	}
	var access mask.Counts
	for _, row := range app.Events.Snapshot() {
		if row.Reason == "blocked_folder_content_removed" {
			t.Fatal("removed content is still logged as a separate row on every request")
		}
		access = access.Add(mask.Counts{MaskedFolders: row.Matches.MaskedFolders, BlockedFolders: row.Matches.BlockedFolders})
	}
	if access.BlockedFolders["~/.ssh"] != 2 || access.MaskedFolders["~/notes"] != 1 || len(access.BlockedFolders)+len(access.MaskedFolders) != 2 {
		t.Fatalf("folder access in the log: %+v", access)
	}
	logged, _ := json.Marshal(app.Events.Snapshot())
	for _, private := range []string{keyLine, noteLine, "id_synthetic", "plan.txt", home} {
		if strings.Contains(string(logged), private) {
			t.Fatalf("log contains %q", private)
		}
	}
}

func TestIncompleteProtectedFoldersStopEveryRequest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	app, handler, upstream := gatewayFixture(t)
	session, secret, err := app.CreateCLISession("claude", 0)
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimPrefix(ClaudeBaseURL("127.0.0.1:18081", session, secret), "http://127.0.0.1:18081")
	folder := filepath.Join(home, "private")
	write := func(name string, size int) {
		os.MkdirAll(filepath.Dir(filepath.Join(folder, name)), 0700)
		data := []byte(strings.Repeat("line of private notes\n", size/22+1)[:size])
		if err := os.WriteFile(filepath.Join(folder, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	protect := func(paths ...string) {
		policy := mask.DefaultPolicy()
		policy.ProtectedPaths = paths
		if err := app.SetPolicy(policy); err != nil {
			t.Fatal(err)
		}
	}
	sent := func() int {
		upstream.mu.Lock()
		defer upstream.mu.Unlock()
		return len(upstream.bodies)
	}
	expect := func(name string, blocked bool) {
		t.Helper()
		before := sent()
		w := gatewayCall(handler, base+"/v1/messages", gatewayRequest, nil)
		if blocked && (w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "protected_folder_not_indexed") || sent() != before) {
			t.Fatalf("%s: not blocked: %d, upstream calls %d -> %d", name, w.Code, before, sent())
		}
		if !blocked && (w.Code != 200 || sent() != before+1) {
			t.Fatalf("%s: blocked: %d %s", name, w.Code, w.Body.String())
		}
	}

	for i := range maxProtectedFiles {
		write(fmt.Sprintf("notes/%04d.txt", i), 30)
	}
	protect("~/private")
	expect("2000 files", false)
	write("notes/2000.txt", 30)
	app.refreshProtectedFiles(false)
	expect("2001 files", true)
	os.RemoveAll(filepath.Join(folder, "notes"))

	write("big.txt", maxProtectedFile)
	app.refreshProtectedFiles(false)
	expect("a file of exactly 1 MiB", false)
	write("big.txt", maxProtectedFile+1)
	app.refreshProtectedFiles(false)
	expect("a file over 1 MiB", true)
	os.Remove(filepath.Join(folder, "big.txt"))
	os.WriteFile(filepath.Join(folder, "big.bin"), append([]byte("binary\x00"), make([]byte, maxProtectedFile)...), 0600)
	app.refreshProtectedFiles(false)
	expect("a binary file over 1 MiB", false)
	os.Remove(filepath.Join(folder, "big.bin"))

	for i := range 16 {
		write(fmt.Sprintf("part%02d.txt", i), maxProtectedFile)
	}
	app.refreshProtectedFiles(false)
	expect("16 MiB in total", false)
	write("part16.txt", 1)
	app.refreshProtectedFiles(false)
	expect("over 16 MiB in total", true)
	os.RemoveAll(folder)
	app.refreshProtectedFiles(false)
	expect("folder removed", true)

	write("notes.txt", 100)
	app.refreshProtectedFiles(false)
	expect("folder back", false)
	if os.Getuid() != 0 {
		os.Chmod(filepath.Join(folder, "notes.txt"), 0)
		app.refreshProtectedFiles(true)
		expect("unreadable file", true)
		os.Chmod(filepath.Join(folder, "notes.txt"), 0600)
		app.refreshProtectedFiles(true)
		expect("readable again", false)
	}

	if err := app.storePolicy(mask.Policy{Secrets: true, ProtectedPaths: []string{"~/private", "~/other"}}); err != nil {
		t.Fatal(err)
	}
	expect("a protected folder added but not indexed yet", true)
}
