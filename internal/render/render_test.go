package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jahau/agent-session-diff/internal/compare"
	"github.com/jahau/agent-session-diff/internal/model"
)

func hostile() *compare.Report {
	evil := "<script>alert(1)</script>\x1b[31mred\x1b]0;title\x07"
	a := &model.Session{Label: evil, Format: model.FormatClaudeCode,
		Commands:    []model.Command{{Seq: 1, Text: evil, Status: model.StatusSucceeded}},
		FileChanges: []model.FileChange{{Path: evil, Op: model.OpCreate, Status: model.StatusSucceeded}},
		Warnings:    []model.Warning{{Line: 1, Message: evil}}, WarningsTotal: 1,
	}
	b := &model.Session{Label: "b", Format: model.FormatCodex}
	return compare.Compare(a, b, nil)
}

func TestHTMLEscapesLoggedContent(t *testing.T) {
	var buf bytes.Buffer
	if err := HTML(&buf, hostile()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "<script>") {
		t.Fatal("logged <script> reached the HTML unescaped")
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Error("expected escaped script tag")
	}
	for _, banned := range []string{"<script", "http://", "https://", "<link", "@import"} {
		if strings.Contains(strings.ReplaceAll(out, "&lt;script", ""), banned) {
			t.Errorf("HTML contains %q; report must be standalone and script-free", banned)
		}
	}
}

func TestTextNeutralizesControlCharacters(t *testing.T) {
	var buf bytes.Buffer
	if err := Text(&buf, hostile(), TextOptions{Color: false}); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(buf.String(), "\x1b\x07") {
		t.Fatal("logged control characters reached the terminal output")
	}
	if !strings.Contains(buf.String(), `\x1b[31mred`) {
		t.Error("expected escaped ESC sequence to be visible")
	}
}

func TestTextColorOnlyWhenRequested(t *testing.T) {
	var plain, color bytes.Buffer
	r := hostile()
	_ = Text(&plain, r, TextOptions{})
	_ = Text(&color, r, TextOptions{Color: true})
	if strings.Contains(plain.String(), "\x1b") {
		t.Error("ANSI in uncolored output")
	}
	if !strings.Contains(color.String(), "\x1b[1m") {
		t.Error("no ANSI in colored output")
	}
}

func TestLimit(t *testing.T) {
	a := &model.Session{Label: "a"}
	for i := 0; i < 30; i++ {
		a.Commands = append(a.Commands, model.Command{Seq: i + 1, Text: strings.Repeat("x", i+1), Status: model.StatusSucceeded})
	}
	r := compare.Compare(a, &model.Session{Label: "b"}, nil)
	var buf bytes.Buffer
	_ = Text(&buf, r, TextOptions{Limit: 10})
	if !strings.Contains(buf.String(), "... 20 more rows") {
		t.Errorf("limit note missing:\n%s", buf.String())
	}
}

func TestCells(t *testing.T) {
	if got := FileCell(&compare.FileSide{Net: "modified", Changes: 2, Added: 3, Removed: 1, Counted: 1, CountsComplete: false}); got != "modified +3/-1* (2 changes)" {
		t.Errorf("FileCell = %q", got)
	}
	if got := CommandCell(&compare.CommandSide{Runs: 3, Succeeded: 2, Failed: 1, ExitCodes: []int{0, 2}}); got != "3x: 2 ok, 1 failed [exit 0,2]" {
		t.Errorf("CommandCell = %q", got)
	}
	if got := CommandCell(&compare.CommandSide{Runs: 1, Other: 1, LastState: "unknown"}); got != "unknown" {
		t.Errorf("CommandCell = %q", got)
	}
	if FileCell(nil) != Absent || CommandCell(nil) != Absent {
		t.Error("absent cells")
	}
}
