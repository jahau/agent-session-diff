package redact

import (
	"strings"
	"testing"

	"github.com/jahau/agent-session-diff/internal/model"
)

// Fake tokens are assembled at runtime so no token-shaped literal is
// committed (keeps secret scanners quiet and makes intent obvious).
func fake(prefix string, n int) string { return prefix + strings.Repeat("A1b2", n/4+1)[:n] }

func TestSecrets(t *testing.T) {
	cases := []struct{ in, kind string }{
		{"curl -H 'x: " + fake("ghp_", 36) + "'", "github-token"},
		{"export KEY=" + fake("github_pat_", 40), "github-token"},
		{"ANTHROPIC_API_KEY=" + fake("sk-ant-api03-", 40) + " run", "anthropic-key"},
		{"openai " + fake("sk-proj-", 40), "openai-key"},
		{"aws AKIA" + "IOSFODNN7EXAMPLE", "aws-access-key"}, // AWS documentation example key
		{"slack " + fake("xoxb-", 30), "slack-token"},
		{"key " + fake("AIza", 35), "google-api-key"},
		{"stripe " + fake("sk_live_", 24), "stripe-key"},
		{"psql postgres://app:" + "s3cretpass" + "@db.internal/app", "url-credentials"},
		{`curl -H "Authorization: Bearer ` + fake("", 30) + `"`, "bearer-token"},
		{"export DB_PASSWORD=hunter2hunter2", "assignment"},
		{`{"api_key": "abcd1234efgh"}`, "assignment"},
		{"login --password 'pa55word!'", "cli-secret-flag"},
		{"-----BEGIN RSA PRIVATE KEY-----\nMIIE\n-----END RSA PRIVATE KEY-----", "private-key"},
		{"jwt " + fake("eyJ", 20) + "." + fake("eyJ", 20) + "." + fake("", 20), "jwt"},
	}
	for _, c := range cases {
		got := Secrets(c.in)
		if !strings.Contains(got, "[REDACTED:"+c.kind+"]") {
			t.Errorf("Secrets(%q) = %q, want kind %s", c.in, got, c.kind)
		}
	}
}

func TestSecretsLeavesOrdinaryTextAlone(t *testing.T) {
	for _, s := range []string{
		"go test ./...",
		"git status --short",
		"export TOKEN=$GITHUB_TOKEN",
		"grep -r password_reset src/",
		"see https://example.com/docs?id=7",
		"sk-short",
	} {
		if got := Secrets(s); got != s {
			t.Errorf("Secrets(%q) changed to %q", s, got)
		}
	}
}

func TestSecretsIdempotent(t *testing.T) {
	in := "export DEPLOY_TOKEN=fake-deploy-token-0000 && curl -H \"Authorization: Bearer fakebearertoken0000000000\" x"
	once := Secrets(in)
	if Secrets(once) != once {
		t.Errorf("not idempotent: %q -> %q", once, Secrets(once))
	}
	if strings.Contains(once, "fake-deploy-token") || strings.Contains(once, "fakebearertoken") {
		t.Errorf("secret left in %q", once)
	}
}

func TestPaths(t *testing.T) {
	p := NewPaths("/home/dev/widget", `C:\work\widget`)
	cases := []struct{ in, want string }{
		{"cd /home/dev/widget && go test", "cd <cwd> && go test"},
		{`type C:\WORK\Widget\config\a.go`, "type <cwd>\\config\\a.go"},
		{"ls /c/work/widget/x", "ls <cwd>/x"},
		{"cat /home/alice/.ssh/config", "cat <home>/"},
		{`dir C:\Users\bob\Documents\x.txt`, "dir <home>/"},
		{"cmd /c dir", "cmd /c dir"},
		{"echo hi > /dev/null", "echo hi > /dev/null"},
		{"open https://example.com/a/b", "open https://example.com/a/b"},
		{"cp a /var/lib/app/data.db", "cp a <path:"},
	}
	for _, c := range cases {
		got := p.String(c.in)
		if !strings.HasPrefix(got, c.want) {
			t.Errorf("Paths(%q) = %q, want prefix %q", c.in, got, c.want)
		}
	}
	for _, leak := range []string{"alice", "bob", "/var/lib"} {
		for _, c := range cases {
			if got := p.String(c.in); strings.Contains(got, leak) {
				t.Errorf("Paths(%q) leaks %q: %q", c.in, leak, got)
			}
		}
	}
	// Equal paths hash equally, so redacted sessions still compare.
	if p.String("/var/lib/app/data.db") != p.String("/var/lib/app/data.db") {
		t.Error("path hashing is not stable")
	}
	if p.String("/var/lib/app/a") == p.String("/var/lib/app/b") {
		t.Error("different paths hash equally")
	}
}

func TestSession(t *testing.T) {
	s := &model.Session{
		Label: "sess.jsonl", SourcePath: "/home/dev/exports/sess.jsonl", CWD: "/home/dev/widget",
		Commands:    []model.Command{{Text: "cd /home/dev/widget && TOKEN=abcdef123456 make", CWD: "/home/dev/widget/sub"}},
		FileChanges: []model.FileChange{{Path: "/opt/secret-project/x.go"}, {Path: "rel/y.go"}},
		Tests:       []model.TestRun{{Command: "API_KEY=zzzz9999 go test"}},
	}
	Session(s, Options{Secrets: true, Paths: true}, []string{s.CWD})
	if s.CWD != "<cwd>" || s.SourcePath != "sess.jsonl" {
		t.Errorf("cwd=%q source=%q", s.CWD, s.SourcePath)
	}
	if got := s.Commands[0].Text; got != "cd <cwd> && TOKEN=[REDACTED:assignment] make" {
		t.Errorf("command = %q", got)
	}
	if s.Commands[0].CWD != "<cwd>/sub" {
		t.Errorf("command cwd = %q", s.Commands[0].CWD)
	}
	if strings.Contains(s.FileChanges[0].Path, "secret-project") || s.FileChanges[1].Path != "rel/y.go" {
		t.Errorf("paths = %q, %q", s.FileChanges[0].Path, s.FileChanges[1].Path)
	}
	if strings.Contains(s.Tests[0].Command, "zzzz9999") {
		t.Errorf("test command = %q", s.Tests[0].Command)
	}
}

func TestSessionNoop(t *testing.T) {
	s := &model.Session{CWD: "/x", Commands: []model.Command{{Text: "TOKEN=abcdef123456"}}}
	Session(s, Options{}, nil)
	if s.CWD != "/x" || s.Commands[0].Text != "TOKEN=abcdef123456" {
		t.Error("redaction applied without being requested")
	}
}
