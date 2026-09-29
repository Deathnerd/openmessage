#!/usr/bin/env bash
# Build the CLI for every release target. This list is the single source of
# truth: CI compiles it on every PR (so a build-tag slip on one OS fails the
# PR, not the release) and the release workflow packages from it.
#
# Usage:
#   scripts/ci/cross-build.sh                    compile every target, discard output
#   scripts/ci/cross-build.sh --package <tag>    build + archive into dist/
#
# Archives: openmessage-<os>-<arch>.tar.gz (unix) / .zip (windows), each
# holding a single openmessage[.exe] binary.
set -euo pipefail

TARGETS=(
  darwin/amd64
  darwin/arm64
  linux/amd64
  linux/arm64
  windows/amd64
  windows/arm64
)

version=""
if [ "${1:-}" = "--package" ]; then
  version="${2:?--package needs a version, e.g. v0.3.0}"
fi

out="dist"
rm -rf "$out"
mkdir -p "$out"

for target in "${TARGETS[@]}"; do
  goos="${target%/*}"
  goarch="${target#*/}"
  ext=""
  [ "$goos" = "windows" ] && ext=".exe"
  stage="$out/stage/${goos}-${goarch}"
  mkdir -p "$stage"

  echo "==> ${goos}/${goarch}"
  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${version:-dev}" \
    -o "$stage/openmessage${ext}" .

  [ -z "$version" ] && continue
  name="openmessage-${goos}-${goarch}"
  if [ "$goos" = "windows" ]; then
    (cd "$stage" && zip -q "../../${name}.zip" "openmessage${ext}")
  else
    tar -C "$stage" -czf "$out/${name}.tar.gz" openmessage
  fi
done

rm -rf "$out/stage"
if [ -n "$version" ]; then
  ls -l "$out"
else
  rmdir "$out"
  echo "All ${#TARGETS[@]} targets compile."
fi
