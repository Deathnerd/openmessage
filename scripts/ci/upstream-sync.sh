#!/usr/bin/env bash
# Merge upstream into a sync branch and open (or refresh) a PR for it.
#
# Modes (SYNC_MODE):
#   pr      merge upstream/<branch> into $SYNC_BRANCH, push, open/refresh a PR
#   report  only report how far behind upstream the fork is
#
# A merge conflict fails the run, listing the conflicted files, so a human
# resolves it locally (recipe in docs/ci-cd.md).
#
# Required env: UPSTREAM_REPO (owner/name), REPO (this fork, owner/name)
# Optional env: UPSTREAM_BRANCH (main), BASE_BRANCH (main),
#               SYNC_BRANCH (sync/upstream), SYNC_MODE (pr),
#               GITHUB_STEP_SUMMARY (job summary file)
set -euo pipefail

: "${UPSTREAM_REPO:?}" "${REPO:?}"
UPSTREAM_BRANCH="${UPSTREAM_BRANCH:-main}"
BASE_BRANCH="${BASE_BRANCH:-main}"
SYNC_BRANCH="${SYNC_BRANCH:-sync/upstream}"
SYNC_MODE="${SYNC_MODE:-pr}"
summary="${GITHUB_STEP_SUMMARY:-/dev/stdout}"

git remote get-url upstream >/dev/null 2>&1 ||
  git remote add upstream "https://github.com/${UPSTREAM_REPO}.git"
# --no-tags: upstream's release tags must never land in the fork, where a
# pushed v* tag would publish a fork release.
git fetch --quiet --no-tags upstream "+refs/heads/${UPSTREAM_BRANCH}:refs/remotes/upstream/${UPSTREAM_BRANCH}"
git fetch --quiet --no-tags origin "+refs/heads/${BASE_BRANCH}:refs/remotes/origin/${BASE_BRANCH}"

upstream_ref="upstream/${UPSTREAM_BRANCH}"
base_ref="origin/${BASE_BRANCH}"
behind="$(git rev-list --count "${base_ref}..${upstream_ref}")"
ahead="$(git rev-list --count "${upstream_ref}..${base_ref}")"

{
  echo "## Upstream sync: ${UPSTREAM_REPO}@${UPSTREAM_BRANCH}"
  echo
  echo "Fork \`${BASE_BRANCH}\` is **${behind}** commit(s) behind and **${ahead}** ahead."
} >>"$summary"

if [ "$behind" -eq 0 ]; then
  echo "Nothing to sync." >>"$summary"
  exit 0
fi

{
  echo
  echo '<details><summary>Incoming upstream commits</summary>'
  echo
  echo '```text'
  git log --oneline --no-merges "${base_ref}..${upstream_ref}" | head -n 100
  echo '```'
  echo '</details>'
} >>"$summary"

if [ "$SYNC_MODE" = "report" ]; then
  echo "::notice title=Upstream sync is report-only::Set the UPSTREAM_SYNC_TOKEN secret to have this job open sync PRs (see docs/ci-cd.md)."
  exit 0
fi

# Reuse an open sync PR's branch so fixes pushed to it by hand survive;
# otherwise start fresh from the fork's base branch.
open_pr="$(gh pr list --repo "$REPO" --head "$SYNC_BRANCH" --state open --json number --jq '.[0].number // empty')"
if [ -n "$open_pr" ]; then
  git fetch --quiet origin "+refs/heads/${SYNC_BRANCH}:refs/remotes/origin/${SYNC_BRANCH}"
  git switch --quiet -C "$SYNC_BRANCH" "origin/${SYNC_BRANCH}"
else
  git switch --quiet -C "$SYNC_BRANCH" "$base_ref"
fi

git config user.name "github-actions[bot]"
git config user.email "41898282+github-actions[bot]@users.noreply.github.com"

if ! git merge --no-ff --no-edit -m "Merge ${UPSTREAM_REPO}@${UPSTREAM_BRANCH} into ${BASE_BRANCH}" "$upstream_ref"; then
  mapfile -t conflicts < <(git diff --name-only --diff-filter=U)
  git merge --abort
  {
    echo
    echo "### Merge conflicts"
    # shellcheck disable=SC2016 # backticks are Markdown, not command substitution
    printf -- '- `%s`\n' "${conflicts[@]}"
    echo
    echo "Resolve locally; see docs/ci-cd.md, \"Resolving a conflicted upstream sync\"."
  } >>"$summary"
  echo "::error title=Upstream sync conflicts::${#conflicts[@]} file(s) conflict with ${UPSTREAM_REPO}. See the job summary."
  exit 1
fi

if [ -n "$open_pr" ]; then
  git push --quiet origin "$SYNC_BRANCH"
else
  git push --quiet --force-with-lease origin "$SYNC_BRANCH"
fi

title="Sync upstream ${UPSTREAM_REPO} (${behind} commits)"
body="$(cat <<EOF
Automated merge of [\`${UPSTREAM_REPO}@${UPSTREAM_BRANCH}\`](https://github.com/${UPSTREAM_REPO}/tree/${UPSTREAM_BRANCH}) into \`${BASE_BRANCH}\`.

> [!IMPORTANT]
> Merge this PR with **Create a merge commit**. Squash or rebase drops upstream's
> ancestry, so the next sync re-applies every commit and conflicts.

Opened by \`.github/workflows/fork-upstream-sync.yml\`; see the workflow run summary for the incoming commit list.
EOF
)"

if [ -n "$open_pr" ]; then
  gh pr edit "$open_pr" --repo "$REPO" --title "$title" >/dev/null
  echo "Updated PR #${open_pr}." >>"$summary"
else
  url="$(gh pr create --repo "$REPO" --base "$BASE_BRANCH" --head "$SYNC_BRANCH" --title "$title" --body "$body")"
  echo "Opened ${url}" >>"$summary"
fi
