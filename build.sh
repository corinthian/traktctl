#!/usr/bin/env bash
# Build the universal macOS traktctl binary (arm64 + amd64) plus per-arch
# artifacts. Ad-hoc codesign; notarization is a later concern.
set -euo pipefail
cd "$(dirname "$0")"

VERSION="${1:-$(grep -m1 'Version =' internal/commands/app.go | cut -d'"' -f2)}"
mkdir -p dist

GOVULNCHECK="$(command -v govulncheck || true)"
if [ -z "$GOVULNCHECK" ]; then
  echo "[build] govulncheck not found; installing..."
  go install golang.org/x/vuln/cmd/govulncheck@latest
  GOVULNCHECK="$(go env GOPATH)/bin/govulncheck"
fi
echo "[build] govulncheck ./..."
"$GOVULNCHECK" ./...

echo "[build] traktctl ${VERSION}"
GOOS=darwin GOARCH=arm64 go build -trimpath -o dist/traktctl-arm64 ./cmd/traktctl
GOOS=darwin GOARCH=amd64 go build -trimpath -o dist/traktctl-amd64 ./cmd/traktctl

lipo -create -output dist/traktctl dist/traktctl-arm64 dist/traktctl-amd64
codesign --sign - --options runtime --force dist/traktctl

echo "[build] universal binary:"
lipo -info dist/traktctl

# Toolchain hygiene: every shipped artifact must be built by a compiler at
# least as new as go.mod's `go` directive, so a stale local toolchain can't
# silently ship an old (potentially vulnerable) stdlib.
required_go="$(grep -m1 '^go ' go.mod | awk '{print $2}')"
for bin in dist/traktctl-arm64 dist/traktctl-amd64 dist/traktctl; do
  built_go="$(go version "$bin" | awk '{print $2}' | sed 's/^go//')"
  newest="$(printf '%s\n%s\n' "$required_go" "$built_go" | sort -V | tail -1)"
  if [ "$newest" != "$built_go" ]; then
    echo "[build] FATAL: $bin built with go${built_go}, older than go.mod's required go${required_go}" >&2
    exit 1
  fi
  echo "[build] $bin: go${built_go} (>= go${required_go} required) OK"
done
