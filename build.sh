#!/usr/bin/env bash
# Build the universal macOS traktctl binary (arm64 + amd64) plus per-arch
# artifacts. Ad-hoc codesign; notarization is a later concern.
set -euo pipefail
cd "$(dirname "$0")"

VERSION="${1:-$(grep -m1 'Version =' internal/commands/app.go | cut -d'"' -f2)}"
mkdir -p dist

echo "[build] traktctl ${VERSION}"
GOOS=darwin GOARCH=arm64 go build -trimpath -o dist/traktctl-arm64 ./cmd/traktctl
GOOS=darwin GOARCH=amd64 go build -trimpath -o dist/traktctl-amd64 ./cmd/traktctl

lipo -create -output dist/traktctl dist/traktctl-arm64 dist/traktctl-amd64
codesign --sign - --options runtime --force dist/traktctl

echo "[build] universal binary:"
lipo -info dist/traktctl
