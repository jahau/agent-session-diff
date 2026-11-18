// Package render turns a compare.Report into a terminal summary or a
// standalone HTML page. Both renderers are pure functions of the report.
package render

import (
	"fmt"
	"strings"

	"github.com/jahau/agent-session-diff/internal/compare"
	"github.com/jahau/agent-session-diff/internal/model"
)

// Absent marks a side that has no entry for a row.
const Absent = "-"

// FileCell describes one side of a file row, e.g. "modified +12/-3 (2 changes)".
func FileCell(fs *compare.FileSide) string {
	if fs == nil {
		return Absent
	}
	var b strings.Builder
	b.WriteString(fs.Net)
	if fs.RenamedTo != "" {
		b.WriteString(" -> " + fs.RenamedTo)
	}
	if fs.Counted == 0 && fs.Changes > fs.NotApplied {
		b.WriteString(" (line counts not recorded)")
	} else if fs.Counted > 0 {
		fmt.Fprintf(&b, " +%d/-%d", fs.Added, fs.Removed)
		if !fs.CountsComplete {
			b.WriteString("*")
		}
	}
	var extra []string
	if fs.Changes > 1 {
		extra = append(extra, fmt.Sprintf("%d changes", fs.Changes))
	}
	if fs.NotApplied > 0 {
		extra = append(extra, fmt.Sprintf("%d not applied", fs.NotApplied))
	}
	if len(extra) > 0 {
		b.WriteString(" (" + strings.Join(extra, ", ") + ")")
	}
	return b.String()
}

// CommandCell describes one side of a command row, e.g. "2x: 1 ok, 1 failed [exit 1]".
func CommandCell(cs *compare.CommandSide) string {
	if cs == nil {
		return Absent
	}
	var parts []string
	if cs.Succeeded > 0 {
		parts = append(parts, fmt.Sprintf("%d ok", cs.Succeeded))
	}
	if cs.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", cs.Failed))
	}
	if cs.Other > 0 {
		parts = append(parts, fmt.Sprintf("%d other", cs.Other))
	}
	s := fmt.Sprintf("%dx: %s", cs.Runs, strings.Join(parts, ", "))
	if cs.Runs == 1 {
		s = strings.Join(parts, ", ")
		if cs.Other == 1 {
			s = cs.LastState
		} else if cs.Succeeded == 1 {
			s = "ok"
		} else {
			s = "failed"
		}
	}
	if len(cs.ExitCodes) > 0 {
		codes := make([]string, len(cs.ExitCodes))
		for i, c := range cs.ExitCodes {
			codes[i] = fmt.Sprint(c)
		}
		s += " [exit " + strings.Join(codes, ",") + "]"
	}
	return s
}

// TestCountsCell renders recorded counts, or why there are none.
func TestCountsCell(t model.TestRun) string {
	if t.CountsSource == "" {
		return "counts not in output"
	}
	var parts []string
	if t.Passed != nil {
		parts = append(parts, fmt.Sprintf("%d passed", *t.Passed))
	}
	if t.Failed != nil {
		parts = append(parts, fmt.Sprintf("%d failed", *t.Failed))
	}
	if t.Skipped != nil && *t.Skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", *t.Skipped))
	}
	s := strings.Join(parts, ", ")
	if strings.Contains(t.CountsSource, "(packages)") {
		s += " packages"
	}
	return s
}

// TestSideCell summarises one side's test runs.
func TestSideCell(t compare.TestSide) string {
	if t.Runs == 0 {
		return "no test runs recorded"
	}
	s := fmt.Sprintf("%d run(s): %d passed, %d failed", t.Runs, t.Passed, t.Failed)
	if t.Other > 0 {
		s += fmt.Sprintf(", %d no result", t.Other)
	}
	return s
}

// SupportCell renders a support entry, e.g. "partial (3/10 commands)".
func SupportCell(f model.FieldSupport) string {
	if f.Coverage == "" || f.Level == model.SupportNotApplicable {
		if f.Level == model.SupportNotApplicable && f.Coverage != "" {
			return f.Level + " (" + f.Coverage + ")"
		}
		return f.Level
	}
	return f.Level + " (" + f.Coverage + ")"
}

// PresenceMark is a one-character marker for a row's presence.
func PresenceMark(presence string, same bool) string {
	switch presence {
	case compare.OnlyA:
		return "A"
	case compare.OnlyB:
		return "B"
	}
	if same {
		return "="
	}
	return "~"
}

// RedactionText describes active redaction.
func RedactionText(r []string) string {
	if len(r) == 0 {
		return "off"
	}
	return strings.Join(r, ", ")
}

func sideTitle(s *model.Session) string {
	v := string(s.Format)
	if s.AgentVersion != "" {
		v += " " + s.AgentVersion
	}
	return fmt.Sprintf("%s  (%s)", s.Label, v)
}
