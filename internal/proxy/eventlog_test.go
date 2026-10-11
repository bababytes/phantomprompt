package proxy

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"phantomprompt/internal/applog"
)

func TestOnlyEventsThatMatterReachTheAppLog(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "PhantomPrompt")
	log, err := applog.Open(folder)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	upstream := &anthropicUpstream{}
	var failing atomic.Bool
	app, handler := gatewayFixtureOptions(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(529)
			io.WriteString(w, `{"type":"error","error":{"type":"overloaded_error"}}`)
			return
		}
		upstream.ServeHTTP(w, r)
	}), Options{Log: log})
	if app.LogFolder() != folder {
		t.Fatalf("log folder %q", app.LogFolder())
	}
	session, secret, err := app.CreateCLISession("claude", 0)
	if err != nil {
		t.Fatal(err)
	}
	app.cliSessions.setFolder(session, "/work/project")
	base := strings.TrimPrefix(ClaudeBaseURL("127.0.0.1:18081", session, secret), "http://127.0.0.1:18081")
	for range 3 {
		if w := gatewayCall(handler, base+"/v1/messages", gatewayRequest, nil); w.Code != 200 {
			t.Fatalf("gateway %d", w.Code)
		}
	}
	upstream.mangle = strings.ToUpper
	gatewayCall(handler, base+"/v1/messages", gatewayRequest, nil)
	if w := gatewayCall(handler, base+"/v1/messages", `{"model":"m","messages":[{"role":"user","content":[{"type":"image","source":{}}]}]}`, nil); w.Code != 422 {
		t.Fatalf("attachment %d", w.Code)
	}
	failing.Store(true)
	if w := gatewayCall(handler, base+"/v1/messages", gatewayRequest, nil); w.Code != 529 {
		t.Fatalf("upstream error %d", w.Code)
	}
	app.SetProtection(false)
	app.SetProtection(true)
	app.Events.Clear()

	data, err := os.ReadFile(filepath.Join(folder, time.Now().Format(time.DateOnly)+".log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	want := []string{
		`level=WARN msg="placeholders not restored" reason=placeholders_not_restored status=200 service=claude-code route=/v1/messages unrestored=1 folder=/work/project`,
		`level=ERROR msg="request blocked" reason=attachments_not_supported status=422 service=claude-code route=/v1/messages folder=/work/project`,
		`level=ERROR msg="AI service returned an error" reason=passed_without_restore status=529 service=claude-code route=/v1/messages folder=/work/project`,
		`level=WARN msg="protection turned off" reason=protection_disabled`,
		`level=INFO msg="protection turned on" reason=protection_enabled`,
	}
	if len(lines) != len(want) {
		t.Fatalf("the log holds %d lines, want %d:\n%s", len(lines), len(want), data)
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, "time=") || !strings.HasSuffix(line, want[i]) {
			t.Fatalf("line %d:\n%s\nwant suffix:\n%s", i+1, line, want[i])
		}
	}
	text := string(data)
	if strings.Contains(text, "masked_and_restored") || strings.Contains(text, "TEST0000000000001") || strings.Contains(text, "PP_VIN_") || strings.Contains(text, session) || strings.Contains(text, secret) || strings.Contains(text, localSecret) {
		t.Fatal("the log holds a successful request, a value, a placeholder, a session id or a secret")
	}
	if len(app.Events.Snapshot()) != 0 {
		t.Fatal("Clear history kept rows")
	}

	var nothing App
	nothing.Events.Add(Event{Phase: "request", Status: 500, Reason: "without a log"})
	if nothing.LogFolder() != "" {
		t.Fatal("an app without a log reports a folder")
	}
}
