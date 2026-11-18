package render

import (
	_ "embed"
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
func HTML(w io.Writer, r *compare.Report) error {
	return htmlTmpl.Execute(w, r)
}
