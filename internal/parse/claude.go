package parse

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jahau/agent-session-diff/internal/model"
)

// Claude Code session exports (~/.claude/projects/<project>/<session>.jsonl).
//
// Interpreted records:
//   - type "assistant": message.content[] blocks of type "tool_use"
//     {id, name, input}; message.model.
//   - type "user": message.content[] blocks of type "tool_result"
//     {tool_use_id, is_error, content}, plus the record-level toolUseResult
//     object (stdout/stderr/interrupted for shells; type/structuredPatch for
//     Write/Edit).
//   - any record: timestamp, sessionId, version, cwd, gitBranch.
//
// Everything else is counted as "not interpreted".

type ccUse struct {
	id, name string
	input    obj
	ts       *time.Time
	line     int
	block    int
}

type ccResult struct {
	isError bool
	text    string
	tur     obj // toolUseResult; nil when absent or ambiguous
}

type claudeParser struct {
	b       *builder
	uses    []ccUse
	seen    map[string]bool
	results map[string]ccResult
}

func newClaudeParser(b *builder) *claudeParser {
	return &claudeParser{b: b, seen: map[string]bool{}, results: map[string]ccResult{}}
}

func (p *claudeParser) record(no int, rec obj) {
	b := p.b
	b.sessionID(rec.s("sessionId"))
	b.setVersion(rec.s("version"))
	b.setCWD(rec.s("cwd"))
	b.setBranch(rec.s("gitBranch"))
	ts, _ := rec.timestamp("timestamp")

	typ := rec.s("type")
	switch typ {
	case "assistant", "user":
	default:
		b.notInterpreted(typ)
		return
	}
	msg, ok := rec.child("message")
	if !ok {
		b.warn(no, typ+" record without a message object; skipped")
		return
	}
	blocks, ok := msg.array("content")
	if !ok {
		return // plain-text message: nothing to normalize
	}
	if typ == "assistant" {
		b.model(msg.s("model"))
		for i, raw := range blocks {
			bo, ok := decodeObj(raw)
			if !ok || bo.s("type") != "tool_use" {
				continue
			}
			id := bo.s("id")
			if id == "" {
				b.warn(no, "tool_use block without id; skipped")
				continue
			}
			if p.seen[id] {
				continue // streamed duplicates of the same block
			}
			p.seen[id] = true
			input, _ := bo.child("input")
			p.uses = append(p.uses, ccUse{id: id, name: bo.s("name"), input: input, ts: ts, line: no, block: i})
		}
		return
	}
	var results []obj
	for _, raw := range blocks {
		if bo, ok := decodeObj(raw); ok && bo.s("type") == "tool_result" {
			results = append(results, bo)
		}
	}
	var tur obj
	if len(results) == 1 {
		tur, _ = rec.child("toolUseResult")
	}
	for _, bo := range results {
		id := bo.s("tool_use_id")
		if id == "" {
			continue
		}
		if _, dup := p.results[id]; dup {
			continue
		}
		isErr, _ := bo.boolean("is_error")
		p.results[id] = ccResult{isError: isErr, text: textOf(bo["content"]), tur: tur}
	}
}

var ccExitCode = regexp.MustCompile(`^Exit code (-?\d+)`)

func ccStatus(res ccResult, has bool) model.Status {
	if !has {
		return model.StatusUnknown
	}
	if res.tur != nil {
		if v, _ := res.tur.boolean("interrupted"); v {
			return model.StatusInterrupted
		}
	}
	if !res.isError {
		return model.StatusSucceeded
	}
	t := strings.TrimSpace(res.text)
	switch {
	case strings.HasPrefix(t, "The user doesn't want to proceed"),
		strings.HasPrefix(t, "Permission for") && strings.Contains(t, "denied"),
		strings.HasPrefix(t, "Permission to use") && strings.Contains(t, "denied"):
		return model.StatusRejected
	case strings.HasPrefix(t, "[Request interrupted"):
		return model.StatusInterrupted
	}
	return model.StatusFailed
}

func (p *claudeParser) finish() {
	b := p.b
	for _, u := range p.uses {
		res, has := p.results[u.id]
		status := ccStatus(res, has)
		order := orderKey(u.line, u.block)
		switch u.name {
		case "Bash", "PowerShell":
			text := u.input.s("command")
			if text == "" {
				b.warn(u.line, u.name+" call without a command string; skipped")
				continue
			}
			c := model.Command{Line: u.line, Order: order, Text: text, Tool: u.name, Status: status,
				Timestamp: u.ts, TimestampKind: "call"}
			if has && res.isError {
				if m := ccExitCode.FindStringSubmatch(strings.TrimSpace(res.text)); m != nil {
					if n, err := strconv.Atoi(m[1]); err == nil {
						c.ExitCode = &n
					}
				}
			}
			out := res.text
			if res.tur != nil {
				out = res.tur.s("stdout") + "\n" + res.tur.s("stderr")
			}
			b.addCommand(c, out)
		case "Write":
			fp := u.input.s("file_path")
			if fp == "" {
				b.warn(u.line, "Write call without file_path; skipped")
				continue
			}
			fc := model.FileChange{Line: u.line, Order: order, Path: fp, Op: model.OpWrite, Tool: u.name, Status: status, Timestamp: u.ts}
			if res.tur != nil {
				switch res.tur.s("type") {
				case "create":
					fc.Op = model.OpCreate
				case "update":
					fc.Op = model.OpModify
				}
			}
			// Claude Code records an empty structuredPatch for creates, so a
			// new file's size comes from the written content instead.
			if fc.Op == model.OpCreate {
				if content, ok := u.input.str("content"); ok {
					fc.Added, fc.Removed = model.IntPtr(lineCount(content)), model.IntPtr(0)
				}
			} else if a, r, ok := structuredPatchCounts(res.tur); ok && fc.Op == model.OpModify {
				fc.Added, fc.Removed = &a, &r
			}
			b.addFileChange(fc)
		case "Edit", "MultiEdit", "NotebookEdit":
			fp := u.input.s("file_path")
			if u.name == "NotebookEdit" {
				fp = u.input.s("notebook_path")
			}
			if fp == "" {
				b.warn(u.line, u.name+" call without a file path; skipped")
				continue
			}
			fc := model.FileChange{Line: u.line, Order: order, Path: fp, Op: model.OpModify, Tool: u.name, Status: status, Timestamp: u.ts}
			if a, r, ok := structuredPatchCounts(res.tur); ok {
				fc.Added, fc.Removed = &a, &r
			}
			b.addFileChange(fc)
		default:
			b.otherTool(u.name)
		}
	}
}

// structuredPatchCounts sums +/- lines over toolUseResult.structuredPatch
// hunks. ok=false when no structuredPatch array is recorded.
func structuredPatchCounts(tur obj) (added, removed int, ok bool) {
	if tur == nil {
		return 0, 0, false
	}
	hunks, ok := tur.array("structuredPatch")
	if !ok {
		return 0, 0, false
	}
	for _, h := range hunks {
		ho, ok := decodeObj(h)
		if !ok {
			continue
		}
		lines, _ := ho.strings("lines")
		for _, l := range lines {
			switch {
			case strings.HasPrefix(l, "+"):
				added++
			case strings.HasPrefix(l, "-"):
				removed++
			}
		}
	}
	return added, removed, true
}
