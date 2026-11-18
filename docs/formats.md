# Supported export formats

This page records which parts of each export format `agent-session-diff`
reads, and why. The structure was checked on 2026-11-18 against real local
exports from Claude Code 2.1.301–2.1.327 (23 sessions) and Codex CLI
0.58–0.169 (127 rollouts). Only key names, value types and record kinds were
inspected, not contents. The fixtures in this repository are synthetic
reconstructions of those shapes.

Both formats are JSON Lines: one JSON object per line, appended as the session
runs. Neither format is a documented, stable public interface. If a future
agent version changes shape, the tool reports unknown record kinds as "not
interpreted" rather than guessing.

## Claude Code

Location: `~/.claude/projects/<encoded-project-path>/<session-id>.jsonl`.

Each line has a top-level `type`. Most lines also carry `timestamp`
(RFC 3339), `sessionId`, `cwd`, `version` and `gitBranch`.

| Record | Fields read | Used for |
| --- | --- | --- |
| `type: "assistant"` | `message.model`; `message.content[]` blocks with `type: "tool_use"` → `id`, `name`, `input` | Tool calls. Streamed duplicates of the same block id are ignored. |
| `type: "user"` | `message.content[]` blocks with `type: "tool_result"` → `tool_use_id`, `is_error`, `content`; record-level `toolUseResult` | Results, paired by `tool_use_id`. `toolUseResult` is used only when the record has exactly one result. |
| any | `timestamp`, `sessionId`, `cwd`, `version`, `gitBranch` | Session metadata and timing. |

Tool mapping:

| Tool | Normalized as | Details |
| --- | --- | --- |
| `Bash`, `PowerShell` | command | `input.command`. Status from `is_error` and `toolUseResult.interrupted`. Exit code only from a failed result starting `Exit code N`. Output (for test counts) from `toolUseResult.stdout/stderr`. |
| `Write` | file change | `toolUseResult.type` `create` → create, `update` → modify, absent → `write` (create-or-overwrite unknown). Line counts: creates use the written content (Claude Code records an empty `structuredPatch` for creates); updates use `structuredPatch`. |
| `Edit`, `MultiEdit` | file change (modify) | Line counts from `toolUseResult.structuredPatch[].lines` (`+`/`-` prefixes). |
| `NotebookEdit` | file change (modify) | `input.notebook_path`; no line counts. |
| anything else | other tool call | Counted, not compared. |

Rejected tool calls ("The user doesn't want to proceed…", "Permission … denied")
get status `rejected`.

Not read: `attachment`, `system`, `file-history-*`, `api-request*`,
`queue-operation`, `ai-title`, `summary` and similar records, plus thinking and
message text, token usage, and `toolUseResult.bashEditDiff`. The last of these
appears on some Bash results but its meaning is not documented, so it is not
treated as a file change.

## Codex CLI

Location: `~/.codex/sessions/YYYY/MM/DD/rollout-<timestamp>-<id>.jsonl`.

Every line is `{"timestamp", "type", "payload"}`. Three generations of
tool-call records exist, and a single rollout can mix them:

| Era | Commands | File changes |
| --- | --- | --- |
| 2025 | `function_call` `shell` with argv `command[]`; output is JSON `{"output", "metadata": {"exit_code", "duration_seconds"}}` | `custom_tool_call` `apply_patch` with a `*** Begin Patch` envelope |
| mid-2026 | `function_call` `shell_command` with string `command`, `workdir`; output text starts `Exit code: N` / `Wall time: X seconds` | `apply_patch` plus an `event_msg` `item_completed` `FileChange` item |
| current | `custom_tool_call` `exec` script cells; actual commands as `item_completed` `CommandExecution` items `{command[], cwd, status, exit_code, duration{secs,nanos}, aggregated_output}` | `FileChange` items `{id, status, changes{path: {type: add/update/delete, unified_diff, content, move_path}}}` |

Rules:

- A `FileChange` item's `id` equals the `call_id` of the `apply_patch` call it
  completes (verified on all 284 such pairs in the sample). The pair is
  counted once, preferring the item, which carries status and a diff.
- `CommandExecution.command` is a shell wrapper argv (`powershell.exe -Command
  …`, `bash -lc …`). It is shown as the inner script.
- `exec` script cells are counted as other tool calls. Their JavaScript is not
  interpreted. When a rollout has `exec` cells, the `commands` field is marked
  partial.
- `CommandExecution` timestamps are completion times. Legacy call timestamps
  are call times. Each command records which kind it has (`timestamp_kind`).
- `apply_patch` outputs `apply_patch verification failed…` → failed,
  `patch rejected by user` → rejected.

Session metadata: `session_meta.payload` (`id`, `cwd`, `cli_version`,
`git.branch`) and `turn_context.payload` (`model`, `cwd`).

Not read: `reasoning`, `message`, `token_count`, `task_started/complete`,
`turn_aborted`, `thread_settings_applied`, `world_state`, `compacted`,
`token_usage_record`, `WebSearch`/`McpToolCall` items (counted as other tool
calls) and other tool names (`update_plan`, `view_image`, `wait`, …).

## Adding a format

Add a parser in `internal/parse` that implements `record(no, obj)` and
`finish()`. Feed results through `builder.addCommand` and
`builder.addFileChange`, add a detection rule in `detect`, and add per-format
notes in `support.go`. Add a synthetic fixture and golden test, and leave any
field the format does not record as nil.
