package mask

import (
	"strings"
	"testing"
)

func TestLabelledSecretsAreMasked(t *testing.T) {
	cases := []struct{ text, value string }{
		{"db.password.mongo=ABCDEG", "ABCDEG"},
		{"spring.datasource.password=S3cr3t!pw", "S3cr3t!pw"},
		{"DB_PASSWORD=hunter2xyz", "hunter2xyz"},
		{"export MONGO_PASS='p@ss w0rd'", "p@ss w0rd"},
		{"database:\n  password: Zx9!qwerty\n", "Zx9!qwerty"},
		{"environment:\n  - POSTGRES_PASSWORD=pg-Secret-1\n", "pg-Secret-1"},
		{`private static final String DB_PASSWORD = "Abc123!";`, "Abc123!"},
		{`dbPassword := "go-Secret-9"`, "go-Secret-9"},
		{`	Password: "struct-pw-1",`, "struct-pw-1"},
		{`	cfg := Config{Password: "struct-pw-2", Owner: "ops"}`, "struct-pw-2"},
		{`const config = { password: 'js-pw-123' };`, "js-pw-123"},
		{`password: str = "py-pw-123"`, "py-pw-123"},
		{`{"apiKey": "k3y-v4lue-xyz"}`, "k3y-v4lue-xyz"},
		{"CREATE USER app WITH PASSWORD 'sql-pw-123';", "sql-pw-123"},
		{"ALTER USER app IDENTIFIED BY 'ora-pw-123';", "ora-pw-123"},
		{"Server=db;Database=app;User Id=sa;Password=Cs-pw-123;", "Cs-pw-123"},
		{"mysql --password=cli-pw-123 -h db", "cli-pw-123"},
		{"client_secret: 0a1b2c3d4e", "0a1b2c3d4e"},
		{"SECRET_KEY = 'django-insecure-abc'", "django-insecure-abc"},
		{"'password' => 'php-pw-123',", "php-pw-123"},
		{"refresh_token: rt-abc-123 # rotated weekly", "rt-abc-123"},
		{"PASSWORD=a;b&c", "a;b&c"},
		{"     3→DB_PASSWORD=read-tool-1\n     4→next", "read-tool-1"},
		{"     3\tDB_PASSWORD=read-tool-2\n", "read-tool-2"},
	}
	for _, tc := range cases {
		s := secretSession()
		out, counts, err := s.Mask([]string{tc.text})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out[0], tc.value) || counts.Secret != 1 || counts.RuleHits[SecretRuleKey(labelledSecretID)] != 1 {
			t.Errorf("%q -> %q %+v", tc.text, out[0], counts)
		}
		if s.Restore(out[0]) != tc.text {
			t.Errorf("%q restored as %q", tc.text, s.Restore(out[0]))
		}
	}
}

func TestLabelledSecretsIgnoreCodeAndProse(t *testing.T) {
	inputs := []string{
		"Secret: none",
		"The next token: it expires soon.",
		"Password: required",
		"password: ${DB_PASSWORD}",
		"password: {{ .Values.db.password | quote }}",
		"spring.datasource.password=${DB_PASSWORD:}",
		"password: string;",
		`password = input("Password: ")`,
		"this.password = password;",
		"token_endpoint: https://auth.example.test/token",
		"password_min_length: 12",
		"existingSecret: my-secret",
		"secretName: app-tls",
		`{"nextPageToken": "CAoQAA"}`,
		"PWD=/Users/someone/project",
		"OLDPWD=/tmp",
		"pass: true",
		"password: ********",
		"token = getToken()",
		"const password = process.env.DB_PASSWORD;",
		"connect(password=pw)",
		"if (password == null) {",
		"valueFrom:\n  secretKeyRef:\n    name: db\n",
		"password: |\n  multi\n",
		"passwordEncoder: bcrypt",
		"type Config struct {\n\tPassword string `json:\"password\"`\n}",
		"cancellationToken = default",
		"password: <your-password>",
		`if value, ok := strings.CutPrefix(entry, "OPENAI_API_KEY="); ok && value != "" {`,
		`if m.Quoted && m.Token == "" {`,
		`const tokenAlphabet = "abcdefghijkmnpqrstuvwxyz23456789"`,
		`unknown_token_in_input: "Blocked · unknown token",`,
	}
	for _, text := range inputs {
		out, counts, err := secretSession().Mask([]string{text})
		if err != nil {
			t.Fatal(err)
		}
		if counts.Secret != 0 {
			t.Errorf("false positive in %q -> %q", text, out[0])
		}
	}
}

func TestWeakLabelledPasswordsAreNotLearned(t *testing.T) {
	s := secretSession()
	if _, _, err := s.Mask([]string{"POSTGRES_PASSWORD=postgres\nAPP_PASSWORD=Hunter2xyz!"}); err != nil {
		t.Fatal(err)
	}
	out, _, err := s.Mask([]string{"docker run postgres && mysql -pHunter2xyz!"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out[0], "docker run postgres") || strings.Contains(out[0], "Hunter2xyz!") {
		t.Fatalf("weak value learned or strong value missed: %q", out[0])
	}
}

func TestLabelledSecretsCanBeTurnedOff(t *testing.T) {
	p := DefaultPolicy()
	p.DisabledSecrets = []string{labelledSecretID}
	out, counts, err := NewSession(4096, 1<<20, p).Mask([]string{"DB_PASSWORD=hunter2xyz"})
	if err != nil || out[0] != "DB_PASSWORD=hunter2xyz" || counts.Secret != 0 {
		t.Fatalf("%q %+v %v", out, counts, err)
	}
}
