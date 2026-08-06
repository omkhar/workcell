# GitHub workflow design

Workcell uses a reviewed GitHub workflow set.
The workflows support the runtime boundary and release process.
They do not replace the runtime boundary.

[Retention policy](retention-policy.md) defines workflow artifact retention.

## Lane inventory

These files define the machine-readable lane inventory:

- [`policy/workflow-lane-policy.json`](../policy/workflow-lane-policy.json) defines each lane and its authority.
- [`policy/workflow-lanes.json`](../policy/workflow-lanes.json) contains the generated lane manifest.

`./scripts/verify-workflow-lanes.sh` rejects manifest drift.
`./scripts/ci-plan.sh` shows the selected local and hosted lanes.

The changed-file planner fails closed for unsafe Git state.
It rejects split indexes, shallow graphs, unsafe ancestry, mutable ignore files, and present gitlinks.
It ignores `.git/info/exclude` and disables Git conversion filters.

`./scripts/pre-merge.sh` runs each selected local lane in `local_order`.
It reads the complete plan before it starts a lane.
Thus, a lane cannot consume a later plan record from standard input.

For an approved large adapter PR, use both required options:

```bash
./scripts/pre-merge.sh \
  --profile pr-parity \
  --label approved-large-certified-adapter

./scripts/repo-publish-pr.sh \
  --approved-large-certified-adapter \
  ...
```

## Workflow inventory

| Workflow | Purpose |
| --- | --- |
| `bench.yml` | Measures exec-guard performance on a schedule or manual run. |
| `ci-insights.yml` | Writes weekly flake and cost reports. See [CI reliability](ci-efficiency-and-reliability.md). |
| `ci.yml` | Runs repository validation, smoke tests, reproducibility, install checks, and PR-shape checks. |
| `codeql.yml` | Scans the shipped Go, Rust, and JavaScript code. |
| `docs.yml` | Checks spelling, links, contracts, and the man page. |
| `fuzz.yml` | Runs extended Go and Rust fuzz tests. |
| `hosted-controls.yml` | Checks GitHub controls that are outside Git. |
| `mutation.yml` | Runs the mutation-score gate on a schedule, manual run, or approved heavy PR. |
| `pin-hygiene.yml` | Checks reviewed upstream and tool pins. |
| `pr-base-policy.yml` | Requires `main` as the base for a ready PR. |
| `release.yml` | Builds, verifies, signs, and publishes a release. |
| `scorecard.yml` | Runs OpenSSF Scorecard analysis. |
| `security.yml` | Checks workflow policy, dependencies, and GitHub Actions security. |
| `upstream-refresh.yml` | Creates an advisory upstream-pin candidate and updates the tracking issue. |

## CI routing

Normal PRs run the required deterministic lanes.
The `approved-heavy-ci` label enables these expensive PR lanes:

- native amd64 and arm64 reproducible builds
- install verification on `macos-26` and `macos-15`
- CodeQL language jobs
- mutation tests

Native reproducibility and install verification also run on each push to `main`.
CodeQL also runs on each push to `main` and its weekly schedule.
The mutation workflow also runs on its weekly schedule.
All four lane groups support manual dispatch.

The aggregate reproducibility check passes on a normal PR when the platform matrix result is `skipped`.
It requires both platform jobs on `main`, an approved heavy PR, or manual dispatch.

## Hosted install evidence

The hosted macOS jobs test five install properties:

- bundle installation
- launcher-link removal
- man-page-link removal
- Homebrew installation and formula removal

The jobs run `scripts/uninstall.sh` for the bundle.
They assert only the two link removals after that command.
They do not prove complete bundle uninstall behavior.

The Homebrew job verifies that `brew uninstall` removes the formula.

GitHub-hosted macOS does not prove the strict Colima boundary.
Local certification supplies that proof.

## Release workflow

The preflight job records the expected digest for its source archive.
The release job independently creates and extracts its own archive from the checked-out release tag.
It then compares the archive digest with the expected digest.
It creates source-dependent manifests and the amd64 image from the extracted tree.
It creates the formula from the verified archive digest.
The native arm64 image job builds from the checked-out release tag.

The workflow also creates the builder-environment manifest, image-digest file, software bills of materials, signatures, and checksums.
The release job seals the assets in one workflow artifact.
The final job publishes that sealed artifact.

Release preflight does these checks:

- repository validation and container smoke tests
- provider and upstream-pin verification
- source-bundle and runtime-image reproducibility
- release input and control-plane manifests
- hosted control verification
- native amd64 and arm64 image builds
- the hosted install evidence in this page
- release CodeQL analysis

The release uses native `ubuntu-latest` for amd64.
It uses native `ubuntu-24.04-arm` for arm64.
The workflow compares both platform digests with preflight data.
Then it creates one multi-platform manifest.

The `release` environment gates image pushes and sealed-asset construction.
No image reaches GHCR before that gate.
The `hosted-controls-audit` environment gates release preflight and final GitHub release publication.

The release uses Cosign to create keyless Sigstore signatures.
It also creates GitHub attestations when the reviewed hosted controls permit them.
GitHub attestations do not replace Sigstore signatures.

The final publisher has `actions: read` and `contents: write` permissions.
It checks hosted controls immediately before publication.
It removes the administration token before it uses the default publication token.

## Upstream refresh

`upstream-refresh.yml` runs each day and on manual dispatch.
It checks these pin groups:

- provider releases
- Linux runtime and validator images
- Debian snapshot data
- Go, Rust, Hadolint, and release tools

A Codex change also checks the classified command inventory.
The workflow stops if the command set changes.

The workflow uploads an advisory candidate bundle.
That bundle contains `patch`, `diffstat`, and `metadata.json`.
It also updates one tracking issue.

The workflow does not push a branch or open a PR.
Use `./scripts/publish-upstream-refresh-pr.sh` for host publication.
That helper recreates the change, checks candidate identity, runs `pr-parity`, and uses the repository PR wrapper.

The candidate artifact and issue are operator signals.
They are not integrity evidence.

## Action and tool pins

Use a full commit SHA for each GitHub Action reference.
List each publisher in [`policy/allowed-actions.toml`](../policy/allowed-actions.toml) before you use its action.

A full commit SHA fixes the selected action version.
The allowlist limits the action publishers.
A new publisher requires a reviewed policy change.

[`policy/tool-pins.toml`](../policy/tool-pins.toml) records the reviewed CI and release tools.
`check-pinned-inputs` compares each workflow copy with that policy.
`scripts/update-upstream-pins.sh` updates the workflow and policy copies together.

## Validator identity

`ci.yml` and `docs.yml` run the validator as the caller UID and GID.
They use separate writable home, cache, and temporary roots.
The launcher creates an isolated home if the caller has no passwd entry.

The mirrored local jobs are under `scripts/ci/`.
Workflow YAML controls events, permissions, runners, and hosted-only steps.
The local scripts contain the shared job logic.

## Hosted controls

Some release controls are outside Git:

- branch and tag rulesets
- the `release` environment
- the `hosted-controls-audit` environment
- the `upstream-refresh` environment
- repository variables for attestation policy

`scripts/verify-github-hosted-controls.sh` compares those controls with
[`policy/github-hosted-controls.toml`](../policy/github-hosted-controls.toml).

The canonical repository requires these variable values:

- `WORKCELL_RELEASE_NO_ATTEST=false`
- `WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS=false`

The release environment permits protected `v*` tags only.
It has no secret or variable content and no administrator bypass.

## Public and private repositories

The `hosted-controls-audit` environment requires `WORKCELL_HOSTED_CONTROLS_TOKEN`.
This requirement applies to public and private repositories.
Private code scans and SARIF uploads depend on the GitHub plan.

The public repository creates GitHub attestations.
A private repository needs reviewed policy and plan support before it creates them.

## Deliberate omissions

Workcell does not use these workflow features:

- general-purpose `pull_request_target` automation
- ambient personal-access-token credentials
- a hosted claim for the strict Colima boundary
- unrelated stale-issue automation

`pr-base-policy.yml` is the only `pull_request_target` exception.
It uses trusted base-branch code and reads PR metadata only.
It does not check out repository content or use an external action.
