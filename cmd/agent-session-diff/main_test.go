package main

import (
	"bytes"
	"flag"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

const (
	exA = "../../examples/claude-code-session.jsonl"
	exB = "../../examples/codex-session.jsonl"
)

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	p := filepath.Join("..", "..", "testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("missing golden file %s (run: go test ./cmd/... -update): %v", p, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from golden file; rerun with -update if the change is intended.\n--- got ---\n%s", name, got)
	}
}

func TestGoldenReports(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"examples", nil},
		{"examples-redacted", []string{"--redact"}},
		{"legacy-vs-example", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			html, js := filepath.Join(dir, "r.html"), filepath.Join(dir, "r.json")
			a, b := exA, exB
			if c.name == "legacy-vs-example" {
				a = "../../testdata/codex/legacy.jsonl"
			}
			args := append([]string{a, b, "--html", html, "--json", js, "--no-color", "--limit", "0"}, c.args...)
			code, out, errOut := runCLI(t, args...)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, errOut)
			}
			// The HTML path line is environment-specific; drop it.
			out = out[:strings.LastIndex(out, "\nHTML report written to")] + "\n"
			golden(t, c.name+".txt", []byte(out))
			h, _ := os.ReadFile(html)
			golden(t, c.name+".html", h)
			j, _ := os.ReadFile(js)
			golden(t, c.name+".json", j)
		})
	}
}

func TestDeterministicOutput(t *testing.T) {
	var first string
	for i := 0; i < 5; i++ {
		code, out, _ := runCLI(t, exA, exB, "--json", "-", "--quiet")
		if code != 0 {
			t.Fatal("nonzero exit")
		}
		if i == 0 {
			first = out
		} else if out != first {
			t.Fatal("JSON output differs between runs")
		}
	}
}

func TestRedactedOutputHasNoSecretsOrPaths(t *testing.T) {
	_, out, _ := runCLI(t, exA, exB, "--redact", "--json", "-", "--no-color")
	for _, leak := range []string{"fake-deploy-token-0000", "fakebearertoken", "/home/dev", `C:\\work`, "examples/"} {
		if strings.Contains(out, leak) {
			t.Errorf("redacted output contains %q", leak)
		}
	}
}

func TestUsageAndErrors(t *testing.T) {
	cases := []struct {
		args []string
		code int
		msg  string
	}{
		{nil, exitUsage, "expected exactly two"},
		{[]string{exA}, exitUsage, "expected exactly two"},
		{[]string{exA, exB, exA}, exitUsage, "expected exactly two"},
		{[]string{"--bogus", exA, exB}, exitUsage, "flag provided but not defined"},
		{[]string{exA, exB, "--format-a", "nope"}, exitUsage, "unknown format"},
		{[]string{exA, exB, "--limit", "-1"}, exitUsage, "--limit"},
		{[]string{exA, "../../testdata/malformed/empty.jsonl"}, exitError, "session B"},
		{[]string{"../../testdata/malformed/plain.txt", exB}, exitError, "unrecognized"},
		{[]string{"missing.jsonl", exB}, exitError, "session A"},
		{[]string{"inspect"}, exitUsage, "exactly one"},
	}
	for _, c := range cases {
		code, _, errOut := runCLI(t, c.args...)
		if code != c.code || !strings.Contains(errOut, c.msg) {
			t.Errorf("run(%q) = %d %q, want %d containing %q", c.args, code, errOut, c.code, c.msg)
		}
	}
}

func TestInfoCommands(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--help"}, {"formats"}} {
		code, out, _ := runCLI(t, args...)
		if code != 0 || out == "" {
			t.Errorf("%v: code %d", args, code)
		}
	}
	code, out, _ := runCLI(t, "inspect", "--redact", exB)
	if code != 0 || !strings.Contains(out, `"format": "codex"`) || strings.Contains(out, `C:\\work`) {
		t.Errorf("inspect: code %d", code)
	}
}

func TestFlagsAfterPositionals(t *testing.T) {
	code, out, _ := runCLI(t, exA, "--quiet", exB)
	if code != 0 || out != "" {
		t.Errorf("code %d out %q", code, out)
	}
}

// TestNoProcessExecution guards the core promise: no package in this module
// can start processes, so logged commands are never executed.
func TestNoProcessExecution(t *testing.T) {
	banned := map[string]bool{"os/exec": true, "syscall": true, "plugin": true, "unsafe": true, "net": true, "net/http": true}
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if banned[path] {
				t.Errorf("%s imports %s", p, path)
			}
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		needle := "Start" + "Process(" // built at runtime so this file does not match itself
		if bytes.Contains(src, []byte(needle)) {
			t.Errorf("%s calls %s", p, needle)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
