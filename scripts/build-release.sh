#!/usr/bin/env bash
# Builds reproducible release archives:
#   scripts/build-release.sh <version> [outdir]
#
# The same commit, version and Go toolchain produce byte-identical archives:
# CGO is off, paths are trimmed, the build ID is empty, VCS stamping is off
# (the version comes from -ldflags), and archive entries are sorted, owned by
# 0:0 and timestamped with SOURCE_DATE_EPOCH (default: the commit time).
# Requires GNU tar, gzip, zip and sha256sum (all present on ubuntu-latest).
set -euo pipefail

version="${1:?usage: build-release.sh <version> [outdir]}"
out="${2:-dist}"
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

: "${SOURCE_DATE_EPOCH:=$(git log -1 --format=%ct)}"
export SOURCE_DATE_EPOCH TZ=UTC
stamp="$(date -u -d "@$SOURCE_DATE_EPOCH" +%Y%m%d%H%M.%S)"

targets="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"
rm -rf "$out" && mkdir -p "$out"
out="$(cd "$out" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

for t in $targets; do
	os="${t%/*}" arch="${t#*/}"
	name="agent-session-diff_${version#v}_${os}_${arch}"
	dir="$work/$name"
	mkdir -p "$dir"
	exe="agent-session-diff"
	[ "$os" = windows ] && exe="$exe.exe"
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" GOFLAGS= \
		go build -trimpath -buildvcs=false \
		-ldflags "-s -w -buildid= -X main.version=$version" \
		-o "$dir/$exe" ./cmd/agent-session-diff
	cp LICENSE README.md CHANGELOG.md "$dir/"
	# The binary embeds the Go standard library (BSD-3-Clause); ship its notice.
	cp "$(go env GOROOT)/LICENSE" "$dir/LICENSE-Go"
	find "$dir" -exec touch -t "$stamp" {} +
	if [ "$os" = windows ]; then
		(cd "$work" && find "$name" -type f | LC_ALL=C sort | zip -q -X -D "$out/$name.zip" -@)
	else
		tar --sort=name --owner=0 --group=0 --numeric-owner --mtime="@$SOURCE_DATE_EPOCH" \
			--format=gnu -C "$work" -cf - "$name" | gzip -n -9 > "$out/$name.tar.gz"
	fi
done

(cd "$out" && sha256sum -- *.tar.gz *.zip | LC_ALL=C sort -k2 > SHA256SUMS)
cat "$out/SHA256SUMS"
