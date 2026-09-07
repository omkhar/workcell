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
| `ci.yml` | Runs repository validation, smoke tests, reproducibility, install checks, PR-shape checks, and advisory hostile-environment reruns. |
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
The `Release asset ACL (Darwin)` lane runs on every PR and `main` push.
It uses `macos-15` and checks the exact Go toolchain.

The `Hostile environment` lanes run repository validation again on one hostile axis each.
`WORKCELL_HOSTILE_ENV` selects the axis:

- `tmpdir` puts `TMPDIR` in a directory whose name has a space, a literal `$`, a `--` token, and 80 characters of padding.
- `workspace` copies the checkout to a bind source whose name has a space and a comma. The `--mount` record is CSV, so the comma must survive the encoder.
- `root` runs the container as UID 0.
- `uidmap` runs the container as a UID that owns none of the bind and has no record in the image.

These shapes reproduce quoting, argument-boundary, mount-record, and `sun_path` defects before review.
The lanes are advisory: they use `continue-on-error` and are not required checks.
Run one axis on a host the same way the lane does, with Docker available:

```bash
export WORKCELL_VALIDATOR_IMAGE="workcell-validator:local"
./scripts/ci/build-validator-image.sh
WORKCELL_VALIDATE_REPO_PROFILE=repo-core \
  WORKCELL_HOSTILE_ENV=tmpdir \
  ./scripts/ci/run-validate-in-validator.sh
```

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

The release install matrix runs the same ACL script before it uses release artifacts.

## Release workflow

The preflight job records the expected digest for its source archive.
The release job independently creates and extracts its own archive from the checked-out release tag.
It then compares the archive digest with the expected digest.
It creates source-dependent manifests from the extracted tree.
It creates the formula from the verified archive digest.
The native amd64 and arm64 image jobs build from the checked-out release tag.
Each one binds its image digest to the matching preflight reproducibility digest.

A separate read-only job creates the nine non-image signing subjects.
That job creates the software bills of materials, the builder-environment manifest, and the checksums.
The assembly job creates one OCI layout and the bound handoff manifest.
It also rebuilds the release bundle, the Homebrew formula, the control-plane manifest, and the build-input manifest.
It requires the binding job to match those four byte for byte before it copies the nine subjects.
The architecture build jobs and assembly job have only `contents: read` permission.
They transfer bound artifacts with GitHub Actions artifact runtime credentials.
The signer downloads those subjects by immutable artifact ID and requires exact byte matches.
A release-approved signing job validates the handoff and publishes the OCI layout.
The signing job uses fixed tools and does not check out repository code.
The final job publishes the 18 signed GitHub release assets.

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

The `release` environment gates registry publication, signatures, and attestations.
No architecture build or assembly step can push packages or request an OIDC token.
The `hosted-controls-audit` environment gates release preflight and final GitHub release publication.

The release uses Cosign to create keyless Sigstore signatures.
It creates GitHub attestations after a fixed public-repository guard.
The guard reads the repository visibility from the GitHub event.
It stops the release if the repository is not public.
GitHub attestations do not replace Sigstore signatures.

The final publisher has `actions: read` and `contents: write` permissions.
It checks hosted controls immediately before publication.
It removes the administration token before it uses the default publication token.
This authority split does not make a SLSA Build L3 claim.

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
Each validator lane in `ci.yml` and `docs.yml` also mounts a synthesized `/etc/passwd` record for that UID, and `scripts/build-and-test.sh --docker` does the same.
`scripts/ci/lib/validator-passwd.sh` writes the record.
A UID with no record breaks each tool that resolves the invoking user, such as `ssh-keygen`.
The `Validate repository` step in `release.yml` runs its own container and does not mount that record yet.

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

The release workflow does not read those variables.
It pins the attestation decision in versioned source.
A variable change alone cannot make a release without attestations.
The hosted-control policy still audits both values.

The release environment permits protected `v*` tags only.
It has no secret or variable content and no administrator bypass.

## Public and private repositories

The `hosted-controls-audit` environment requires `WORKCELL_HOSTED_CONTROLS_TOKEN`.
This requirement applies to public and private repositories.
Private code scans and SARIF uploads depend on the GitHub plan.

The public repository creates GitHub attestations.
The canonical workflow stops for a private repository.
A private repository needs a reviewed workflow change, a policy change, and plan support.

## Deliberate omissions

Workcell does not use these workflow features:

- general-purpose `pull_request_target` automation
- ambient personal-access-token credentials
- a hosted claim for the strict Colima boundary
- unrelated stale-issue automation

`pr-base-policy.yml` is the only `pull_request_target` exception.
It uses trusted base-branch code and reads PR metadata only.
It does not check out repository content or use an external action.
