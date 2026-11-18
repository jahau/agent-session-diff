# License review

Reviewed: 2026-11-18, for the initial implementation and again for v0.1.0.

## Project license

MIT (see `LICENSE`), matching the other repositories under the same owner.

## Dependencies

| Component | Version | License | How it is used |
| --- | --- | --- | --- |
| Go standard library | toolchain ≥ 1.24 | BSD-3-Clause | Compiled into the binary. |

`go.mod` declares no `require` directives, so `go list -m all` lists only this
module. CI fails if a dependency is added without updating this review (the
`deps` step in `.github/workflows/ci.yml`).

The Go standard library's BSD-3-Clause license allows redistribution of
binaries if its notice is included. Every release archive built by
`scripts/build-release.sh` contains this project's `LICENSE` and the Go
toolchain's notice as `LICENSE-Go`.

## CI-only tools (not distributed)

| Tool | License | Use |
| --- | --- | --- |
| `actions/checkout`, `actions/setup-go` | MIT | GitHub Actions steps. |
| `golang.org/x/vuln/cmd/govulncheck` v1.9.0 | BSD-3-Clause | Vulnerability scan in CI. Fetched at run time, not vendored or linked. |
| GNU tar, gzip, zip (ubuntu-latest runner) | GPL / zip license | Package release archives. Not included in the archives. |

## Repository content

- Source code: original work under MIT.
- Fixtures (`examples/*.jsonl`, `testdata/**`): synthetic, written for this
  project. They mimic the structure of Claude Code and Codex CLI exports but
  contain no copied session content. Fake credentials in them are made-up
  values for redaction tests. The only well-known value is AWS's documented
  example key `AKIAIOSFODNN7EXAMPLE`, assembled at run time in a unit test.
- Screenshots (`docs/screenshots/*.png`): rendered from this tool's own output
  on the synthetic fixtures with a headless browser. They contain no
  third-party artwork.
- Product names ("Claude Code", "Codex") are used only to identify the file
  formats the tool reads. No logos or branding are included.

## Format compatibility

Reading these export files involves no licensed code. The parsers were written
from observed file structure, not from either vendor's source.
