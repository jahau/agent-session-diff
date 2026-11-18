#!/usr/bin/env sh
# Regenerates examples/report.txt and examples/report.html from the synthetic
# example sessions. CI runs this and fails if the committed files differ.
set -eu
cd "$(dirname "$0")/../examples"
go run ../cmd/agent-session-diff claude-code-session.jsonl codex-session.jsonl --no-color --limit 0 > report.txt
go run ../cmd/agent-session-diff claude-code-session.jsonl codex-session.jsonl --quiet --html report.html
