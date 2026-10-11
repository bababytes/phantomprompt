package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// planted is one sensitive value written into a generated project file, with what the panel
// should report for it: the match kind and, for secrets, the detector's name.
type planted struct {
	kind, detector, value string
}

type fixtureFile struct {
	path, content string
}

type projectFixture struct {
	random *rand.Rand
	values []planted
	used   map[string]bool
}

func newProjectFixture(seed uint64) *projectFixture {
	return &projectFixture{random: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)), used: make(map[string]bool)}
}

func (f *projectFixture) pick(alphabet string, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[f.random.IntN(len(alphabet))]
	}
	return string(b)
}

func (f *projectFixture) plant(kind, detector string, generate func() string) string {
	for {
		value := generate()
		if !f.used[value] {
			f.used[value] = true
			f.values = append(f.values, planted{kind: kind, detector: detector, value: value})
			return value
		}
	}
}

const (
	upperChars  = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	lowerChars  = "abcdefghijklmnopqrstuvwxyz"
	digitChars  = "0123456789"
	symbolChars = "!%^*-+=~"
	labelled    = "Password, token or secret after a label"
)

func (f *projectFixture) password() string {
	return f.plant("secret", labelled, f.passwordValue)
}

// passwordValue starts with a letter and mixes every character class, so it is a strong value
// that the session learns; it avoids quotes, separators and URL delimiters.
func (f *projectFixture) passwordValue() string {
	b := []byte(f.pick(upperChars, 1) + f.pick(upperChars+lowerChars+digitChars+symbolChars, 8+f.random.IntN(6)) + f.pick(digitChars, 1) + f.pick(symbolChars, 1) + f.pick(lowerChars, 1))
	tail := b[1:]
	f.random.Shuffle(len(tail), func(i, j int) { tail[i], tail[j] = tail[j], tail[i] })
	return string(b)
}

func (f *projectFixture) urlPassword() string {
	return f.plant("secret", "Password in a URL", f.passwordValue)
}

func (f *projectFixture) email() string {
	return f.plant("email", "", func() string {
		return f.pick(lowerChars, 5+f.random.IntN(6)) + "." + f.pick(lowerChars, 4) + "@" + "example.com"
	})
}

func (f *projectFixture) vin() string {
	return f.plant("vin", "", func() string {
		return f.pick("ABCDEFGHJKLMNPRSTUVWXYZ", 3) + f.pick("ABCDEFGHJKLMNPRSTUVWXYZ0123456789", 14)
	})
}

func (f *projectFixture) guid() string {
	return f.plant("guid", "", func() string {
		hex := "0123456789abcdef"
		return f.pick(hex, 8) + "-" + f.pick(hex, 4) + "-4" + f.pick(hex, 3) + "-a" + f.pick(hex, 3) + "-" + f.pick(hex, 12)
	})
}

func (f *projectFixture) name() string {
	return f.plant("fields", "", func() string {
		consonants, vowels := "bdfgklmnprstvz", "aeiou"
		name := f.pick(strings.ToUpper(consonants), 1)
		for range 2 + f.random.IntN(2) {
			name += f.pick(vowels, 1) + f.pick(consonants, 1)
		}
		return name + f.pick(vowels, 1)
	})
}

func (f *projectFixture) awsKey() string {
	return f.plant("secret", "AWS access key ID", func() string { return "AKIA" + f.pick(upperChars+digitChars, 16) })
}

func (f *projectFixture) githubToken() string {
	return f.plant("secret", "GitHub token", func() string { return "ghp" + "_" + f.pick(upperChars+lowerChars+digitChars, 36) })
}

func (f *projectFixture) jwt() string {
	return f.plant("secret", "JSON Web Token", func() string {
		part := func() string { return f.pick(upperChars+lowerChars+digitChars+"-_", 12+f.random.IntN(12)) }
		return "eyJ" + part() + "." + part() + "." + part()
	})
}

// files writes a small project in the nine most common formats of a service repository.
func (f *projectFixture) files() []fixtureFile {
	return []fixtureFile{
		{"src/main/java/com/example/billing/CustomerService.java", `package com.example.billing;

import java.util.Map;

public class CustomerService {
    private static final String DB_PASSWORD = "` + f.password() + `";
    private static final String SUPPORT = "` + f.email() + `";

    public Map<String, String> sample(Customer customer) {
        customer.firstName = "` + f.name() + `";
        String car = "` + f.vin() + `";
        return Map.of("requestId", "` + f.guid() + `", "car", car);
    }
}
`},
		{"cmd/sync/main.go", `package main

import "fmt"

const apiToken = "` + f.githubToken() + `"

type Config struct {
	Password string
	Owner    string
}

func main() {
	cfg := Config{Password: "` + f.password() + `", Owner: "` + f.email() + `"}
	fmt.Println(cfg, apiToken, "` + f.vin() + `")
}
`},
		{"src/main/resources/application.properties", `spring.datasource.url=jdbc:postgresql://db:5432/billing
spring.datasource.username=billing
spring.datasource.password=` + f.password() + `
db.password.mongo=` + f.password() + `
app.admin.contact=` + f.email() + `
app.fleet.sample-car=` + f.vin() + `
`},
		{"charts/billing/values.yaml", `replicaCount: 2
image:
  repository: registry.example.test/billing
  tag: "1.4.2"
postgresql:
  auth:
    username: billing
    password: "` + f.password() + `"
    existingSecret: ""
aws:
  accessKeyId: ` + f.awsKey() + `
owner:
  firstName: ` + f.name() + `
  contact: ` + f.email() + `
`},
		{"web/src/api.js", `const config = {
  baseUrl: "https://api.example.test",
  password: '` + f.password() + `',
  apiKey: '` + f.password() + `',
  owner: '` + f.email() + `',
};

export const trackingId = '` + f.guid() + `';
export default config;
`},
		{"app/settings.py", `import os

DEBUG = os.environ.get("DEBUG") == "1"
DATABASES = {"default": {"ENGINE": "django.db.backends.postgresql", "USER": "app", "PASSWORD": "` + f.password() + `"}}
SECRET_KEY = "` + f.password() + `"
ADMINS = [("Ops", "` + f.email() + `")]
AWS_ACCESS_KEY_ID = "` + f.awsKey() + `"
`},
		{"web/src/client.ts", `export interface Owner { firstName: string; lastName: string }

export const owner: Owner = { firstName: "` + f.name() + `", lastName: "` + f.name() + `" };
const jwt = "` + f.jwt() + `";
export const headers = { Authorization: ` + "`Bearer ${jwt}`" + ` };
export const vehicle = "` + f.vin() + `";
`},
		{".env", `APP_ENV=production
DATABASE_URL=postgres://app:` + f.urlPassword() + `@db:5432/app
REDIS_PASSWORD=` + f.password() + `
SMTP_USER=` + f.email() + `
GITHUB_TOKEN=` + f.githubToken() + `
`},
		{"db/seed.sql", `CREATE USER billing WITH PASSWORD '` + f.password() + `';
INSERT INTO customers (id, contact, car) VALUES ('` + f.guid() + `', '` + f.email() + `', '` + f.vin() + `');
`},
	}
}

func fixtureSeed(t *testing.T) uint64 {
	seed := uint64(time.Now().UnixNano())
	if fixed := os.Getenv("PHANTOMPROMPT_FIXTURE_SEED"); fixed != "" {
		parsed, err := strconv.ParseUint(fixed, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		seed = parsed
	}
	t.Logf("fixture seed %d (repeat with PHANTOMPROMPT_FIXTURE_SEED=%d)", seed, seed)
	return seed
}

var readGutter = regexp.MustCompile(`(?m)^ *[0-9]+→`)

// copyingModel answers like a model asked to copy files: it takes the Read results it was sent
// and writes each one back unchanged under copy/, streaming the tool input in small pieces.
type copyingModel struct {
	mu     sync.Mutex
	bodies []string
}

func (m *copyingModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	m.mu.Lock()
	m.bodies = append(m.bodies, string(body))
	m.mu.Unlock()
	var request struct {
		Messages []struct {
			Content []struct {
				Type      string          `json:"type"`
				ID        string          `json:"id"`
				Input     json.RawMessage `json:"input"`
				ToolUseID string          `json:"tool_use_id"`
				Content   string          `json:"content"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	paths := map[string]string{}
	var results [][2]string
	for _, message := range request.Messages {
		for _, block := range message.Content {
			switch block.Type {
			case "tool_use":
				var input struct {
					FilePath string `json:"file_path"`
				}
				json.Unmarshal(block.Input, &input)
				paths[block.ID] = input.FilePath
			case "tool_result":
				results = append(results, [2]string{paths[block.ToolUseID], readGutter.ReplaceAllString(block.Content, "")})
			}
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(name string, value any) {
		data, _ := json.Marshal(value)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
	}
	send("message_start", map[string]any{"type": "message_start", "message": map[string]any{"usage": map[string]any{"input_tokens": 900}}})
	for index, result := range results {
		send("content_block_start", map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "tool_use", "id": fmt.Sprintf("toolu_w%d", index), "name": "Write", "input": map[string]any{}}})
		input, _ := json.Marshal(map[string]string{"file_path": "copy/" + result[0], "content": result[1]})
		for i := 0; i < len(input); i += 7 {
			send("content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(input[i:min(i+7, len(input))])}})
		}
		send("content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
	}
	send("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use"}})
	send("message_stop", map[string]any{"type": "message_stop"})
}

// claudeReadRequest is what Claude Code sends after reading the files: the user's prompt, its Read
// calls and their results in the Read tool's numbered-line format.
func claudeReadRequest(files []fixtureFile) string {
	var calls, results []map[string]any
	for i, file := range files {
		id := fmt.Sprintf("toolu_r%d", i)
		calls = append(calls, map[string]any{"type": "tool_use", "id": id, "name": "Read", "input": map[string]string{"file_path": file.path}})
		var numbered strings.Builder
		for n, line := range strings.SplitAfter(file.content, "\n") {
			if line != "" {
				fmt.Fprintf(&numbered, "%6d→%s", n+1, line)
			}
		}
		results = append(results, map[string]any{"type": "tool_result", "tool_use_id": id, "content": numbered.String()})
	}
	body, _ := json.Marshal(map[string]any{"model": "m", "max_tokens": 4096, "stream": true, "messages": []any{
		map[string]any{"role": "user", "content": []any{map[string]string{"type": "text", "text": "Copy every file of this project into copy/ without changing a byte."}}},
		map[string]any{"role": "assistant", "content": calls},
		map[string]any{"role": "user", "content": results},
	}})
	return string(body)
}

// writtenFiles rebuilds the Write calls a client receives from the restored stream.
func writtenFiles(t *testing.T, stream string) map[string]string {
	t.Helper()
	inputs := map[int]*strings.Builder{}
	scanner := bufio.NewScanner(strings.NewReader(stream))
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue
		}
		var event struct {
			Type  string `json:"type"`
			Index int    `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if json.Unmarshal([]byte(data), &event) != nil || event.Delta.Type != "input_json_delta" {
			continue
		}
		if inputs[event.Index] == nil {
			inputs[event.Index] = &strings.Builder{}
		}
		inputs[event.Index].WriteString(event.Delta.PartialJSON)
	}
	written := map[string]string{}
	for index, input := range inputs {
		var write struct {
			FilePath string `json:"file_path"`
			Content  string `json:"content"`
		}
		if err := json.Unmarshal([]byte(input.String()), &write); err != nil {
			t.Fatalf("Write call %d is not valid JSON after restore: %v\n%s", index, err, input.String())
		}
		written[strings.TrimPrefix(write.FilePath, "copy/")] = write.Content
	}
	return written
}

// TestCopyingAProjectThroughClaudeCode reads a generated project with sensitive values in nine
// formats, lets a model copy it, and checks the three promises: nothing sensitive reaches the
// model, the panel counts every value, and the copies are byte-for-byte the originals.
func TestCopyingAProjectThroughClaudeCode(t *testing.T) {
	seed := fixtureSeed(t)
	fixture := newProjectFixture(seed)
	files := fixture.files()

	model := &copyingModel{}
	app, handler := gatewayFixtureWith(t, model)
	session, secret, err := app.CreateCLISession("claude", 0)
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimPrefix(ClaudeBaseURL("127.0.0.1:18081", session, secret), "http://127.0.0.1:18081")
	w := gatewayCall(handler, base+"/v1/messages", claudeReadRequest(files), nil)
	if w.Code != 200 || len(model.bodies) != 1 {
		t.Fatalf("seed %d: gateway response %d %s", seed, w.Code, w.Body.String())
	}

	for _, value := range fixture.values {
		if strings.Contains(model.bodies[0], value.value) {
			t.Errorf("seed %d: %s value reached the model: %q", seed, value.kind, value.value)
		}
	}

	want := map[string]int{}
	detectors := map[string]int{}
	for _, value := range fixture.values {
		want[value.kind]++
		if value.detector != "" {
			detectors[value.detector]++
		}
	}
	var masked *Event
	for _, event := range app.Events.Snapshot() {
		switch event.Reason {
		case "masked_and_restored":
			masked = &event
		case "placeholders_not_restored", "secret_kept_masked_in_tool":
			t.Errorf("seed %d: unexpected %s event %+v", seed, event.Reason, event)
		}
	}
	if masked == nil {
		t.Fatalf("seed %d: no masking event in the panel", seed)
	}
	got := map[string]int{"vin": masked.Matches.VIN, "email": masked.Matches.Email, "guid": masked.Matches.GUID, "secret": masked.Matches.Secret, "fields": masked.Matches.Fields}
	for kind, count := range got {
		if count != want[kind] {
			t.Errorf("seed %d: panel shows %d %s values, %d were written", seed, count, kind, want[kind])
		}
	}
	for detector, count := range detectors {
		if use := masked.Rules[detector]; use.Values != count || !use.Secret {
			t.Errorf("seed %d: panel shows %+v for %q, %d values were written", seed, use, detector, count)
		}
	}
	if len(masked.Rules) != len(detectors) {
		t.Errorf("seed %d: panel lists detectors %v, want %v", seed, masked.Rules, detectors)
	}

	written := writtenFiles(t, w.Body.String())
	if len(written) != len(files) {
		t.Fatalf("seed %d: model wrote %d files, want %d", seed, len(written), len(files))
	}
	for _, file := range files {
		if written[file.path] != file.content {
			t.Errorf("seed %d: copy of %s differs\n--- original\n%s\n--- copy\n%s", seed, file.path, file.content, written[file.path])
		}
	}
}
