package render

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"strings"
	"time"

	"github.com/jahau/agent-session-diff/internal/compare"
	"github.com/jahau/agent-session-diff/internal/model"
)

//go:embed report.html.tmpl
var reportTemplate string

var htmlTmpl = template.Must(template.New("report").Funcs(template.FuncMap{
	"redaction":   RedactionText,
	"filecell":    FileCell,
	"cmdcell":     CommandCell,
	"testcounts":  TestCountsCell,
	"testside":    TestSideCell,
	"supportcell": SupportCell,
	"mark":        PresenceMark,
	"time":        func(t *time.Time) string { return compare.FormatTime(t) },
	"minus":       func(a, b int) int { return a - b },
	"sessions":    func(a, b *model.Session) []*model.Session { return []*model.Session{a, b} },
	"markclass": func(p string, same bool) string {
		switch PresenceMark(p, same) {
		case "A":
			return "m-a"
		case "B":
			return "m-b"
		case "=":
			return "m-same"
		}
		return "m-diff"
	},
	"nrclass": func(s string) string {
		if strings.HasPrefix(s, compare.NotRecorded) || strings.HasPrefix(s, "no test runs") {
			return "nr"
		}
		return ""
	},
	"absentclass": func(fs *compare.FileSide) string {
		if fs == nil {
			return "absent"
		}
		return ""
	},
	"cmdclass": func(cs *compare.CommandSide) string {
		switch {
		case cs == nil:
			return "absent"
		case cs.Failed > 0:
			return "bad"
		}
		return ""
	},
	"lvlclass": func(l string) string { return "lvl-" + strings.ReplaceAll(l, " ", "-") },
	"notes": func(a, b model.FieldSupport) string {
		switch {
		case a.Note == b.Note:
			return a.Note
		case a.Note == "":
			return "B: " + b.Note
		case b.Note == "":
			return "A: " + a.Note
		}
		return "A: " + a.Note + " B: " + b.Note
	},
}).Parse(reportTemplate))

// HTML writes the standalone HTML report. All logged values are escaped by
// html/template; the page loads no external resources and runs no scripts.
//
// Control characters from logs (ESC, BEL, C1 controls...) are rewritten to a
// visible \xNN form so the file is also safe to print in a terminal. The
// template itself contains none, so only logged values are affected.
func HTML(w io.Writer, r *compare.Report) error {
	var buf bytes.Buffer
	if err := htmlTmpl.Execute(&buf, r); err != nil {
		return err
	}
	_, err := io.WriteString(w, stripControls(buf.String()))
	return err
}

func stripControls(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if (r < 0x20 && r != '\n' && r != '\t' && r != '\r') || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			fmt.Fprintf(&b, `\x%02x`, r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
