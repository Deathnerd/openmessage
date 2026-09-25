#!/usr/bin/env bash
# Fail if `go mod tidy` would change go.mod or go.sum.
#
# Usage: scripts/ci/check-go-mod-tidy.sh
set -euo pipefail

go mod tidy
if ! git diff --exit-code -- go.mod go.sum; then
  echo "::error file=go.mod::go.mod/go.sum are not tidy. Run: go mod tidy"
  exit 1
fi
echo "go.mod and go.sum are tidy."
