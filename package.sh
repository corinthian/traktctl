#!/usr/bin/env bash
# Package traktctl release artifacts, named to match the release/README
# convention:
#   traktctl-<version>-universal-macos.tar.gz
#   traktctl-<version>-arm64-macos.tar.gz
#   traktctl-<version>-amd64-macos.tar.gz
#   traktctl-<version>-SHA256SUMS          (checksums over the three tarballs)
#
# Each tarball holds a single binary named `traktctl`, so `tar -xzf <tarball>`
# yields `traktctl` regardless of arch. Wraps build.sh, which compiles the
# binaries (the universal one is ad-hoc codesigned there).
#
# Usage: ./package.sh [version]     # version defaults to the app.go constant
set -euo pipefail
cd "$(dirname "$0")"

VERSION="${1:-$(grep -m1 'Version =' internal/commands/app.go | cut -d'"' -f2)}"

echo "[package] traktctl ${VERSION}"
./build.sh "$VERSION"

cd dist
rm -f traktctl-*-macos.tar.gz traktctl-*-SHA256SUMS

# pack <arch-label> <built-binary-in-dist>
pack() {
  local label="$1" bin="$2"
  local tarball="traktctl-${VERSION}-${label}-macos.tar.gz"
  local stage
  stage="$(mktemp -d)"
  cp "$bin" "$stage/traktctl"
  tar -czf "$tarball" -C "$stage" traktctl
  rm -rf "$stage"
  echo "[package] $tarball"
}

pack universal traktctl
pack arm64     traktctl-arm64
pack amd64     traktctl-amd64

sums="traktctl-${VERSION}-SHA256SUMS"
shasum -a 256 traktctl-"${VERSION}"-*-macos.tar.gz > "$sums"
echo "[package] $sums"

echo
echo "Release artifacts in dist/:"
ls -1 "traktctl-${VERSION}"-*-macos.tar.gz "$sums"
echo
echo "Publish with:"
echo "  gh release create v${VERSION} \\"
echo "    dist/traktctl-${VERSION}-universal-macos.tar.gz \\"
echo "    dist/traktctl-${VERSION}-arm64-macos.tar.gz \\"
echo "    dist/traktctl-${VERSION}-amd64-macos.tar.gz \\"
echo "    dist/${sums}"
