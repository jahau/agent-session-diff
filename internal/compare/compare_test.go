package compare

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/jahau/agent-session-diff/internal/model"
	"github.com/jahau/agent-session-diff/internal/parse"
)

func load(t *testing.T, p string) *model.Session {
	t.Helper()
	s, err := parse.ParseFile(p, parse.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCompareExamples(t *testing.T) {
	a := load(t, "../../examples/claude-code-session.jsonl")
	b := load(t, "../../examples/codex-session.jsonl")
	r := Compare(a, b, nil)
	want := Summary{FilesBoth: 1, FilesOnlyA: 1, FilesOnlyB: 1, CommandsBoth: 2, CommandsOnlyA: 2, CommandsOnlyB: 3}
	if r.Summary != want {
		t.Errorf("summary = %+v, want %+v", r.Summary, want)
	}
	f := r.Files[0]
	if f.Path != "config/config.go" || f.Presence != Both || !f.SameNet || f.A.Changes != 2 || f.A.Added != 4 || f.A.Removed != 1 {
		t.Errorf("config.go row = %+v A=%+v", f, f.A)
	}
	if r.Commands[0].Text != "go test ./..." || r.Commands[0].A.Runs != 2 || r.Commands[0].B.Runs != 1 {
		t.Errorf("first command row = %+v", r.Commands[0])
	}
	if r.Tests[0].Runs != 4 || r.Tests[0].Failed != 1 || r.Tests[1].Runs != 2 {
		t.Errorf("tests = %+v", r.Tests)
	}
}

func TestCompareIsDeterministicAndSymmetric(t *testing.T) {
	a := load(t, "../../examples/claude-code-session.jsonl")
	b := load(t, "../../examples/codex-session.jsonl")
	first, _ := json.Marshal(Compare(a, b, nil))
	for i := 0; i < 10; i++ {
		again, _ := json.Marshal(Compare(load(t, "../../examples/claude-code-session.jsonl"), load(t, "../../examples/codex-session.jsonl"), nil))
		if !bytes.Equal(first, again) {
			t.Fatal("report differs between runs")
		}
	}
	ab, ba := Compare(a, b, nil).Summary, Compare(b, a, nil).Summary
	if ab.FilesOnlyA != ba.FilesOnlyB || ab.CommandsOnlyA != ba.CommandsOnlyB || ab.FilesBoth != ba.FilesBoth {
		t.Errorf("swap asymmetry: %+v vs %+v", ab, ba)
	}
}

func TestCompareSelfIsAllSame(t *testing.T) {
	a := load(t, "../../testdata/codex/legacy.jsonl")
	r := Compare(a, load(t, "../../testdata/codex/legacy.jsonl"), nil)
	for _, row := range append(r.Overview, r.Timing...) {
		if row.Differs {
			t.Errorf("self-compare differs on %s: %q vs %q", row.Field, row.A, row.B)
		}
	}
	if r.Summary.FilesOnlyA+r.Summary.FilesOnlyB+r.Summary.FilesDiffer+r.Summary.CommandsOnlyA+r.Summary.CommandsOnlyB != 0 {
		t.Errorf("self-compare summary = %+v", r.Summary)
	}
}

func TestNetEffect(t *testing.T) {
	cases := []struct {
		ops  []model.Op
		want string
	}{
		{nil, "not applied"},
		{[]model.Op{model.OpCreate, model.OpModify}, "created"},
		{[]model.Op{model.OpCreate, model.OpDelete}, "created then deleted"},
		{[]model.Op{model.OpModify, model.OpDelete}, "deleted"},
		{[]model.Op{model.OpRename}, "renamed"},
		{[]model.Op{model.OpWrite}, "written"},
		{[]model.Op{model.OpWrite, model.OpModify}, "modified"},
	}
	for _, c := range cases {
		if got := netEffect(c.ops); got != c.want {
			t.Errorf("netEffect(%v) = %q, want %q", c.ops, got, c.want)
		}
	}
}

func TestFailedChangesDoNotCountAsApplied(t *testing.T) {
	one := 1
	s := &model.Session{FileChanges: []model.FileChange{
		{Path: "a", Op: model.OpModify, Status: model.StatusFailed, Added: &one, Removed: &one},
	}}
	e := &model.Session{}
	r := Compare(s, e, nil)
	if r.Files[0].A.Net != "not applied" || r.Files[0].A.NotApplied != 1 || r.Files[0].A.Added != 0 {
		t.Errorf("row = %+v", r.Files[0].A)
	}
}

func TestPartialCountsFlagged(t *testing.T) {
	one := 1
	s := &model.Session{FileChanges: []model.FileChange{
		{Path: "a", Op: model.OpModify, Status: model.StatusSucceeded, Added: &one, Removed: &one},
		{Path: "a", Op: model.OpModify, Status: model.StatusSucceeded},
	}}
	r := Compare(s, &model.Session{}, nil)
	if r.Files[0].A.CountsComplete {
		t.Error("counts should be flagged partial")
	}
}

func TestFormatDuration(t *testing.T) {
	cases := map[time.Duration]string{
		1500 * time.Millisecond:                     "1.5s",
		90 * time.Second:                            "1m30s",
		3*time.Hour + 2*time.Minute + 1*time.Second: "3h02m01s",
		-2 * time.Second:                            "-2.0s",
	}
	for d, want := range cases {
		if got := FormatDuration(d); got != want {
			t.Errorf("FormatDuration(%s) = %q, want %q", d, got, want)
		}
	}
	if FormatTime(nil) != NotRecorded {
		t.Error("nil time should be not recorded")
	}
}

func TestCommandKey(t *testing.T) {
	if CommandKey("  go   test\n ./... ") != "go test ./..." {
		t.Error("whitespace not normalized")
	}
}
