# Changelog

## v0.1.0

First public release.

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
- Reproducible prebuilt archives for Linux, macOS and Windows (amd64, arm64)
  with `SHA256SUMS`.

Fixed during release preparation:

- `agent-session-diff version` printed `dev` for `go install …@vX.Y.Z`
  builds. It now reports the module version.
- With `--redact-paths`, two different working directories both appeared as
  `<cwd>` and were reported as equal. They now appear as `<cwd> (A)` and
  `<cwd> (B)`.
- Control characters from logs (such as ESC and BEL) reached the HTML report
  as raw bytes. They are now written as `\xNN`, as in the terminal output.
