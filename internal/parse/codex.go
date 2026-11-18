package parse

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/jahau/agent-session-diff/internal/model"
)

// Codex CLI rollout exports (~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl).
//
// Every line is {timestamp, type, payload}. Interpreted records:
//   - session_meta: payload.id, cwd, cli_version, git.branch.
//   - turn_context: payload.model, cwd.
//   - response_item function_call {name, call_id, arguments(JSON string)} and
//     custom_tool_call {name, call_id, input}, paired with
//     function_call_output / custom_tool_call_output by call_id. Mapped tools:
//     shell (argv), shell_command (string), exec_command (string),
//     apply_patch (patch envelope).
//   - event_msg item_completed with item.type CommandExecution
//     {command, cwd, status, exit_code, duration, aggregated_output} or
//     FileChange {id, status, changes{path: {type, unified_diff, content,
//     move_path}}}. A FileChange item's id equals the call_id of the
//     apply_patch call it completes, so that call is not counted twice.
//
// Unified-exec "exec" script cells are counted as other tool calls: the
// commands they run are only visible through CommandExecution items.

type cxCall struct {
	name, callID string
	args         obj    // function_call arguments
	input        string // custom_tool_call input
	ts           *time.Time
	line         int
}

type codexParser struct {
	b             *builder
	calls         []cxCall
	outs          map[string]string
	fileChangeIDs map[string]bool
}

func newCodexParser(b *builder) *codexParser {
	return &codexParser{b: b, outs: map[string]string{}, fileChangeIDs: map[string]bool{}}
}

func (p *codexParser) record(no int, rec obj) {
	b := p.b
	typ := rec.s("type")
	ts, _ := rec.timestamp("timestamp")
	pl, ok := rec.child("payload")
	if !ok {
		b.notInterpreted(typ)
		return
	}
	switch typ {
	case "session_meta":
		b.sessionID(pl.s("id"))
		b.setCWD(pl.s("cwd"))
		b.setVersion(pl.s("cli_version"))
		if g, ok := pl.child("git"); ok {
			b.setBranch(g.s("branch"))
		}
	case "turn_context":
		b.model(pl.s("model"))
		b.setCWD(pl.s("cwd"))
	case "response_item":
		pt := pl.s("type")
		switch pt {
		case "function_call":
			c := cxCall{name: pl.s("name"), callID: pl.s("call_id"), ts: ts, line: no}
			if argStr, ok := pl.str("arguments"); ok {
				c.args, _ = decodeObj([]byte(argStr))
			} else {
				c.args, _ = pl.child("arguments")
			}
			p.calls = append(p.calls, c)
		case "custom_tool_call":
			p.calls = append(p.calls, cxCall{name: pl.s("name"), callID: pl.s("call_id"), input: pl.s("input"), ts: ts, line: no})
		case "function_call_output", "custom_tool_call_output":
			if id := pl.s("call_id"); id != "" {
				if _, dup := p.outs[id]; !dup {
					p.outs[id] = textOf(pl["output"])
				}
			}
		default:
			b.notInterpreted("response_item/" + pt)
		}
	case "event_msg":
		pt := pl.s("type")
		if pt != "item_completed" {
			b.notInterpreted("event_msg/" + pt)
			return
		}
		item, ok := pl.child("item")
		if !ok {
			b.warn(no, "item_completed without an item object; skipped")
			return
		}
		switch it := item.s("type"); it {
		case "CommandExecution":
			p.commandItem(no, ts, item)
		case "FileChange":
			p.fileChangeItem(no, ts, item)
		case "AgentMessage", "UserMessage", "Reasoning", "ContextCompaction":
			b.notInterpreted("item_completed/" + it)
		default:
			b.otherTool("item:" + it)
		}
	default:
		b.notInterpreted(typ)
	}
}

func (p *codexParser) commandItem(no int, ts *time.Time, item obj) {
	var text string
	if argv, ok := item.strings("command"); ok {
		text = commandFromArgv(argv)
	} else {
		text = item.s("command")
	}
	if strings.TrimSpace(text) == "" {
		p.b.warn(no, "CommandExecution without a command; skipped")
		return
	}
	c := model.Command{Line: no, Text: text, Tool: "CommandExecution", Timestamp: ts, TimestampKind: "completion", CWD: item.s("cwd")}
	if n, ok := item.integer("exit_code"); ok {
		c.ExitCode = &n
	}
	if d, ok := item.child("duration"); ok {
		secs, ok1 := d.number("secs")
		nanos, ok2 := d.number("nanos")
		if ok1 || ok2 {
			ms := int64(secs*1000 + nanos/1e6 + 0.5)
			c.DurationMS = &ms
		}
	}
	c.Status = statusFrom(c.ExitCode, item.s("status"))
	p.b.addCommand(c, item.s("aggregated_output"))
}

func statusFrom(exit *int, status string) model.Status {
	if exit != nil {
		if *exit == 0 {
			return model.StatusSucceeded
		}
		return model.StatusFailed
	}
	switch strings.ToLower(status) {
	case "failed":
		return model.StatusFailed
	case "declined", "rejected":
		return model.StatusRejected
	case "interrupted", "aborted", "cancelled":
		return model.StatusInterrupted
	}
	return model.StatusUnknown
}

func (p *codexParser) fileChangeItem(no int, ts *time.Time, item obj) {
	if id := item.s("id"); id != "" {
		p.fileChangeIDs[id] = true
	}
	var status model.Status
	switch strings.ToLower(item.s("status")) {
	case "completed":
		status = model.StatusSucceeded
	case "failed":
		status = model.StatusFailed
	case "declined", "rejected":
		status = model.StatusRejected
	default:
		status = model.StatusUnknown
	}
	var changes map[string]json.RawMessage
	if json.Unmarshal(item["changes"], &changes) != nil {
		p.b.warn(no, "FileChange without a changes object; skipped")
		return
	}
	paths := make([]string, 0, len(changes))
	for k := range changes {
		paths = append(paths, k)
	}
	sort.Strings(paths) // map order is random; keep output deterministic
	for i, path := range paths {
		ch, ok := decodeObj(changes[path])
		if !ok {
			continue
		}
		fc := model.FileChange{Line: no, Order: orderKey(no, i), Path: path, Tool: "FileChange", Status: status, Timestamp: ts}
		diff, hasDiff := ch.str("unified_diff")
		content, hasContent := ch.str("content")
		switch ch.s("type") {
		case "add":
			fc.Op = model.OpCreate
			if hasContent {
				fc.Added, fc.Removed = model.IntPtr(lineCount(content)), model.IntPtr(0)
			}
		case "delete":
			fc.Op = model.OpDelete
			if hasContent {
				fc.Added, fc.Removed = model.IntPtr(0), model.IntPtr(lineCount(content))
			}
		case "update":
			fc.Op = model.OpModify
			if mv := ch.s("move_path"); mv != "" {
				fc.Op = model.OpRename
				fc.NewPath = mv
			}
		default:
			p.b.warn(no, "FileChange entry with unrecognized type "+quote(ch.s("type"))+"; skipped")
			continue
		}
		if hasDiff && fc.Added == nil {
			a, r := diffCounts(diff)
			fc.Added, fc.Removed = &a, &r
		}
		p.b.addFileChange(fc)
	}
}

func quote(s string) string {
	if s == "" {
		return "(empty)"
	}
	return `"` + s + `"`
}

func (p *codexParser) finish() {
	b := p.b
	for _, c := range p.calls {
		out, has := p.outs[c.callID]
		switch c.name {
		case "shell", "shell_command", "exec_command":
			var text string
			switch c.name {
			case "shell":
				argv, _ := c.args.strings("command")
				text = commandFromArgv(argv)
			case "shell_command":
				text = c.args.s("command")
			default:
				text = c.args.s("cmd")
			}
			if strings.TrimSpace(text) == "" {
				b.warn(c.line, c.name+" call without a command; skipped")
				continue
			}
			cmd := model.Command{Line: c.line, Text: text, Tool: c.name, Timestamp: c.ts, TimestampKind: "call", CWD: c.args.s("workdir")}
			body := out
			cmd.Status = model.StatusUnknown
			if has {
				body, cmd.ExitCode, cmd.DurationMS = legacyShellOutput(out)
				cmd.Status = statusFrom(cmd.ExitCode, "")
				if cmd.ExitCode == nil {
					cmd.Status = outputStatus(out)
				}
			}
			b.addCommand(cmd, body)
		case "apply_patch":
			if p.fileChangeIDs[c.callID] {
				continue // completed FileChange item already recorded it
			}
			input := c.input
			if input == "" && c.args != nil {
				input = c.args.s("input")
			}
			status := model.StatusUnknown
			if has {
				_, exit, _ := legacyShellOutput(out)
				status = statusFrom(exit, "")
				if exit == nil {
					status = outputStatus(out)
				}
			}
			files := parseApplyPatch(input)
			if len(files) == 0 {
				b.warn(c.line, "apply_patch call with no file sections; skipped")
			}
			for i, f := range files {
				fc := model.FileChange{Line: c.line, Order: orderKey(c.line, i), Path: f.path, Tool: "apply_patch", Status: status, Timestamp: c.ts}
				switch f.op {
				case "add":
					fc.Op = model.OpCreate
				case "delete":
					fc.Op = model.OpDelete
				default:
					fc.Op = model.OpModify
					if f.moveTo != "" {
						fc.Op, fc.NewPath = model.OpRename, f.moveTo
					}
				}
				if f.op != "delete" {
					fc.Added, fc.Removed = model.IntPtr(f.added), model.IntPtr(f.removed)
				}
				b.addFileChange(fc)
			}
		default:
			b.otherTool(c.name)
		}
	}
}

// legacyShellOutput reads exit code and duration from legacy Codex shell
// output: either JSON {"output", "metadata": {"exit_code", "duration_seconds"}}
// or text beginning "Exit code: N\nWall time: X seconds\nOutput:".
func legacyShellOutput(out string) (body string, exit *int, durMS *int64) {
	trimmed := strings.TrimSpace(out)
	if strings.HasPrefix(trimmed, "{") {
		if o, ok := decodeObj([]byte(trimmed)); ok {
			body = o.s("output")
			if m, ok := o.child("metadata"); ok {
				if n, ok := m.integer("exit_code"); ok {
					exit = &n
				}
				if d, ok := m.number("duration_seconds"); ok {
					ms := int64(d*1000 + 0.5)
					durMS = &ms
				}
			}
			return body, exit, durMS
		}
	}
	exit, durMS = legacyExitAndWall(out)
	return out, exit, durMS
}

// outputStatus classifies tool output that carries no exit code.
func outputStatus(out string) model.Status {
	t := strings.ToLower(strings.TrimSpace(out))
	switch {
	case strings.HasPrefix(t, "success."):
		return model.StatusSucceeded
	case strings.Contains(t, "rejected by user"), strings.Contains(t, "declined by user"):
		return model.StatusRejected
	case strings.HasPrefix(t, "execution error"), strings.HasPrefix(t, "apply_patch verification failed"):
		return model.StatusFailed
	case strings.HasPrefix(t, "aborted"):
		return model.StatusInterrupted
	}
	return model.StatusUnknown
}
