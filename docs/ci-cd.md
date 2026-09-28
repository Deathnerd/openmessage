# CI/CD for the Deathnerd/openmessage fork

How code gets from a branch to a release in this fork, and how to change
that process. Everything here is code in the repo; the GitHub UI is only
where it runs.

## Design rules

1. **Upstream files stay untouched where possible.** `test.yml` and
   `gmessages-fork-drift.yml` belong to MaxGhenis/openmessage. The fork adds
   checks in `fork-*.yml` files instead of editing them, so weekly upstream
   syncs merge cleanly. `release.yml` and the `Dockerfile` are the deliberate
   exceptions: the fork owns its releases.
2. **Workflows are thin; scripts hold the logic.** Every non-trivial step
   calls `scripts/ci/*.sh`, which you can run locally with the same result.
3. **Settings are code.** Branch and tag protection live in
   `.github/rulesets/*.json` and are applied by a script, not clicked in.
4. **One required check per fork workflow.** `Fork CI` funnels into
   `CI Gate`; adding a job to its `needs` makes it required with no settings
   change.

## Map

| File | Runs on | Does |
| --- | --- | --- |
| `.github/workflows/test.yml` *(upstream)* | PR, push to main | Go tests + coverage floor, race, Playwright e2e, site build, macOS app build |
| `.github/workflows/fork-ci.yml` | PR, push to main, weekly, manual | Lint, Windows tests, cross-compile, container build + smoke, govulncheck → **CI Gate** |
| `.github/workflows/release.yml` | `v*` tag, manual | Verify tag → CLI archives + macOS DMG → GHCR image + GitHub Release |
| `.github/workflows/fork-upstream-sync.yml` | weekly, manual | Merge upstream into `sync/upstream`, open a PR |
| `.github/workflows/gmessages-fork-drift.yml` *(upstream)* | weekly | Checks the pinned gmessages fork against mautrix upstream |
| `.github/rulesets/main.json` | applied by script | `main`: PR required, required checks, no force-push or deletion |
| `.github/rulesets/release-tags.json` | applied by script | `v*` tags cannot be moved or deleted |
| `.github/dependabot.yml` | monthly | Bumps SHA-pinned actions |

## Required checks on `main`

`CI Gate` (fork CI) plus upstream's `Go Test`, `Go Race`, `Web E2E`,
`Site Build`, `macOS App Build`. Each is pinned to the GitHub Actions app
(`integration_id` 15368) so nothing else can post a passing status with the
same name.

Checks are matched **by job name**. If an upstream sync renames a job in
`test.yml`, PRs wait forever on the old name: update `main.json` in the same
PR and re-apply.

Admins can bypass the rules only through a PR (`bypass_mode: pull_request`),
never by pushing directly.

## Running the checks locally

```bash
scripts/ci/check-gofmt.sh origin/main   # gofmt on Go files your branch changed
scripts/ci/check-go-mod-tidy.sh         # go.mod/go.sum are tidy
scripts/ci/cross-build.sh               # every release target compiles
go vet ./... && go test ./...
```

**gofmt covers changed files only.** Several upstream files aren't
gofmt-clean. Reformatting them would make every sync conflict, so new and
edited code must be clean and untouched upstream code is left as-is.

## Go toolchain

Fork workflows build with the newest stable Go, not `go.mod`'s `go 1.25.0`.
`go.mod` states a minimum, and 1.25.0 carries known stdlib CVEs that
govulncheck reports as reachable. To pin a version, set the repository
variable `GO_VERSION` (e.g. `1.27.x`). Upstream's `test.yml` still uses
`go.mod`, which also keeps the minimum version tested.

## Applying repository settings

After editing anything in `.github/rulesets/`:

```bash
scripts/ci/apply-repo-settings.sh --dry-run   # see what would change
scripts/ci/apply-repo-settings.sh             # apply (needs repo admin)
```

- Rulesets are matched by `name`. Keep each file named `<name>.json`.
- Re-applying overwrites any edits made in the UI.
- A ruleset that exists on GitHub but has no file triggers a warning. It is
  never deleted automatically.
- The script sets `allow_rebase_merge=false`: rebase-merging a sync PR
  rewrites upstream's commits.
- The repo defaults to `Deathnerd/openmessage` and is never inferred from
  `gh`. Inside a fork, `gh` resolves to the upstream parent.

## Releasing

1. Merge to `main` (CI green, enforced by the ruleset).
2. Tag and push: `git tag v0.3.0 && git push origin v0.3.0`.
3. `release.yml` then:
   - **verifies** the tag is `vX.Y.Z[-suffix]` and on `main`, and refuses
     anything else
   - builds `openmessage-{darwin,linux,windows}-{amd64,arm64}` archives (the
     target list lives in `scripts/ci/cross-build.sh`) and the macOS DMG
   - publishes `ghcr.io/deathnerd/openmessage:{X.Y.Z, X.Y, latest}` for
     amd64 and arm64, with SBOM and provenance
   - creates the GitHub Release with generated notes and `SHA256SUMS`

Suffixes containing `-rc`, `-alpha` or `-beta` are published as prereleases
and don't move `:latest` or `:X.Y`. Any other suffix, such as a fork scheme
like `v0.2.9-win.1`, publishes as a normal release.

To rebuild an existing tag's assets, run the workflow manually with its `tag`
input.

**Optional secrets for signed macOS builds:** `MACOS_CERT_P12_BASE64`,
`MACOS_CERT_PASSWORD`, `DEVELOPER_ID`, `AC_USERNAME`, `AC_PASSWORD`,
`AC_TEAM_ID`. Without them the DMG is ad-hoc signed.

## Upstream sync

Every Monday, `fork-upstream-sync.yml` merges `MaxGhenis/openmessage@main`
into the `sync/upstream` branch and opens a PR. CI runs on that PR like any
other.

- **Merge sync PRs with "Create a merge commit".** Squashing drops upstream's
  ancestry, so the next sync re-applies everything and conflicts.
- If a sync PR is already open, the job merges new upstream commits into its
  branch, so fixes you pushed to it are kept.
- Upstream tags are never fetched, so they can't trigger fork releases.

**One-time setup:** create a fine-grained PAT scoped to this repo with
*Contents*, *Pull requests* and *Workflows* set to read/write. Save it as the
`UPSTREAM_SYNC_TOKEN` secret. `GITHUB_TOKEN` won't work here for two
reasons: it can't push upstream's workflow-file changes, and PRs it opens
don't trigger CI. Without the secret, the job only reports drift in its
summary.

### Resolving a conflicted upstream sync

The job fails and lists the conflicting files in its summary. Resolve them
locally:

```bash
git fetch upstream main && git fetch origin
git switch -C sync/upstream origin/main
git merge upstream/main          # resolve conflicts, then commit
git push -u origin sync/upstream --force-with-lease
gh pr create --repo Deathnerd/openmessage --base main --head sync/upstream
```

The next scheduled run finds the open PR and builds on it.

## Fork gotchas

- **Scheduled workflows are off by default in forks.** Enable them once under
  *Actions*, or with
  `gh workflow enable fork-upstream-sync.yml --repo Deathnerd/openmessage`.
- **Dependabot version updates must be enabled** in *Settings → Code security*.
- **A new GHCR package is private.** After the first release, set
  `ghcr.io/deathnerd/openmessage` to public under *Packages → Package
  settings* so `docker pull` works without logging in.
- **Dependabot covers only GitHub Actions**, and skips upstream's workflow
  files. Go and npm bumps come through
  upstream syncs, so fork-side bumps don't cause `go.sum` conflicts.
  govulncheck flags anything urgent in the meantime.
