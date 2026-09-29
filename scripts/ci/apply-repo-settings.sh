#!/usr/bin/env bash
# Apply the repository settings and rulesets kept in .github/ to GitHub.
# Idempotent: rulesets are matched by name and created or replaced in full,
# so the JSON files are the source of truth. Edits made in the web UI are
# overwritten on the next apply.
#
# Needs: gh (authenticated as a repo admin), jq.
#
# Usage: scripts/ci/apply-repo-settings.sh [--dry-run] [owner/repo]
#   owner/repo defaults to Deathnerd/openmessage. It is never inferred from
#   gh, because inside a fork gh resolves to the upstream parent.
set -euo pipefail

dry_run=false
if [ "${1:-}" = "--dry-run" ]; then
  dry_run=true
  shift
fi
repo="${1:-Deathnerd/openmessage}"
root="$(git rev-parse --show-toplevel)"

run() {
  if $dry_run; then
    echo "  would run: $*"
  else
    "$@" >/dev/null
  fi
}

echo "Repository settings for ${repo}:"
# Merge commits must stay enabled: upstream sync PRs need them (see docs/ci-cd.md).
run gh api --method PATCH "repos/${repo}" \
  -F allow_merge_commit=true \
  -F allow_squash_merge=true \
  -F allow_rebase_merge=false \
  -F delete_branch_on_merge=true \
  -F allow_update_branch=true

existing="$(gh api "repos/${repo}/rulesets" --jq 'map({(.name): .id}) | add // {}')"

for file in "$root"/.github/rulesets/*.json; do
  name="$(jq -r .name "$file")"
  id="$(jq -r --arg n "$name" '.[$n] // empty' <<<"$existing")"
  if [ -n "$id" ]; then
    echo "Ruleset '${name}': update (id ${id})"
    run gh api --method PUT "repos/${repo}/rulesets/${id}" --input "$file"
  else
    echo "Ruleset '${name}': create"
    run gh api --method POST "repos/${repo}/rulesets" --input "$file"
  fi
done

# Rulesets present on GitHub but absent from .github/rulesets/ are reported,
# not deleted: removing enforcement should be a deliberate, manual act.
jq -r 'keys[]' <<<"$existing" | while read -r name; do
  [ -f "$root/.github/rulesets/${name}.json" ] ||
    echo "::warning::Ruleset '${name}' exists on GitHub but has no file in .github/rulesets/."
done

$dry_run && echo "Dry run: nothing changed."
exit 0
