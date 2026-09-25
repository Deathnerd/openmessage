#!/usr/bin/env bash
# Fail if any Go file changed since <base-ref> is not gofmt-clean.
#
# Only changed files are checked on purpose: several upstream-owned files are
# not gofmt-clean, and reformatting them here would turn every upstream sync
# into a merge conflict. New and edited code is held to the standard; untouched
# upstream code is left alone.
#
# Usage: scripts/ci/check-gofmt.sh <base-ref>
#   base-ref  commit or ref to diff against (e.g. origin/main). An empty or
#             all-zero ref (a new branch or tag push) checks nothing.
set -euo pipefail

base="${1:-}"
if [ -z "$base" ] || [ -z "${base//0/}" ]; then
  echo "gofmt: no base ref to diff against; skipping."
  exit 0
fi
if ! git cat-file -e "${base}^{commit}" 2>/dev/null; then
  # e.g. the "before" SHA of a force push, which no longer exists.
  echo "::warning::gofmt: base ${base} is not in this clone; skipping."
  exit 0
fi

merge_base="$(git merge-base "$base" HEAD)"
mapfile -t files < <(git diff --name-only --diff-filter=ACMR "$merge_base" HEAD -- '*.go')

if [ "${#files[@]}" -eq 0 ]; then
  echo "gofmt: no Go files changed since ${merge_base:0:12}."
  exit 0
fi

mapfile -t unformatted < <(gofmt -l "${files[@]}")
if [ "${#unformatted[@]}" -gt 0 ]; then
  for f in "${unformatted[@]}"; do
    echo "::error file=${f}::not gofmt-clean. Run: gofmt -w ${f}"
  done
  exit 1
fi

echo "gofmt: ${#files[@]} changed Go file(s) are clean."
