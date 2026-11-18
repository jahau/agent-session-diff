// Command agent-session-diff compares two explicitly supplied AI coding-agent
// session exports and prints a terminal summary, optionally writing a
// standalone HTML report and a JSON report.
//
// It reads the given files only. It never executes logged commands, never
// discovers sessions on its own, and makes no network requests.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/jahau/agent-session-diff/internal/compare"
	"github.com/jahau/agent-session-diff/internal/model"
	"github.com/jahau/agent-session-diff/internal/parse"
	"github.com/jahau/agent-session-diff/internal/redact"
	"github.com/jahau/agent-session-diff/internal/render"
)

// version is set at build time with -ldflags "-X main.version=...". When it
// is not set, the module version recorded by "go install ...@vX.Y.Z" is used.
var version = "dev"

func versionString() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return version
}

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usageText = `agent-session-diff compares two AI coding-agent session exports.

Usage:
  agent-session-diff [flags] <session-a> <session-b>
  agent-session-diff inspect [flags] <session>     print one normalized session as JSON
  agent-session-diff formats                        list supported export formats
  agent-session-diff version

Flags (compare):
  --html FILE          also write a standalone HTML report
  --json FILE          also write the full comparison as JSON ("-" for stdout)
  --format-a FORMAT    format of session A: auto (default), claude-code, codex
  --format-b FORMAT    format of session B
  --label-a NAME       display name for A (default: file name)
  --label-b NAME       display name for B
  --redact             redact secrets and local paths (same as both flags below)
  --redact-secrets     replace tokens, keys and password assignments with [REDACTED:kind]
  --redact-paths       replace the session cwd with <cwd>, home dirs with <home>,
                       other absolute paths with <path:hash>
  --limit N            max rows per section in the terminal summary (default 25, 0 = all)
  --no-color           disable ANSI colors (also honoured: NO_COLOR env var)
  --color              force ANSI colors even when stdout is not a terminal
  --quiet              do not print the terminal summary
  --max-line-bytes N   skip records larger than N bytes (default 33554432)

Exit status: 0 on success, 1 if a file cannot be read or parsed, 2 on usage errors.
Differences between sessions do not change the exit status.
`

type options struct {
	html, jsonOut         string
	formatA, formatB      string
	labelA, labelB        string
	redactAll, redSecrets bool
	redPaths              bool
	limit                 int
	noColor, quiet        bool
	forceColor            bool
	maxLineBytes          int
}

func newFlagSet(stderr io.Writer, o *options) *flag.FlagSet {
	fs := flag.NewFlagSet("agent-session-diff", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.html, "html", "", "")
	fs.StringVar(&o.jsonOut, "json", "", "")
	fs.StringVar(&o.formatA, "format-a", "auto", "")
	fs.StringVar(&o.formatB, "format-b", "auto", "")
	fs.StringVar(&o.labelA, "label-a", "", "")
	fs.StringVar(&o.labelB, "label-b", "", "")
	fs.BoolVar(&o.redactAll, "redact", false, "")
	fs.BoolVar(&o.redSecrets, "redact-secrets", false, "")
	fs.BoolVar(&o.redPaths, "redact-paths", false, "")
	fs.IntVar(&o.limit, "limit", 25, "")
	fs.BoolVar(&o.noColor, "no-color", false, "")
	fs.BoolVar(&o.forceColor, "color", false, "")
	fs.BoolVar(&o.quiet, "quiet", false, "")
	fs.IntVar(&o.maxLineBytes, "max-line-bytes", parse.DefaultMaxLineBytes, "")
	return fs
}

// parseInterspersed lets flags appear before, between or after positionals.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		if args[0] == "--" {
			return append(pos, args[1:]...), nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "version", "--version", "-version":
			fmt.Fprintf(stdout, "agent-session-diff %s\n", versionString())
			return exitOK
		case "help", "--help", "-h", "-help":
			fmt.Fprint(stdout, usageText)
			return exitOK
		case "formats":
			fmt.Fprint(stdout, formatsText)
			return exitOK
		case "inspect":
			return runInspect(args[1:], stdout, stderr)
		}
	}
	var o options
	fs := newFlagSet(stderr, &o)
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, usageText)
			return exitOK
		}
		return usageErr(stderr, err.Error())
	}
	if len(pos) != 2 {
		return usageErr(stderr, fmt.Sprintf("expected exactly two session files, got %d", len(pos)))
	}
	fa, err := parseFormat(o.formatA)
	if err != nil {
		return usageErr(stderr, "--format-a: "+err.Error())
	}
	fb, err := parseFormat(o.formatB)
	if err != nil {
		return usageErr(stderr, "--format-b: "+err.Error())
	}
	if o.limit < 0 || o.maxLineBytes <= 0 {
		return usageErr(stderr, "--limit must be >= 0 and --max-line-bytes > 0")
	}

	a, err := parse.ParseFile(pos[0], parse.Options{Format: fa, MaxLineBytes: o.maxLineBytes, Label: o.labelA})
	if err != nil {
		fmt.Fprintf(stderr, "error: session A: %v\n", err)
		return exitError
	}
	b, err := parse.ParseFile(pos[1], parse.Options{Format: fb, MaxLineBytes: o.maxLineBytes, Label: o.labelB})
	if err != nil {
		fmt.Fprintf(stderr, "error: session B: %v\n", err)
		return exitError
	}

	ro := redact.Options{Secrets: o.redactAll || o.redSecrets, Paths: o.redactAll || o.redPaths}
	var applied []string
	if ro.Secrets {
		applied = append(applied, "secrets")
	}
	if ro.Paths {
		applied = append(applied, "paths")
	}
	cwds := []string{a.CWD, b.CWD}
	sameCWD := a.CWD == b.CWD
	redact.Session(a, ro, cwds)
	redact.Session(b, ro, cwds)
	// Both cwds redact to "<cwd>"; keep the overview honest about whether
	// the original directories were the same.
	if ro.Paths && !sameCWD && a.CWD != "" && b.CWD != "" {
		a.CWD, b.CWD = "<cwd> (A)", "<cwd> (B)"
	}

	rep := compare.Compare(a, b, applied)

	if !o.quiet {
		color := !o.noColor && (o.forceColor || (os.Getenv("NO_COLOR") == "" && isTerminal(stdout)))
		if err := render.Text(stdout, rep, render.TextOptions{Color: color, Limit: o.limit}); err != nil {
			fmt.Fprintf(stderr, "error: writing summary: %v\n", err)
			return exitError
		}
	}
	if o.html != "" {
		if err := writeFile(o.html, func(w io.Writer) error { return render.HTML(w, rep) }); err != nil {
			fmt.Fprintf(stderr, "error: writing HTML report: %v\n", err)
			return exitError
		}
		if !o.quiet {
			fmt.Fprintf(stdout, "\nHTML report written to %s\n", o.html)
		}
	}
	if o.jsonOut != "" {
		write := func(w io.Writer) error { return writeJSON(w, rep) }
		if o.jsonOut == "-" {
			err = write(stdout)
		} else {
			err = writeFile(o.jsonOut, write)
		}
		if err != nil {
			fmt.Fprintf(stderr, "error: writing JSON report: %v\n", err)
			return exitError
		}
	}
	return exitOK
}

func runInspect(args []string, stdout, stderr io.Writer) int {
	var o options
	fs := newFlagSet(stderr, &o)
	fs.StringVar(&o.formatA, "format", "auto", "")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return usageErr(stderr, err.Error())
	}
	if len(pos) != 1 {
		return usageErr(stderr, "inspect expects exactly one session file")
	}
	f, err := parseFormat(o.formatA)
	if err != nil {
		return usageErr(stderr, "--format: "+err.Error())
	}
	s, err := parse.ParseFile(pos[0], parse.Options{Format: f, MaxLineBytes: o.maxLineBytes, Label: o.labelA})
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitError
	}
	redact.Session(s, redact.Options{Secrets: o.redactAll || o.redSecrets, Paths: o.redactAll || o.redPaths}, []string{s.CWD})
	if err := writeJSON(stdout, s); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitError
	}
	return exitOK
}

func usageErr(stderr io.Writer, msg string) int {
	fmt.Fprintf(stderr, "error: %s\n\nRun 'agent-session-diff --help' for usage.\n", msg)
	return exitUsage
}

func parseFormat(s string) (model.Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return model.FormatAuto, nil
	case "claude-code", "claude":
		return model.FormatClaudeCode, nil
	case "codex":
		return model.FormatCodex, nil
	}
	return "", fmt.Errorf("unknown format %q (use auto, claude-code or codex)", s)
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func writeFile(path string, fn func(io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := fn(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

const formatsText = `Supported session export formats

claude-code   Claude Code session transcripts (JSONL)
              ~/.claude/projects/<project>/<session-id>.jsonl
              file changes: Write, Edit, MultiEdit, NotebookEdit tool calls
              commands:     Bash, PowerShell tool calls
              not recorded: exit code of successful commands, command duration,
                            per-command working directory

codex         OpenAI Codex CLI rollout files (JSONL)
              ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl
              file changes: FileChange items, apply_patch calls
              commands:     CommandExecution items; shell, shell_command,
                            exec_command calls
              not decoded:  unified-exec "exec" script cells (commands they run
                            are visible only through CommandExecution items)

Format is detected from the first records; override with --format-a/--format-b.
Changes made by shell commands (sed -i, rm, git checkout...) are never inferred.
`
