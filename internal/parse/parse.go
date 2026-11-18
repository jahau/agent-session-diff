// Package parse reads supported agent session exports into model.Session.
//
// Parsers only record what the export states. They never execute, replay or
// simulate logged commands, and they do not infer file changes from shell
// command text.
package parse

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jahau/agent-session-diff/internal/model"
)

// DefaultMaxLineBytes bounds the size of a single JSONL record.
const DefaultMaxLineBytes = 32 << 20

// maxStoredWarnings bounds how many warnings are kept verbatim.
const maxStoredWarnings = 50

// detectWindow is how many non-empty lines are inspected to detect a format.
const detectWindow = 200

// Options controls parsing.
type Options struct {
	Format       model.Format // FormatAuto to detect
	MaxLineBytes int          // 0 means DefaultMaxLineBytes
	Label        string       // display label; defaults to the file base name
}

// ErrUnknownFormat is returned when auto-detection finds no supported format.
var ErrUnknownFormat = errors.New("unrecognized session export format")

// ParseFile opens and parses one export file.
func ParseFile(p string, opt Options) (*model.Session, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.IsDir() {
		return nil, fmt.Errorf("%s: is a directory; pass a session export file", p)
	}
	if opt.Label == "" {
		opt.Label = filepath.Base(p)
	}
	s, err := Parse(f, p, opt)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return s, nil
}

// line is one raw input line.
type line struct {
	no   int
	data []byte
}

// formatParser consumes decoded records of one format.
type formatParser interface {
	record(no int, rec obj)
	finish()
}

// Parse reads an export from r. sourcePath is recorded for display only.
func Parse(r io.Reader, sourcePath string, opt Options) (*model.Session, error) {
	if opt.MaxLineBytes <= 0 {
		opt.MaxLineBytes = DefaultMaxLineBytes
	}
	b := newBuilder(sourcePath, opt.Label)
	lr := newLineReader(r, opt.MaxLineBytes)

	format := opt.Format
	var pending []line
	var fp formatParser

	start := func(f model.Format) error {
		switch f {
		case model.FormatClaudeCode:
			fp = newClaudeParser(b)
		case model.FormatCodex:
			fp = newCodexParser(b)
		default:
			return fmt.Errorf("unsupported format %q (supported: %s)", f, supportedList())
		}
		b.s.Format = f
		for _, l := range pending {
			b.feed(fp, l.no, l.data)
		}
		pending = nil
		return nil
	}
	if format != model.FormatAuto {
		if err := start(format); err != nil {
			return nil, err
		}
	}

	nonEmpty := 0
	for {
		data, no, tooLong, err := lr.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		b.s.Lines = no
		if tooLong {
			b.s.MalformedRecords++
			b.warn(no, fmt.Sprintf("record exceeds --max-line-bytes (%d bytes); skipped", opt.MaxLineBytes))
			continue
		}
		if len(strings.TrimSpace(string(data))) == 0 {
			continue
		}
		if fp != nil {
			b.feed(fp, no, data)
			continue
		}
		cp := make([]byte, len(data))
		copy(cp, data)
		pending = append(pending, line{no, cp})
		nonEmpty++
		if f := detect(pending); f != model.FormatAuto {
			if err := start(f); err != nil {
				return nil, err
			}
		} else if nonEmpty >= detectWindow {
			return nil, fmt.Errorf("%w: no %s records in the first %d lines", ErrUnknownFormat, supportedList(), detectWindow)
		}
	}
	if fp == nil {
		if nonEmpty == 0 {
			return nil, fmt.Errorf("%w: file is empty", ErrUnknownFormat)
		}
		return nil, fmt.Errorf("%w: no %s records found", ErrUnknownFormat, supportedList())
	}
	fp.finish()
	b.finalize()
	return b.s, nil
}

func supportedList() string {
	names := make([]string, len(model.Formats))
	for i, f := range model.Formats {
		names[i] = string(f)
	}
	return strings.Join(names, ", ")
}

// detect returns the format once any decodable record is characteristic of
// one, or FormatAuto if undecided.
func detect(lines []line) model.Format {
	l := lines[len(lines)-1]
	o, ok := decodeObj(l.data)
	if !ok {
		return model.FormatAuto
	}
	typ := o.s("type")
	if _, hasPayload := o["payload"]; hasPayload {
		switch typ {
		case "session_meta", "response_item", "event_msg", "turn_context", "compacted":
			return model.FormatCodex
		}
	}
	if _, ok := o.str("sessionId"); ok {
		return model.FormatClaudeCode
	}
	if typ == "user" || typ == "assistant" {
		if _, ok := o["message"]; ok {
			return model.FormatClaudeCode
		}
	}
	return model.FormatAuto
}

// builder accumulates a session; format parsers append to it.
type builder struct {
	s          *model.Session
	sessionIDs map[string]bool
	models     map[string]bool
	notInterp  map[string]int
	otherTools map[string]int
	cwdKnown   bool
}

func newBuilder(sourcePath, label string) *builder {
	return &builder{
		s:          &model.Session{SourcePath: sourcePath, Label: label},
		sessionIDs: map[string]bool{},
		models:     map[string]bool{},
		notInterp:  map[string]int{},
		otherTools: map[string]int{},
	}
}

func (b *builder) feed(fp formatParser, no int, data []byte) {
	o, ok := decodeObj(data)
	if !ok {
		b.s.MalformedRecords++
		b.warn(no, "not a JSON object; skipped")
		return
	}
	b.s.Records++
	if t, ok := o.timestamp("timestamp"); ok {
		b.observeTime(t)
	} else {
		b.s.MissingTimes++
	}
	fp.record(no, o)
}

func (b *builder) observeTime(t *time.Time) {
	if b.s.FirstTime == nil || t.Before(*b.s.FirstTime) {
		tt := *t
		b.s.FirstTime = &tt
	}
	if b.s.LastTime == nil || t.After(*b.s.LastTime) {
		tt := *t
		b.s.LastTime = &tt
	}
}

func (b *builder) warn(no int, msg string) {
	b.s.WarningsTotal++
	if len(b.s.Warnings) < maxStoredWarnings {
		b.s.Warnings = append(b.s.Warnings, model.Warning{Line: no, Message: msg})
	}
}

func (b *builder) sessionID(id string) {
	if id != "" {
		b.sessionIDs[id] = true
	}
}

func (b *builder) model(m string) {
	if m != "" && !strings.HasPrefix(m, "<") {
		b.models[m] = true
	}
}

func (b *builder) setCWD(cwd string) {
	if cwd != "" && !b.cwdKnown {
		b.s.CWD = cwd
		b.cwdKnown = true
	}
}

func (b *builder) setVersion(v string) {
	if v != "" && b.s.AgentVersion == "" {
		b.s.AgentVersion = v
	}
}

func (b *builder) setBranch(v string) {
	if v != "" && b.s.GitBranch == "" {
		b.s.GitBranch = v
	}
}

func (b *builder) notInterpreted(kind string) {
	if kind == "" {
		kind = "(no type)"
	}
	b.notInterp[kind]++
}

func (b *builder) otherTool(name string) {
	if name == "" {
		name = "(unnamed)"
	}
	b.otherTools[name]++
}

// orderKey builds a unique, sortable position from a source line and the
// index of the block within that line.
func orderKey(line, block int) int { return line*4096 + block }

// addCommand records a command. output is the recorded command output (used
// only to read a test-runner summary line, never stored); pass "" if none.
func (b *builder) addCommand(c model.Command, output string) {
	c.Text = strings.TrimSpace(c.Text)
	if c.Order == 0 {
		c.Order = orderKey(c.Line, 0)
	}
	c.TestRunner = testRunner(c.Text)
	b.s.Commands = append(b.s.Commands, c)
	if c.TestRunner == "" {
		return
	}
	tr := model.TestRun{
		CommandSeq: c.Order, // replaced by the command's Seq in finalize
		Command:    c.Text,
		Runner:     c.TestRunner,
		Outcome:    testOutcome(c.Status),
		Timestamp:  c.Timestamp,
	}
	if p, f, sk, src, ok := testCounts(c.TestRunner, output); ok {
		tr.Passed, tr.Failed, tr.Skipped, tr.CountsSource = p, f, sk, src
	}
	b.s.Tests = append(b.s.Tests, tr)
}

func testOutcome(s model.Status) model.Status {
	switch s {
	case model.StatusSucceeded:
		return "passed"
	case model.StatusFailed:
		return "failed"
	default:
		return s
	}
}

func (b *builder) addFileChange(fc model.FileChange) {
	if fc.Order == 0 {
		fc.Order = orderKey(fc.Line, 0)
	}
	fc.Path = normPath(fc.Path, b.s.CWD)
	if fc.NewPath != "" {
		fc.NewPath = normPath(fc.NewPath, b.s.CWD)
	}
	b.s.FileChanges = append(b.s.FileChanges, fc)
}

// finalize orders everything by source position and fills summary fields.
func (b *builder) finalize() {
	s := b.s
	sort.SliceStable(s.Commands, func(i, j int) bool { return s.Commands[i].Order < s.Commands[j].Order })
	seqByOrder := map[int]int{}
	for i := range s.Commands {
		s.Commands[i].Seq = i + 1
		seqByOrder[s.Commands[i].Order] = i + 1
	}
	sort.SliceStable(s.Tests, func(i, j int) bool { return s.Tests[i].CommandSeq < s.Tests[j].CommandSeq })
	for i := range s.Tests {
		s.Tests[i].CommandSeq = seqByOrder[s.Tests[i].CommandSeq]
	}
	sort.SliceStable(s.FileChanges, func(i, j int) bool { return s.FileChanges[i].Order < s.FileChanges[j].Order })
	for i := range s.FileChanges {
		s.FileChanges[i].Seq = i + 1
		// Paths may have been recorded before the session cwd was known.
		s.FileChanges[i].Path = normPath(s.FileChanges[i].Path, s.CWD)
		if s.FileChanges[i].NewPath != "" {
			s.FileChanges[i].NewPath = normPath(s.FileChanges[i].NewPath, s.CWD)
		}
	}
	s.SessionIDs = sortedKeys(b.sessionIDs)
	s.Models = sortedKeys(b.models)
	s.NotInterpreted = sortedCounts(b.notInterp)
	s.OtherTools = sortedCounts(b.otherTools)
	if s.Commands == nil {
		s.Commands = []model.Command{}
	}
	if s.FileChanges == nil {
		s.FileChanges = []model.FileChange{}
	}
	if s.Tests == nil {
		s.Tests = []model.TestRun{}
	}
	if s.Warnings == nil {
		s.Warnings = []model.Warning{}
	}
	s.Support = support(s)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedCounts(m map[string]int) []model.NamedCount {
	out := make([]model.NamedCount, 0, len(m))
	for k, v := range m {
		out = append(out, model.NamedCount{Name: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// normPath makes a recorded path comparable across sessions: separators become
// "/", and paths inside the session cwd become relative to it. Windows drive
// paths compare case-insensitively against the cwd. The host OS is never
// consulted, so results do not depend on where the tool runs.
func normPath(p, cwd string) string {
	if p == "" {
		return p
	}
	p = strings.ReplaceAll(p, "\\", "/")
	isAbs := strings.HasPrefix(p, "/") || isDrivePath(p)
	if isAbs {
		p = cleanSlash(p)
	}
	if cwd != "" && isAbs {
		c := cleanSlash(strings.ReplaceAll(cwd, "\\", "/"))
		pp, cc := p, c
		if isDrivePath(p) || isDrivePath(c) {
			pp, cc = strings.ToLower(p), strings.ToLower(c)
		}
		if pp == cc {
			return "."
		}
		prefix := strings.TrimSuffix(cc, "/") + "/"
		if strings.HasPrefix(pp, prefix) {
			return p[len(prefix):]
		}
		return p
	}
	if !isAbs {
		return path.Clean(p)
	}
	return p
}

func cleanSlash(p string) string {
	if isDrivePath(p) {
		return p[:2] + path.Clean("/"+p[2:])
	}
	return path.Clean(p)
}

func isDrivePath(p string) bool {
	return len(p) >= 3 && p[1] == ':' && (p[2] == '/' || p[2] == '\\') &&
		((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z'))
}
