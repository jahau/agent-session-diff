package parse

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jahau/agent-session-diff/internal/model"
)

const (
	exClaude = "../../examples/claude-code-session.jsonl"
	exCodex  = "../../examples/codex-session.jsonl"
	legacy   = "../../testdata/codex/legacy.jsonl"
)

func mustParse(t *testing.T, p string) *model.Session {
	t.Helper()
	s, err := ParseFile(p, Options{})
	if err != nil {
		t.Fatalf("ParseFile(%s): %v", p, err)
	}
	return s
}

func intv(p *int) string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprint(*p)
}

func TestClaudeCodeFixture(t *testing.T) {
	s := mustParse(t, exClaude)
	if s.Format != model.FormatClaudeCode {
		t.Fatalf("format = %q", s.Format)
	}
	if s.CWD != "/home/dev/widget" || s.AgentVersion != "2.1.40" || s.GitBranch != "fix/port-validation" {
		t.Errorf("session meta = %q %q %q", s.CWD, s.AgentVersion, s.GitBranch)
	}
	if !reflect.DeepEqual(s.Models, []string{"claude-opus-5-5"}) {
		t.Errorf("models = %v", s.Models)
	}
	type fc struct {
		path, op       string
		added, removed string
	}
	var got []fc
	for _, f := range s.FileChanges {
		got = append(got, fc{f.Path, string(f.Op), intv(f.Added), intv(f.Removed)})
	}
	want := []fc{
		{"config/config.go", "modify", "3", "0"},
		{"config/config_test.go", "create", "15", "0"},
		{"config/config.go", "modify", "1", "1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("file changes:\n got %v\nwant %v", got, want)
	}
	if len(s.Commands) != 6 {
		t.Fatalf("commands = %d, want 6", len(s.Commands))
	}
	failed := s.Commands[1]
	if failed.Status != model.StatusFailed || failed.ExitCode == nil || *failed.ExitCode != 1 {
		t.Errorf("failed command = %+v", failed)
	}
	// Successful Claude Code commands carry no exit code: never invent 0.
	if s.Commands[0].ExitCode != nil {
		t.Errorf("successful command has invented exit code %d", *s.Commands[0].ExitCode)
	}
	if s.Commands[4].Status != model.StatusRejected {
		t.Errorf("deploy command status = %s, want rejected", s.Commands[4].Status)
	}
	for _, c := range s.Commands {
		if c.DurationMS != nil {
			t.Errorf("duration invented for %q", c.Text)
		}
	}
	if len(s.Tests) != 4 || s.Tests[1].Outcome != "failed" || intv(s.Tests[1].Failed) != "1" || intv(s.Tests[1].Passed) != "1" {
		t.Errorf("tests = %+v", s.Tests)
	}
	if s.Tests[1].CommandSeq != 2 {
		t.Errorf("test command seq = %d, want 2", s.Tests[1].CommandSeq)
	}
	if !reflect.DeepEqual(s.OtherTools, []model.NamedCount{{Name: "Read", Count: 1}}) {
		t.Errorf("other tools = %v", s.OtherTools)
	}
}

func TestCodexUnifiedFixture(t *testing.T) {
	s := mustParse(t, exCodex)
	if s.Format != model.FormatCodex || s.AgentVersion != "0.98.0" || s.GitBranch != "fix/port-validation" {
		t.Fatalf("meta = %q %q %q", s.Format, s.AgentVersion, s.GitBranch)
	}
	// apply_patch call_B2 is completed by FileChange item call_B2: counted once.
	if len(s.FileChanges) != 2 {
		t.Fatalf("file changes = %d, want 2 (apply_patch must dedupe against FileChange)", len(s.FileChanges))
	}
	if s.FileChanges[0].Path != "config/config.go" || s.FileChanges[1].Path != "config/validate.go" {
		t.Errorf("paths = %q %q (windows paths must be made relative to cwd)", s.FileChanges[0].Path, s.FileChanges[1].Path)
	}
	if s.FileChanges[0].Tool != "FileChange" {
		t.Errorf("tool = %q", s.FileChanges[0].Tool)
	}
	if len(s.Commands) != 5 {
		t.Fatalf("commands = %d", len(s.Commands))
	}
	c := s.Commands[0]
	if c.Text != "go test ./..." || intv(c.ExitCode) != "0" || c.DurationMS == nil || *c.DurationMS != 1210 || c.TimestampKind != "completion" {
		t.Errorf("command = %+v", c)
	}
	// Removing a file via a shell command is NOT turned into a file change.
	for _, f := range s.FileChanges {
		if strings.Contains(f.Path, "legacy_port") {
			t.Errorf("file change inferred from shell command: %+v", f)
		}
	}
	if !reflect.DeepEqual(s.OtherTools, []model.NamedCount{{Name: "exec", Count: 2}}) {
		t.Errorf("other tools = %v", s.OtherTools)
	}
	var cmdSupport model.FieldSupport
	for _, f := range s.Support {
		if f.Field == "commands" {
			cmdSupport = f
		}
	}
	if cmdSupport.Level != model.SupportPartial || !strings.Contains(cmdSupport.Note, "exec") {
		t.Errorf("commands support = %+v, want partial with exec note", cmdSupport)
	}
}

func TestCodexLegacyFixture(t *testing.T) {
	s := mustParse(t, legacy)
	if len(s.Commands) != 4 {
		t.Fatalf("commands = %d", len(s.Commands))
	}
	pytest, npm, cargo, deploy := s.Commands[0], s.Commands[1], s.Commands[2], s.Commands[3]
	if pytest.Text != "pytest -q" || intv(pytest.ExitCode) != "1" || *pytest.DurationMS != 500 || pytest.CWD != "/srv/app" {
		t.Errorf("pytest = %+v", pytest)
	}
	if npm.Status != model.StatusSucceeded || *npm.DurationMS != 2300 {
		t.Errorf("npm = %+v", npm)
	}
	if cargo.Status != model.StatusFailed || intv(cargo.ExitCode) != "101" {
		t.Errorf("cargo = %+v", cargo)
	}
	if deploy.Status != model.StatusUnknown || deploy.ExitCode != nil {
		t.Errorf("command without output must be unknown, got %+v", deploy)
	}
	wantTests := []string{"pytest failed 3/1", "npm test passed 4/0", "cargo test failed 7/1"}
	var gotTests []string
	for _, tr := range s.Tests {
		gotTests = append(gotTests, fmt.Sprintf("%s %s %s/%s", tr.Runner, tr.Outcome, intv(tr.Passed), intv(tr.Failed)))
	}
	if !reflect.DeepEqual(gotTests, wantTests) {
		t.Errorf("tests = %v, want %v", gotTests, wantTests)
	}
	var ops []string
	for _, f := range s.FileChanges {
		ops = append(ops, fmt.Sprintf("%s:%s:%s:%s", f.Op, f.Path, f.NewPath, f.Status))
	}
	wantOps := []string{
		"rename:api/limits.py:api/rate_limits.py:succeeded",
		"delete:api/old.py::succeeded",
		"modify:api/views.py::failed",
	}
	if !reflect.DeepEqual(ops, wantOps) {
		t.Errorf("ops = %v, want %v", ops, wantOps)
	}
}

func TestMalformedMixed(t *testing.T) {
	s := mustParse(t, "../../testdata/malformed/mixed.jsonl")
	if s.Format != model.FormatClaudeCode {
		t.Fatalf("format = %q (BOM/CRLF must not break detection)", s.Format)
	}
	if s.MalformedRecords != 5 {
		t.Errorf("malformed = %d, want 5", s.MalformedRecords)
	}
	if s.Records != 6 {
		t.Errorf("records = %d, want 6", s.Records)
	}
	if len(s.Commands) != 1 || s.Commands[0].Text != "echo ok" || s.Commands[0].Status != model.StatusSucceeded {
		t.Errorf("commands = %+v", s.Commands)
	}
	if len(s.FileChanges) != 1 || s.FileChanges[0].Status != model.StatusUnknown || s.FileChanges[0].Path != "a.txt" {
		t.Errorf("file changes = %+v", s.FileChanges)
	}
	if s.WarningsTotal == 0 || len(s.Warnings) == 0 {
		t.Errorf("expected warnings")
	}
	if s.MissingTimes != 2 {
		t.Errorf("records without valid timestamp = %d, want 2", s.MissingTimes)
	}
}

func TestTruncatedFinalRecord(t *testing.T) {
	s := mustParse(t, "../../testdata/claude-code/truncated.jsonl")
	if s.MalformedRecords != 1 || len(s.Commands) != 1 {
		t.Errorf("malformed=%d commands=%d", s.MalformedRecords, len(s.Commands))
	}
}

func TestUnrecognizedInputs(t *testing.T) {
	for _, f := range []string{"empty.jsonl", "plain.txt", "binary.bin", "unknown-format.jsonl"} {
		t.Run(f, func(t *testing.T) {
			_, err := ParseFile(filepath.Join("../../testdata/malformed", f), Options{})
			if !errors.Is(err, ErrUnknownFormat) {
				t.Fatalf("err = %v, want ErrUnknownFormat", err)
			}
		})
	}
	if _, err := ParseFile("../../testdata/does-not-exist.jsonl", Options{}); err == nil {
		t.Error("missing file: expected error")
	}
	if _, err := ParseFile("../../testdata", Options{}); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Errorf("directory: err = %v", err)
	}
}

func TestFormatOverride(t *testing.T) {
	// Forcing the wrong format yields an empty but valid session, not a crash.
	s, err := ParseFile(exCodex, Options{Format: model.FormatClaudeCode})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Commands) != 0 || len(s.FileChanges) != 0 {
		t.Errorf("forced wrong format produced data: %d cmds %d changes", len(s.Commands), len(s.FileChanges))
	}
	if _, err := Parse(strings.NewReader("{}"), "x", Options{Format: "nope"}); err == nil {
		t.Error("unknown forced format: expected error")
	}
}

func TestDeterministicParse(t *testing.T) {
	for _, p := range []string{exClaude, exCodex, legacy} {
		a, _ := json.Marshal(mustParse(t, p))
		for i := 0; i < 5; i++ {
			b, _ := json.Marshal(mustParse(t, p))
			if !bytes.Equal(a, b) {
				t.Fatalf("%s: parse output differs between runs", p)
			}
		}
	}
}

// claudeLines builds n synthetic Claude Code tool-call pairs.
func claudeLines(n int) string { return claudeLinesFrom(0, n) }

func claudeLinesFrom(from, n int) string {
	var b strings.Builder
	for i := from; i < from+n; i++ {
		ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second).Format(time.RFC3339)
		fmt.Fprintf(&b, `{"type":"assistant","sessionId":"big","timestamp":%q,"message":{"content":[{"type":"tool_use","id":"t%d","name":"Bash","input":{"command":"echo %d"}}]}}`+"\n", ts, i, i%50)
		fmt.Fprintf(&b, `{"type":"user","sessionId":"big","timestamp":%q,"message":{"content":[{"type":"tool_result","tool_use_id":"t%d","content":"ok"}]}}`+"\n", ts, i)
	}
	return b.String()
}

func TestLargeInput(t *testing.T) {
	const n = 50000 // 100k records
	data := claudeLines(n)
	start := time.Now()
	s, err := Parse(strings.NewReader(data), "big", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Commands) != n || s.Records != 2*n {
		t.Fatalf("commands=%d records=%d", len(s.Commands), s.Records)
	}
	if d := time.Since(start); d > 60*time.Second {
		t.Errorf("parsing %d records took %s", 2*n, d)
	}
}

func TestOversizedLineIsSkipped(t *testing.T) {
	big := `{"type":"user","sessionId":"x","message":{"content":"` + strings.Repeat("a", 3<<20) + `"}}`
	data := claudeLines(2) + big + "\n" + claudeLinesFrom(2, 1)
	s, err := Parse(strings.NewReader(data), "x", Options{MaxLineBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if s.MalformedRecords != 1 || len(s.Commands) != 3 {
		t.Fatalf("malformed=%d commands=%d", s.MalformedRecords, len(s.Commands))
	}
	if !strings.Contains(s.Warnings[0].Message, "max-line-bytes") || s.Warnings[0].Line != 5 {
		t.Errorf("warning = %+v", s.Warnings[0])
	}
}

func TestLongLineWithinLimit(t *testing.T) {
	// A 5 MiB record (e.g. a big Write) under the default limit is parsed.
	content := strings.Repeat("x\n", 5<<19)
	line := fmt.Sprintf(`{"type":"assistant","sessionId":"x","message":{"content":[{"type":"tool_use","id":"w","name":"Write","input":{"file_path":"/p/big.txt","content":%q}}]}}`, content)
	s, err := Parse(strings.NewReader(line), "x", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.FileChanges) != 1 || s.FileChanges[0].Op != model.OpWrite {
		t.Fatalf("changes = %+v", s.FileChanges)
	}
}

func TestDeepNestingDoesNotCrash(t *testing.T) {
	deep := strings.Repeat("[", 200000) + strings.Repeat("]", 200000)
	data := `{"type":"user","sessionId":"x","message":{"content":"hi"}}` + "\n" +
		`{"type":"assistant","sessionId":"x","message":{"content":` + deep + `}}` + "\n"
	s, err := Parse(strings.NewReader(data), "x", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s.MalformedRecords != 1 {
		t.Errorf("malformed = %d, want 1", s.MalformedRecords)
	}
}

func TestWarningsAreCapped(t *testing.T) {
	data := `{"type":"user","sessionId":"x","message":{"content":"hi"}}` + "\n" + strings.Repeat("garbage\n", 500)
	s, err := Parse(strings.NewReader(data), "x", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s.WarningsTotal != 500 || len(s.Warnings) != maxStoredWarnings {
		t.Errorf("total=%d stored=%d", s.WarningsTotal, len(s.Warnings))
	}
}

func TestDetectionWindow(t *testing.T) {
	data := strings.Repeat(`{"x":1}`+"\n", detectWindow+5) + `{"type":"user","sessionId":"late","message":{}}` + "\n"
	if _, err := Parse(strings.NewReader(data), "x", Options{}); !errors.Is(err, ErrUnknownFormat) {
		t.Errorf("err = %v", err)
	}
	s, err := Parse(strings.NewReader(data), "x", Options{Format: model.FormatClaudeCode})
	if err != nil || len(s.SessionIDs) != 1 {
		t.Errorf("forced format: err=%v ids=%v", err, s.SessionIDs)
	}
}

func TestNormPath(t *testing.T) {
	cases := []struct{ p, cwd, want string }{
		{"/home/dev/w/a/b.go", "/home/dev/w", "a/b.go"},
		{"/home/dev/w", "/home/dev/w/", "."},
		{"/home/dev/other/x", "/home/dev/w", "/home/dev/other/x"},
		{`C:\Work\Widget\cfg\a.go`, `c:\work\widget`, "cfg/a.go"},
		{`C:\work\widgetx\a.go`, `C:\work\widget`, "C:/work/widgetx/a.go"},
		{"./src/../src/a.go", "/x", "src/a.go"},
		{"rel/a.go", "", "rel/a.go"},
		{"", "/x", ""},
	}
	for _, c := range cases {
		if got := normPath(c.p, c.cwd); got != c.want {
			t.Errorf("normPath(%q, %q) = %q, want %q", c.p, c.cwd, got, c.want)
		}
	}
}

func TestTestRunnerDetection(t *testing.T) {
	cases := map[string]string{
		"go test ./...":                          "go test",
		"cd x && go test -run Foo ./pkg":         "go test",
		"CGO_ENABLED=0 go test ./...":            "go test",
		"python -m pytest tests/ -q":             "pytest",
		"uv run pytest":                          "pytest",
		"npm run test:unit":                      "npm test",
		"pnpm test":                              "pnpm test",
		"npx vitest run":                         "vitest",
		"cargo test --all":                       "cargo test",
		"./gradlew test":                         "gradle",
		"dotnet test":                            "dotnet test",
		"bundle exec rspec spec/a_spec.rb":       "rspec",
		"go vet ./...":                           "",
		"echo \"go test | pytest\"":              "",
		"grep -r 'pytest' .":                     "",
		"git commit -m 'run go test'":            "",
		"go build ./... ; go test ./... 2>&1":    "go test",
		"C:\\Python\\python.exe -m pytest":       "pytest",
		"make test":                              "make test",
		"golangci-lint run && go test -race ./.": "go test",
	}
	for cmd, want := range cases {
		if got := testRunner(cmd); got != want {
			t.Errorf("testRunner(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestTestCounts(t *testing.T) {
	cases := []struct {
		runner, out, want string
	}{
		{"go test", "--- PASS: A\n--- FAIL: B\n--- SKIP: C\n", "1/1/1"},
		{"go test", "ok  \ta\t0.1s\nFAIL\tb\t0.2s\nFAIL\n", "1/1/nil"},
		{"pytest", "....\n4 passed, 1 skipped in 0.3s\n", "4/0/1"},
		{"pytest", "== 2 failed, 3 passed, 1 error in 1.2s ==", "3/3/0"},
		{"jest", "Tests:       1 failed, 2 skipped, 9 passed, 12 total", "9/1/2"},
		{"npm test", "Tests  3 passed (3)", "3/0/0"},
		{"cargo test", "test result: ok. 3 passed; 0 failed; 1 ignored\ntest result: ok. 2 passed; 0 failed; 0 ignored", "5/0/1"},
		{"go test", "no summary here", "none"},
		{"rspec", "10 examples, 0 failures", "none"},
	}
	for _, c := range cases {
		p, f, s, _, ok := testCounts(c.runner, c.out)
		got := "none"
		if ok {
			got = intv(p) + "/" + intv(f) + "/" + intv(s)
		}
		if got != c.want {
			t.Errorf("testCounts(%s, %q) = %s, want %s", c.runner, c.out, got, c.want)
		}
	}
}

func TestCommandFromArgv(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"bash", "-lc", "ls -la"}, "ls -la"},
		{[]string{`C:\WINDOWS\System32\WindowsPowerShell\v1.0\powershell.exe`, "-Command", "dir"}, "dir"},
		{[]string{"cmd.exe", "/c", "dir"}, "dir"},
		{[]string{"git", "commit", "-m", "two words"}, `git commit -m "two words"`},
		{[]string{"rg", ""}, `rg ""`},
	}
	for _, c := range cases {
		if got := commandFromArgv(c.argv); got != c.want {
			t.Errorf("commandFromArgv(%q) = %q, want %q", c.argv, got, c.want)
		}
	}
}

func TestParseApplyPatch(t *testing.T) {
	in := "*** Begin Patch\n*** Add File: a.txt\n+one\n+two\n*** Update File: b.txt\n*** Move to: c.txt\n@@\n-x\n+y\n context\n*** Delete File: d.txt\n*** End Patch\n"
	got := parseApplyPatch(in)
	want := []patchFile{
		{op: "add", path: "a.txt", added: 2},
		{op: "update", path: "b.txt", moveTo: "c.txt", added: 1, removed: 1},
		{op: "delete", path: "d.txt"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestLineReaderNoTrailingNewline(t *testing.T) {
	lr := newLineReader(strings.NewReader("a\r\nb"), 10)
	var lines []string
	for {
		l, _, _, err := lr.next()
		if err != nil {
			break
		}
		lines = append(lines, string(l))
	}
	if !reflect.DeepEqual(lines, []string{"a", "b"}) {
		t.Errorf("lines = %q", lines)
	}
}

func TestReadError(t *testing.T) {
	_, err := Parse(errReader{}, "x", Options{})
	if err == nil {
		t.Fatal("expected read error")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, os.ErrClosed }
