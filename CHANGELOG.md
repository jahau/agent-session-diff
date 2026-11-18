# Changelog

## Unreleased

Initial implementation.

- Compare two explicitly supplied session exports: file changes, commands,
  test runs, timing and field support.
- Formats: Claude Code session JSONL and Codex CLI rollout JSONL (2025 shell
  calls, 2026 `shell_command`, and current `CommandExecution`/`FileChange`
  items), detected automatically or set with `--format-a/--format-b`.
- Outputs: terminal summary, standalone HTML report (`--html`) and JSON
  (`--json`). Output is deterministic.
- `--redact`, `--redact-secrets`, `--redact-paths`.
- `inspect` subcommand prints one normalized session as JSON.
- Logged commands are never executed. A test enforces that no package can
  start processes or open network connections.
