package parse

import (
	"fmt"

	"github.com/jahau/agent-session-diff/internal/model"
)

// support computes, from the parsed data, how well each normalized field is
// backed by the export, plus fixed per-format caveats.
func support(s *model.Session) []model.FieldSupport {
	var out []model.FieldSupport
	add := func(field string, have, total int, unit, note string) {
		out = append(out, model.FieldSupport{
			Field:    field,
			Level:    level(have, total),
			Coverage: coverage(have, total, unit),
			Note:     note,
		})
	}
	fixed := func(field, lvl, note string) {
		out = append(out, model.FieldSupport{Field: field, Level: lvl, Note: note})
	}

	nFC := len(s.FileChanges)
	var fcLines, fcStatus, fcTime int
	for _, fc := range s.FileChanges {
		if fc.Added != nil && fc.Removed != nil {
			fcLines++
		}
		if fc.Status != model.StatusUnknown {
			fcStatus++
		}
		if fc.Timestamp != nil {
			fcTime++
		}
	}
	nCmd := len(s.Commands)
	var cExit, cDur, cCWD, cTime, cStatus int
	for _, c := range s.Commands {
		if c.ExitCode != nil {
			cExit++
		}
		if c.DurationMS != nil {
			cDur++
		}
		if c.CWD != "" {
			cCWD++
		}
		if c.Timestamp != nil {
			cTime++
		}
		if c.Status != model.StatusUnknown {
			cStatus++
		}
	}
	var tCounts int
	for _, t := range s.Tests {
		if t.CountsSource != "" {
			tCounts++
		}
	}

	shellEdits := "Only changes made through the agent's file-edit tools are counted. Files changed by shell commands (sed -i, git checkout, rm, output redirects) are not detected."
	out = append(out, model.FieldSupport{Field: "file_changes", Level: model.SupportPartial,
		Coverage: fmt.Sprintf("%d recorded", nFC), Note: shellEdits})
	add("file_changes.line_counts", fcLines, nFC, "changes", lineCountNote(s.Format))
	add("file_changes.status", fcStatus, nFC, "changes", "unknown = no tool result recorded for the edit")
	add("file_changes.timestamp", fcTime, nFC, "changes", "")

	cmdLevel, cmdNote := model.SupportRecorded, ""
	if n := otherToolCount(s, "exec"); s.Format == model.FormatCodex && n > 0 {
		cmdLevel = model.SupportPartial
		cmdNote = fmt.Sprintf("%d Codex unified-exec script cell(s) are not decoded; commands they ran are counted only where the export has CommandExecution items.", n)
	}
	out = append(out, model.FieldSupport{Field: "commands", Level: cmdLevel,
		Coverage: fmt.Sprintf("%d recorded", nCmd), Note: cmdNote})
	add("commands.status", cStatus, nCmd, "commands", "unknown = no tool result recorded")
	add("commands.exit_code", cExit, nCmd, "commands", exitNote(s.Format))
	add("commands.duration", cDur, nCmd, "commands", durationNote(s.Format))
	add("commands.cwd", cCWD, nCmd, "commands", cwdNote(s.Format))
	add("commands.timestamp", cTime, nCmd, "commands", timestampNote(s.Format))

	fixed("tests", model.SupportPartial, "Test runs are commands whose text starts a known test runner; outcome is the recorded command status. Tests run inside scripts or other tools are not detected.")
	add("tests.counts", tCounts, len(s.Tests), "test runs", "Counts come only from a runner summary line in the recorded output (go, pytest, jest/vitest, cargo).")

	add("session.timestamps", s.Records-s.MissingTimes, s.Records, "records", "Span = first to last timestamped record; idle time is included.")
	presence("session.model", len(s.Models) > 0, &out)
	presence("session.agent_version", s.AgentVersion != "", &out)
	presence("session.git_branch", s.GitBranch != "", &out)
	presence("session.cwd", s.CWD != "", &out)
	fixed("token_usage", model.SupportUnsupported, "Recorded by both formats but not compared by this tool.")
	fixed("assistant_messages", model.SupportUnsupported, "Conversation text and reasoning are not compared.")
	return out
}

func presence(field string, ok bool, out *[]model.FieldSupport) {
	l := model.SupportNotRecorded
	if ok {
		l = model.SupportRecorded
	}
	*out = append(*out, model.FieldSupport{Field: field, Level: l})
}

func otherToolCount(s *model.Session, name string) int {
	for _, t := range s.OtherTools {
		if t.Name == name {
			return t.Count
		}
	}
	return 0
}

func level(have, total int) string {
	switch {
	case total == 0:
		return model.SupportNotApplicable
	case have == total:
		return model.SupportRecorded
	case have == 0:
		return model.SupportNotRecorded
	default:
		return model.SupportPartial
	}
}

func coverage(have, total int, unit string) string {
	if total == 0 {
		return "no " + unit
	}
	return fmt.Sprintf("%d/%d %s", have, total, unit)
}

func lineCountNote(f model.Format) string {
	switch f {
	case model.FormatClaudeCode:
		return "From the edit tool's structuredPatch, or the full content of a newly created file."
	case model.FormatCodex:
		return "From FileChange unified diffs or apply_patch hunks."
	}
	return ""
}

func exitNote(f model.Format) string {
	switch f {
	case model.FormatClaudeCode:
		return "Claude Code states an exit code only in failed results (\"Exit code N\"); successful commands have a status but no exit code."
	case model.FormatCodex:
		return "From CommandExecution.exit_code, legacy \"Exit code: N\" output, or shell output metadata."
	}
	return ""
}

func durationNote(f model.Format) string {
	switch f {
	case model.FormatClaudeCode:
		return "Claude Code does not record command duration. It is not derived from timestamps."
	case model.FormatCodex:
		return "From CommandExecution.duration, \"Wall time\" output, or shell output metadata."
	}
	return ""
}

func cwdNote(f model.Format) string {
	switch f {
	case model.FormatClaudeCode:
		return "Claude Code records a session cwd, not a per-command cwd."
	case model.FormatCodex:
		return "From CommandExecution.cwd or the workdir argument."
	}
	return ""
}

func timestampNote(f model.Format) string {
	switch f {
	case model.FormatClaudeCode:
		return "Time of the tool-call record."
	case model.FormatCodex:
		return "CommandExecution items: completion time. Legacy shell calls: call time."
	}
	return ""
}
