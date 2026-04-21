# Upstream Refresh Candidate Plan

This note captures the reviewed redesign for upstream refresh after rejecting
GitHub-hosted maintainer signing.

## Goal

Keep weekday upstream-refresh detection, remove any stored maintainer signing
key from GitHub, and preserve the repo's host-side signed publication model.

## Consensus Design

Target `Option B`:

- GitHub Actions becomes a candidate-generation lane only.
- The workflow no longer pushes branches or opens pull requests.
- GitHub stores no maintainer signing key for upstream refresh.
- The authoritative refresh PR is created later on the host through
  `./scripts/repo-publish-pr.sh`.
- `Option A` remains breakglass-only.
- `Option C` is unsupported.

The candidate bundle and tracking issue are advisory only. They are operator
signals and traceability records, not integrity evidence. The later host-side
signed PR remains the only authoritative review unit.

## Environment Posture

Keep an empty `upstream-refresh` GitHub environment as an out-of-tree gate for
the candidate workflow.

Required posture:

- no variables
- no secrets
- selected branches limited to `main`
- `can_admins_bypass = false`

Keep `hosted-controls-audit` as a separate environment with only
`WORKCELL_HOSTED_CONTROLS_TOKEN`, also restricted to `main` with admin bypass
disabled.

## Why This Design

The current design signs on a GitHub runner using stored key material in
`.github/workflows/upstream-refresh.yml`. That conflicts with the repo's own
host-side publication model and with the requirement to avoid storing a
maintainer signing key in GitHub-hosted automation.

`Option A` was accepted only as breakglass because rewriting an already-open
unsigned PR creates review and provenance ambiguity.

`Option C` was rejected because a later signed follow-up commit does not make
the unsigned base history trustworthy.

## Rollout

### Phase 0: GitHub Pre-Stage

Apply GitHub-hosted changes before merging repo enforcement:

- reconfigure `upstream-refresh` as an empty environment
- set `upstream-refresh` to selected branches `main` only
- disable admin bypass on `upstream-refresh`
- set `hosted-controls-audit` to selected branches `main` only
- disable admin bypass on `hosted-controls-audit`
- keep only `WORKCELL_HOSTED_CONTROLS_TOKEN` in `hosted-controls-audit`
- remove any existing `upstream-refresh` signing variables and secrets

Phase 0 exit gate:

- verify the pre-staged GitHub state against the cutover branch's updated
  hosted-controls policy and validator before merging
- do not start the repo cutover merge sequence until that verification passes
- avoid an expected scheduled refresh failure during the gap between pre-stage
  and merge by either:
  - merging the cutover before the next scheduled `upstream-refresh` run
  - or temporarily pausing or disabling the current `upstream-refresh` workflow
    until the cutover lands
- do not manually dispatch the pre-cutover `upstream-refresh` workflow after
  removing its signing inputs unless that workflow has been paused or disabled

### Phase 1: Repo Cutover PR

Replace the current signed publisher in `.github/workflows/upstream-refresh.yml`
with a candidate-only workflow:

- keep weekday schedule and manual dispatch
- keep the empty `upstream-refresh` environment binding
- remove hosted signing inputs and maintainer identity inputs
- remove `git commit -S`, branch push, and GitHub-side PR creation
- reduce token permissions to read-only plus the minimum needed for issue
  updates and artifact upload
- upload a candidate bundle containing:
  - `patch`
  - `diffstat`
  - `metadata.json`
- open or update one rolling tracking issue with the latest candidate run

Add a new host-side helper, expected shape:

- `scripts/publish-upstream-refresh-pr.sh`
- modeled on `scripts/publish-provider-bump-pr.sh`
- starts from fresh `origin/main` in a disposable worktree
- reruns upstream refresh locally
- requires exact candidate base-SHA match before publication
- requires exact candidate patch and tree or content digest match before
  publication when the PR body links a candidate run
- runs:
  - `./scripts/update-upstream-pins.sh --check`
  - `./scripts/check-pinned-inputs.sh`
  - `./scripts/pre-merge.sh --profile pr-parity --allow-dirty`
- publishes a signed draft PR through `./scripts/repo-publish-pr.sh`
- links the candidate run and tracking issue in the PR body
- refuses ambiguous state, including:
  - an already-open refresh PR
  - stale candidate base SHA
  - unexpected diff shape
  - multiple candidate issues or mismatched candidate identity

Update repo validation and contract surfaces in the same PR:

- `internal/metadatautil/validate.go`
- `internal/metadatautil/*test.go`
- `policy/github-hosted-controls.toml`
- `.github/workflows/hosted-controls.yml`
- `policy/workflow-lane-policy.json`
- `policy/workflow-lanes.json`
- `docs/github-workflows.md`
- any workflow-lane or operator-contract evidence touched by the changed path

Validation must reject any upstream-refresh workflow that still:

- references `WORKCELL_UPSTREAM_REFRESH_GPG_PRIVATE_KEY`
- references `WORKCELL_UPSTREAM_REFRESH_GPG_FINGERPRINT`
- imports a maintainer key on-runner
- uses `git commit -S` in GitHub Actions for refresh publication
- pushes a refresh branch from GitHub Actions
- opens a refresh pull request from GitHub Actions

Hosted-controls hardening is part of the same cutover PR, not an immediate
follow-up:

- make `.github/workflows/hosted-controls.yml` fail closed
- set `WORKCELL_HOSTED_CONTROLS_REQUIRED="1"`
- extend hosted-controls validation to audit:
  - exact environment contents
  - branch restriction posture
  - `can_admins_bypass = false`
- remove obsolete `upstream-refresh` signing expectations from policy and
  validators

### Phase 2: Verification

- run `./scripts/verify-github-hosted-controls.sh` again after merge from
  updated `main`
- manually dispatch `upstream-refresh` on `main`
- confirm the workflow creates an artifact and issue update, not a branch or PR
- exercise the new host-side helper end to end
- confirm it opens one signed draft refresh PR only after candidate identity
  checks succeed
- follow the PR and merged `main` workflows until repo-owned lanes are green

### Phase 3: External Follow-Up

- update the Codex-home `workcell-release` skill to use
  `./scripts/repo-publish-pr.sh`
- keep that change separate from the repo cutover unless the implementation
  deliberately includes local Codex-home maintenance

## Candidate Bundle Rules

`metadata.json` should include at least:

- candidate run URL
- triggering workflow run id
- base ref and base SHA
- candidate patch SHA256
- candidate tree identifier or equivalent content digest
- changed-file list

The rolling issue should use one stable label and clearly mark the latest
candidate while superseding older ones.

## Skill Updates

### Repo-Local Skills

`workcell-pr-lifecycle`

- add a special-case note once `scripts/publish-upstream-refresh-pr.sh` exists
- make clear that upstream-refresh candidate issues and artifacts are advisory
  only
- make clear that authoritative refresh publication goes through the new
  host-side helper, which then uses `./scripts/repo-publish-pr.sh`

`workcell-contract-parity`

- no logic change required
- use it during implementation because this redesign changes a user-visible
  workflow, docs, validators, and evidence

### Codex-Home Skill

`workcell-release`

- update `$CODEX_HOME/skills/workcell-release/SKILL.md`
- it currently instructs release PR publication via
  `./scripts/workcell publish-pr`
- the repo runbook requires `./scripts/repo-publish-pr.sh`
- track this as a post-cutover external follow-up, not as a required repo PR
  change unless the implementation deliberately includes Codex-home
  maintenance work

## Scope Cuts

Do not include in phase 1:

- artifact attestation for the candidate bundle
- auto-promotion
- auto-ready or auto-merge
- generalized `workcell publish-pr` redesign
- stale-candidate reminders or notification workflows

## Breakglass

`Option A` may remain as an explicit lower-assurance fallback only if all of
the following are true:

- the operator explicitly acknowledges the breakglass path
- the run leaves a durable waiver trail in issue or PR commentary
- the unsigned automation PR stays draft-only
- it is fully replaced by a locally signed rewrite before ready-for-review
- the unsigned PR is immediately closed or superseded once the signed rewrite
  exists
- CI is rerun from the rewritten head
- docs label the path as lower assurance

`Option C` should be explicitly unsupported.

## Review Outcome

Peer review converged across SWE, security engineer, engineering manager, and
program manager roles on this plan with one final constraint:

- pre-stage GitHub environment protections first
- then land the repo cutover in one bounded PR

That ordering avoids leaving `main` red while the repo begins enforcing a
hosted-control posture that GitHub has not yet been configured to satisfy.
