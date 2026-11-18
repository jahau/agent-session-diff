package render

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/jahau/agent-session-diff/internal/compare"
	"github.com/jahau/agent-session-diff/internal/model"
)

// TextOptions controls the terminal summary.
type TextOptions struct {
	Color bool
	// Limit caps rows per list section; 0 means no limit.
	Limit int
	// Width is the target line width; 0 means 120.
	Width int
}

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
	ansiMag    = "\x1b[35m"
)

type textWriter struct {
	w   *bufio.Writer
	o   TextOptions
	err error
}

func (t *textWriter) printf(format string, a ...any) {
	if t.err == nil {
		_, t.err = fmt.Fprintf(t.w, format, a...)
	}
}

func (t *textWriter) c(code, s string) string {
	if !t.o.Color || s == "" {
		return s
	}
	return code + s + ansiReset
}

func (t *textWriter) heading(s string) {
	t.printf("\n%s\n", t.c(ansiBold, s))
}

// Text writes the terminal summary.
func Text(w io.Writer, r *compare.Report, o TextOptions) error {
	if o.Width <= 0 {
		o.Width = 120
	}
	t := &textWriter{w: bufio.NewWriter(w), o: o}

	t.printf("%s\n", t.c(ansiBold, "agent-session-diff: comparing two session exports"))
	t.printf("  %s  %s\n", t.c(ansiCyan, "A"), Sanitize(sideTitle(r.A)))
	t.printf("  %s  %s\n", t.c(ansiMag, "B"), Sanitize(sideTitle(r.B)))
	t.printf("  redaction: %s\n", RedactionText(r.Redaction))
	s := r.Summary
	t.printf("  files: %d in both (%d differ), %d only in A, %d only in B\n", s.FilesBoth, s.FilesDiffer, s.FilesOnlyA, s.FilesOnlyB)
	t.printf("  commands: %d in both, %d only in A, %d only in B\n", s.CommandsBoth, s.CommandsOnlyA, s.CommandsOnlyB)
	t.printf("  tests: A %s | B %s\n", TestSideCell(r.Tests[0]), TestSideCell(r.Tests[1]))

	t.heading("OVERVIEW")
	t.pairs(r.Overview)
	t.heading("TIMING")
	t.pairs(r.Timing)

	t.heading(fmt.Sprintf("FILE CHANGES  (= same net effect, ~ differs, A/B only in one session; * line counts partial)"))
	if len(r.Files) == 0 {
		t.printf("  no file changes recorded in either session\n")
	}
	fileRows := make([][]string, 0, len(r.Files))
	marks := make([]string, 0, len(r.Files))
	for _, f := range r.Files {
		marks = append(marks, PresenceMark(f.Presence, f.SameNet))
		fileRows = append(fileRows, []string{f.Path, FileCell(f.A), FileCell(f.B)})
	}
	t.table([]string{"path", "A", "B"}, fileRows, marks, []int{50, 34, 34})

	t.heading("COMMANDS  (never executed; matched by exact text after whitespace normalization)")
	if len(r.Commands) == 0 {
		t.printf("  no commands recorded in either session\n")
	}
	cmdRows := make([][]string, 0, len(r.Commands))
	cmdMarks := make([]string, 0, len(r.Commands))
	for _, c := range r.Commands {
		cmdMarks = append(cmdMarks, PresenceMark(c.Presence, true))
		cmdRows = append(cmdRows, []string{c.Text, CommandCell(c.A), CommandCell(c.B)})
	}
	t.table([]string{"command", "A", "B"}, cmdRows, cmdMarks, []int{64, 24, 24})

	t.heading("TEST RUNS  (detected from command text; outcome = recorded command status)")
	for i, side := range r.Tests {
		name := "A"
		if i == 1 {
			name = "B"
		}
		t.printf("  %s  %s\n", name, TestSideCell(side))
		rows := make([][]string, 0, len(side.Entries))
		for _, e := range side.Entries {
			rows = append(rows, []string{fmt.Sprintf("#%d", e.CommandSeq), outcomeLabel(e.Outcome), e.Runner, TestCountsCell(e), e.Command})
		}
		if len(rows) > 0 {
			t.table([]string{"cmd", "outcome", "runner", "counts", "command"}, rows, nil, []int{6, 11, 12, 30, 50})
		}
	}

	t.heading("UNSUPPORTED OR PARTIAL FIELDS  (values the exports do not fully record)")
	var supRows [][]string
	for _, sr := range r.Support {
		if !sr.Limited() {
			continue
		}
		supRows = append(supRows, []string{sr.Field, SupportCell(sr.A), SupportCell(sr.B)})
	}
	if len(supRows) == 0 {
		t.printf("  none\n")
	} else {
		t.tableNoLimit([]string{"field", "A", "B"}, supRows, nil, []int{26, 40, 40})
		t.printf("  %s\n", t.c(ansiDim, "Notes per field are in the HTML and JSON reports."))
	}

	for i, s := range []*model.Session{r.A, r.B} {
		name := "A"
		if i == 1 {
			name = "B"
		}
		if s.WarningsTotal == 0 {
			continue
		}
		t.heading(fmt.Sprintf("WARNINGS %s  (%d)", name, s.WarningsTotal))
		for _, wn := range s.Warnings {
			t.printf("  line %d: %s\n", wn.Line, Sanitize(wn.Message))
		}
		if s.WarningsTotal > len(s.Warnings) {
			t.printf("  ... %d more\n", s.WarningsTotal-len(s.Warnings))
		}
	}
	if t.err != nil {
		return t.err
	}
	return t.w.Flush()
}

func outcomeLabel(s model.Status) string { return string(s) }

func (t *textWriter) pairs(rows []compare.Row) {
	data := make([][]string, len(rows))
	marks := make([]string, len(rows))
	for i, r := range rows {
		data[i] = []string{r.Field, r.A, r.B}
		if r.Differs {
			marks[i] = "~"
		} else {
			marks[i] = "="
		}
	}
	t.tableNoLimit([]string{"field", "A", "B"}, data, marks, []int{28, 44, 44})
}

func (t *textWriter) table(head []string, rows [][]string, marks []string, maxw []int) {
	limit := t.o.Limit
	if limit > 0 && len(rows) > limit {
		hidden := len(rows) - limit
		t.tableNoLimit(head, rows[:limit], prefix(marks, limit), maxw)
		t.printf("  %s\n", t.c(ansiDim, fmt.Sprintf("... %d more rows (use --limit 0 or see the HTML report)", hidden)))
		return
	}
	t.tableNoLimit(head, rows, marks, maxw)
}

func prefix(v []string, n int) []string {
	if v == nil {
		return nil
	}
	return v[:n]
}

func (t *textWriter) tableNoLimit(head []string, rows [][]string, marks []string, maxw []int) {
	if len(rows) == 0 {
		return
	}
	widths := make([]int, len(head))
	for i, h := range head {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, r := range rows {
		for i, cell := range r {
			if n := utf8.RuneCountInString(cell); n > widths[i] {
				widths[i] = n
			}
		}
	}
	for i := range widths {
		if i < len(maxw) && widths[i] > maxw[i] {
			widths[i] = maxw[i]
		}
	}
	line := func(mark string, cells []string, header bool) {
		var b strings.Builder
		b.WriteString("  ")
		if marks != nil {
			if header {
				b.WriteString("  ")
			} else {
				b.WriteString(t.colorMark(mark) + " ")
			}
		}
		for i, cell := range cells {
			cell = fit(cell, widths[i])
			pad := widths[i] - utf8.RuneCountInString(cell)
			if header {
				cell = t.c(ansiDim, cell)
			} else if cell == Absent || strings.HasPrefix(cell, compare.NotRecorded) {
				cell = t.c(ansiDim, cell)
			} else if strings.Contains(cell, "failed") && !strings.Contains(cell, "0 failed") {
				cell = t.c(ansiRed, cell)
			}
			b.WriteString(cell)
			if i < len(cells)-1 {
				b.WriteString(strings.Repeat(" ", pad+2))
			}
		}
		t.printf("%s\n", strings.TrimRight(b.String(), " "))
	}
	line("", head, true)
	for i, r := range rows {
		m := ""
		if marks != nil {
			m = marks[i]
		}
		line(m, r, false)
	}
}

func (t *textWriter) colorMark(m string) string {
	switch m {
	case "=":
		return t.c(ansiGreen, m)
	case "~":
		return t.c(ansiYellow, m)
	case "A":
		return t.c(ansiCyan, m)
	case "B":
		return t.c(ansiMag, m)
	}
	return m
}

// Sanitize makes logged text safe to print on a terminal: newlines are
// flattened and every other control character (including ESC, so logged ANSI
// sequences cannot drive the terminal) is shown as \xNN.
func Sanitize(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", " | ")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			fmt.Fprintf(&b, `\x%02x`, r)
		case r == utf8.RuneError:
			b.WriteRune('?')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// fit sanitizes s and truncates it to w runes (adding "...").
func fit(s string, w int) string {
	s = Sanitize(s)
	if utf8.RuneCountInString(s) <= w {
		return s
	}
	if w <= 3 {
		return string([]rune(s)[:w])
	}
	return string([]rune(s)[:w-3]) + "..."
}
