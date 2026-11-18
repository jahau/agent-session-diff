// Package compare builds a deterministic comparison of two normalized
// sessions. The same two inputs always produce an identical Report: rows are
// ordered by path or first appearance, never by map iteration, and nothing
// depends on the current time or host.
package compare

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jahau/agent-session-diff/internal/model"
)

// SchemaVersion is bumped when the JSON report shape changes incompatibly.
const SchemaVersion = 1

// Presence of a row in the two sessions.
const (
	Both  = "both"
	OnlyA = "a-only"
	OnlyB = "b-only"
)

// NotRecorded is shown for values the export does not contain.
const NotRecorded = "not recorded"

// Report is the full comparison.
type Report struct {
	Tool          string         `json:"tool"`
	SchemaVersion int            `json:"schema_version"`
	Redaction     []string       `json:"redaction"`
	Summary       Summary        `json:"summary"`
	Overview      []Row          `json:"overview"`
	Timing        []Row          `json:"timing"`
	Files         []FileRow      `json:"files"`
	Commands      []CommandRow   `json:"commands"`
	Tests         [2]TestSide    `json:"tests"`
	Support       []SupportRow   `json:"field_support"`
	A             *model.Session `json:"a"`
	B             *model.Session `json:"b"`
}

// Row is a labelled pair of values.
type Row struct {
	Field   string `json:"field"`
	A       string `json:"a"`
	B       string `json:"b"`
	Differs bool   `json:"differs"`
}

// Summary holds headline counts.
type Summary struct {
	FilesBoth     int `json:"files_both"`
	FilesDiffer   int `json:"files_both_differ"`
	FilesOnlyA    int `json:"files_only_a"`
	FilesOnlyB    int `json:"files_only_b"`
	CommandsBoth  int `json:"commands_both"`
	CommandsOnlyA int `json:"commands_only_a"`
	CommandsOnlyB int `json:"commands_only_b"`
}

// FileSide aggregates one session's recorded changes to one path.
type FileSide struct {
	Changes int        `json:"changes"`
	Ops     []model.Op `json:"ops"`
	Net     string     `json:"net_effect"`
	Added   int        `json:"lines_added"`
	Removed int        `json:"lines_removed"`
	// CountsComplete is false when some change lacks recorded line counts;
	// Added/Removed then cover only the changes that have them.
	CountsComplete bool `json:"line_counts_complete"`
	// Counted is how many applied changes have recorded line counts.
	Counted    int      `json:"changes_with_line_counts"`
	NotApplied int      `json:"changes_not_applied"`
	RenamedTo  string   `json:"renamed_to,omitempty"`
	Tools      []string `json:"tools"`
}

// FileRow compares one path.
type FileRow struct {
	Path     string    `json:"path"`
	Presence string    `json:"presence"`
	SameNet  bool      `json:"same_net_effect"`
	A        *FileSide `json:"a,omitempty"`
	B        *FileSide `json:"b,omitempty"`
}

// CommandSide aggregates one session's runs of one command text.
type CommandSide struct {
	Runs      int    `json:"runs"`
	Succeeded int    `json:"succeeded"`
	Failed    int    `json:"failed"`
	Other     int    `json:"other"`
	ExitCodes []int  `json:"exit_codes"`
	FirstSeq  int    `json:"first_seq"`
	LastState string `json:"last_status"`
}

// CommandRow compares one command text.
type CommandRow struct {
	Text       string       `json:"text"`
	Presence   string       `json:"presence"`
	TestRunner string       `json:"test_runner,omitempty"`
	A          *CommandSide `json:"a,omitempty"`
	B          *CommandSide `json:"b,omitempty"`
}

// TestSide summarises one session's test runs.
type TestSide struct {
	Runs    int             `json:"runs"`
	Passed  int             `json:"passed"`
	Failed  int             `json:"failed"`
	Other   int             `json:"other"`
	Last    *model.TestRun  `json:"last,omitempty"`
	Entries []model.TestRun `json:"runs_list"`
}

// SupportRow pairs field-support entries.
type SupportRow struct {
	Field string             `json:"field"`
	A     model.FieldSupport `json:"a"`
	B     model.FieldSupport `json:"b"`
}

// Compare builds the report. It does not modify a or b.
func Compare(a, b *model.Session, redaction []string) *Report {
	if redaction == nil {
		redaction = []string{}
	}
	r := &Report{Tool: "agent-session-diff", SchemaVersion: SchemaVersion, Redaction: redaction, A: a, B: b}
	r.Overview = overview(a, b)
	r.Timing = timing(a, b)
	r.Files = files(a, b)
	r.Commands = commands(a, b)
	r.Tests = [2]TestSide{tests(a), tests(b)}
	r.Support = supportRows(a, b)
	for _, f := range r.Files {
		switch f.Presence {
		case Both:
			r.Summary.FilesBoth++
			if !f.SameNet {
				r.Summary.FilesDiffer++
			}
		case OnlyA:
			r.Summary.FilesOnlyA++
		case OnlyB:
			r.Summary.FilesOnlyB++
		}
	}
	for _, c := range r.Commands {
		switch c.Presence {
		case Both:
			r.Summary.CommandsBoth++
		case OnlyA:
			r.Summary.CommandsOnlyA++
		case OnlyB:
			r.Summary.CommandsOnlyB++
		}
	}
	return r
}

func row(field, a, b string) Row { return Row{Field: field, A: a, B: b, Differs: a != b} }

func orNR(s string) string {
	if s == "" {
		return NotRecorded
	}
	return s
}

func joinOrNR(v []string) string {
	if len(v) == 0 {
		return NotRecorded
	}
	return strings.Join(v, ", ")
}

func overview(a, b *model.Session) []Row {
	cnt := func(s *model.Session, st model.Status) int {
		n := 0
		for _, c := range s.Commands {
			if c.Status == st {
				n++
			}
		}
		return n
	}
	touched := func(s *model.Session) int {
		m := map[string]bool{}
		for _, f := range s.FileChanges {
			m[f.Path] = true
		}
		return len(m)
	}
	lastTest := func(s *model.Session) string {
		if len(s.Tests) == 0 {
			return "no test runs recorded"
		}
		t := s.Tests[len(s.Tests)-1]
		return fmt.Sprintf("%s (%s)", t.Outcome, t.Runner)
	}
	n := func(v int) string { return fmt.Sprint(v) }
	return []Row{
		row("format", string(a.Format), string(b.Format)),
		row("agent version", orNR(a.AgentVersion), orNR(b.AgentVersion)),
		row("session id", joinOrNR(a.SessionIDs), joinOrNR(b.SessionIDs)),
		row("model", joinOrNR(a.Models), joinOrNR(b.Models)),
		row("git branch", orNR(a.GitBranch), orNR(b.GitBranch)),
		row("working directory", orNR(a.CWD), orNR(b.CWD)),
		row("records (lines)", fmt.Sprintf("%d (%d)", a.Records, a.Lines), fmt.Sprintf("%d (%d)", b.Records, b.Lines)),
		row("malformed records", n(a.MalformedRecords), n(b.MalformedRecords)),
		row("file changes", n(len(a.FileChanges)), n(len(b.FileChanges))),
		row("files touched", n(touched(a)), n(touched(b))),
		row("commands", n(len(a.Commands)), n(len(b.Commands))),
		row("commands failed", n(cnt(a, model.StatusFailed)), n(cnt(b, model.StatusFailed))),
		row("commands without result", n(cnt(a, model.StatusUnknown)), n(cnt(b, model.StatusUnknown))),
		row("test runs", n(len(a.Tests)), n(len(b.Tests))),
		row("last test run", lastTest(a), lastTest(b)),
	}
}

// FormatTime renders a timestamp deterministically (UTC, second precision).
func FormatTime(t *time.Time) string {
	if t == nil {
		return NotRecorded
	}
	return t.UTC().Format("2006-01-02 15:04:05Z")
}

// FormatDuration renders a duration as e.g. "1h02m03s" or "4.2s".
func FormatDuration(d time.Duration) string {
	if d < 0 {
		return "-" + FormatDuration(-d)
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	d = d.Round(time.Second)
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	}
	return fmt.Sprintf("%dm%02ds", m, s)
}

func offset(s *model.Session, t *time.Time) string {
	if t == nil || s.FirstTime == nil {
		return NotRecorded
	}
	return "+" + FormatDuration(t.Sub(*s.FirstTime))
}

func timing(a, b *model.Session) []Row {
	span := func(s *model.Session) string {
		if s.FirstTime == nil || s.LastTime == nil {
			return NotRecorded
		}
		return FormatDuration(s.LastTime.Sub(*s.FirstTime))
	}
	firstFC := func(s *model.Session, last bool) *time.Time {
		var out *time.Time
		for i := range s.FileChanges {
			if t := s.FileChanges[i].Timestamp; t != nil {
				if out == nil || (!last && t.Before(*out)) || (last && t.After(*out)) {
					out = t
				}
			}
		}
		return out
	}
	firstCmd := func(s *model.Session) *time.Time {
		for _, c := range s.Commands {
			if c.Timestamp != nil {
				return c.Timestamp
			}
		}
		return nil
	}
	firstTest := func(s *model.Session, passing bool) *time.Time {
		for _, t := range s.Tests {
			if t.Timestamp != nil && (!passing || t.Outcome == "passed") {
				return t.Timestamp
			}
		}
		return nil
	}
	durSum := func(s *model.Session) string {
		var sum int64
		n := 0
		for _, c := range s.Commands {
			if c.DurationMS != nil {
				sum += *c.DurationMS
				n++
			}
		}
		if n == 0 {
			return NotRecorded
		}
		return fmt.Sprintf("%s (%d/%d commands)", FormatDuration(time.Duration(sum)*time.Millisecond), n, len(s.Commands))
	}
	return []Row{
		row("first record", FormatTime(a.FirstTime), FormatTime(b.FirstTime)),
		row("last record", FormatTime(a.LastTime), FormatTime(b.LastTime)),
		row("span (first to last record)", span(a), span(b)),
		row("first file change", offset(a, firstFC(a, false)), offset(b, firstFC(b, false))),
		row("last file change", offset(a, firstFC(a, true)), offset(b, firstFC(b, true))),
		row("first command", offset(a, firstCmd(a)), offset(b, firstCmd(b))),
		row("first test run", offset(a, firstTest(a, false)), offset(b, firstTest(b, false))),
		row("first passing test run", offset(a, firstTest(a, true)), offset(b, firstTest(b, true))),
		row("recorded command time", durSum(a), durSum(b)),
	}
}

func applied(st model.Status) bool {
	return st != model.StatusFailed && st != model.StatusRejected && st != model.StatusInterrupted
}

func fileSides(s *model.Session) map[string]*FileSide {
	out := map[string]*FileSide{}
	for _, fc := range s.FileChanges {
		fs := out[fc.Path]
		if fs == nil {
			fs = &FileSide{CountsComplete: true}
			out[fc.Path] = fs
		}
		fs.Changes++
		fs.Ops = append(fs.Ops, fc.Op)
		if !containsStr(fs.Tools, fc.Tool) {
			fs.Tools = append(fs.Tools, fc.Tool)
		}
		if !applied(fc.Status) {
			fs.NotApplied++
			continue
		}
		if fc.Added != nil && fc.Removed != nil {
			fs.Counted++
			fs.Added += *fc.Added
			fs.Removed += *fc.Removed
		} else {
			fs.CountsComplete = false
		}
		if fc.Op == model.OpRename {
			fs.RenamedTo = fc.NewPath
		}
	}
	// Net effect from the applied (or unknown-status) changes, in order.
	for p, fs := range out {
		var ops []model.Op
		for _, fc := range s.FileChanges {
			if fc.Path == p && applied(fc.Status) {
				ops = append(ops, fc.Op)
			}
		}
		fs.Net = netEffect(ops)
		sort.Strings(fs.Tools)
	}
	return out
}

func netEffect(ops []model.Op) string {
	if len(ops) == 0 {
		return "not applied"
	}
	first, last := ops[0], ops[len(ops)-1]
	renamed := false
	for _, o := range ops {
		if o == model.OpRename {
			renamed = true
		}
	}
	switch {
	case last == model.OpDelete && first == model.OpCreate:
		return "created then deleted"
	case last == model.OpDelete:
		return "deleted"
	case renamed:
		return "renamed"
	case first == model.OpCreate:
		return "created"
	case first == model.OpWrite && len(ops) == 1:
		return "written"
	default:
		return "modified"
	}
}

func containsStr(v []string, s string) bool {
	for _, x := range v {
		if x == s {
			return true
		}
	}
	return false
}

func files(a, b *model.Session) []FileRow {
	fa, fb := fileSides(a), fileSides(b)
	paths := map[string]bool{}
	for p := range fa {
		paths[p] = true
	}
	for p := range fb {
		paths[p] = true
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	rows := make([]FileRow, 0, len(sorted))
	for _, p := range sorted {
		r := FileRow{Path: p, A: fa[p], B: fb[p]}
		switch {
		case r.A != nil && r.B != nil:
			r.Presence = Both
			r.SameNet = r.A.Net == r.B.Net && r.A.RenamedTo == r.B.RenamedTo
		case r.A != nil:
			r.Presence = OnlyA
		default:
			r.Presence = OnlyB
		}
		rows = append(rows, r)
	}
	return rows
}

// CommandKey normalizes command text for matching: whitespace runs collapse
// to one space. Nothing else is rewritten.
func CommandKey(s string) string { return strings.Join(strings.Fields(s), " ") }

func commandSides(s *model.Session) (map[string]*CommandSide, []string, map[string]string) {
	out := map[string]*CommandSide{}
	runner := map[string]string{}
	var order []string
	for _, c := range s.Commands {
		k := CommandKey(c.Text)
		cs := out[k]
		if cs == nil {
			cs = &CommandSide{FirstSeq: c.Seq}
			out[k] = cs
			order = append(order, k)
			runner[k] = c.TestRunner
		}
		cs.Runs++
		switch c.Status {
		case model.StatusSucceeded:
			cs.Succeeded++
		case model.StatusFailed:
			cs.Failed++
		default:
			cs.Other++
		}
		cs.LastState = string(c.Status)
		if c.ExitCode != nil && !containsInt(cs.ExitCodes, *c.ExitCode) {
			cs.ExitCodes = append(cs.ExitCodes, *c.ExitCode)
		}
	}
	for _, cs := range out {
		sort.Ints(cs.ExitCodes)
		if cs.ExitCodes == nil {
			cs.ExitCodes = []int{}
		}
	}
	return out, order, runner
}

func containsInt(v []int, n int) bool {
	for _, x := range v {
		if x == n {
			return true
		}
	}
	return false
}

func commands(a, b *model.Session) []CommandRow {
	ca, orderA, runA := commandSides(a)
	cb, orderB, runB := commandSides(b)
	var rows []CommandRow
	for _, k := range orderA {
		r := CommandRow{Text: k, A: ca[k], B: cb[k], TestRunner: runA[k]}
		if r.B != nil {
			r.Presence = Both
		} else {
			r.Presence = OnlyA
		}
		rows = append(rows, r)
	}
	for _, k := range orderB {
		if ca[k] != nil {
			continue
		}
		rows = append(rows, CommandRow{Text: k, B: cb[k], Presence: OnlyB, TestRunner: runB[k]})
	}
	if rows == nil {
		rows = []CommandRow{}
	}
	return rows
}

func tests(s *model.Session) TestSide {
	t := TestSide{Runs: len(s.Tests), Entries: s.Tests}
	for _, r := range s.Tests {
		switch r.Outcome {
		case "passed":
			t.Passed++
		case "failed":
			t.Failed++
		default:
			t.Other++
		}
	}
	if n := len(s.Tests); n > 0 {
		last := s.Tests[n-1]
		t.Last = &last
	}
	return t
}

func supportRows(a, b *model.Session) []SupportRow {
	idx := func(s *model.Session) map[string]model.FieldSupport {
		m := map[string]model.FieldSupport{}
		for _, f := range s.Support {
			m[f.Field] = f
		}
		return m
	}
	ia, ib := idx(a), idx(b)
	var fields []string
	seen := map[string]bool{}
	for _, s := range []*model.Session{a, b} {
		for _, f := range s.Support {
			if !seen[f.Field] {
				seen[f.Field] = true
				fields = append(fields, f.Field)
			}
		}
	}
	rows := make([]SupportRow, 0, len(fields))
	for _, f := range fields {
		ra, ok := ia[f]
		if !ok {
			ra = model.FieldSupport{Field: f, Level: model.SupportUnsupported}
		}
		rb, ok := ib[f]
		if !ok {
			rb = model.FieldSupport{Field: f, Level: model.SupportUnsupported}
		}
		rows = append(rows, SupportRow{Field: f, A: ra, B: rb})
	}
	return rows
}

// Limited reports whether a support row is anything other than fully
// recorded (or not applicable) on either side.
func (s SupportRow) Limited() bool {
	ok := func(l string) bool { return l == model.SupportRecorded || l == model.SupportNotApplicable }
	return !ok(s.A.Level) || !ok(s.B.Level)
}
