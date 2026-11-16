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

When the plan selects the validate lane, `pre-merge.sh` starts the live-Colima invariant lane first.
That lane runs `verify-invariants.sh --live-lane-only` in parallel with the other lanes.
It uses its own Colima VMs, so it does not share image tags with the other lanes.
The validate lane then runs `verify-invariants.sh --skip-live-lane`.

`pre-merge.sh` waits for the live lane at the end and fails if that lane fails.
On a failure or a stop signal, it stops the live lane and waits for its cleanup.
That cleanup deletes the Colima profiles of the lane.

It prints one line for each lane: `[pre-merge] lane=<name> seconds=<n> rc=<rc>`.
It prints `[pre-merge] total seconds=<n> rc=<rc>` when it exits.
The PR-parity evidence file records the same data in `timings`.

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
| `claude-mention.yml` | Answers an `@claude` mention by the repository owner. See [Claude mention](#claude-mention). |
| `ci.yml` | Runs repository validation, smoke tests, reproducibility, install checks, PR-shape checks, and required hostile-environment reruns. |
| `codeql.yml` | Scans the shipped Go, Rust, and JavaScript code. |
| `dependabot-follow-up.yml` | Makes the action inputs patch that a Dependabot bump needs. See [Dependabot follow-up](#dependabot-follow-up). |
| `docs.yml` | Checks spelling, links, contracts, and the man page. |
| `fuzz.yml` | Runs extended Go and Rust fuzz tests. |
| `hosted-controls.yml` | Checks GitHub controls that are outside Git. |
| `mutation.yml` | Runs the mutation-score gate on a schedule, manual run, or approved heavy PR. |
| `pin-hygiene.yml` | Checks reviewed upstream and tool pins. |
| `pr-base-policy.yml` | Requires `main` as the base for a ready PR. |
| `release.yml` | Builds, verifies, signs, and publishes a release. |
| `scorecard.yml` | Runs OpenSSF Scorecard analysis. |
| `security.yml` | Checks workflow policy, dependencies, and GitHub Actions security. |
| `upstream-refresh.yml` | Creates an upstream-pin candidate, updates the tracking issue, and opens the bump PR. |

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

The `root` and `uidmap` axes validate a copy of the workspace that the container makes as its own UID.
Git refuses a repository owned by another UID.
`scripts/ci-plan.sh` runs Git with no configuration that could grant an exception, so the lane aligns the ownership.

These shapes reproduce quoting, argument-boundary, mount-record, and `sun_path` defects before review.
The lanes are required merge gates: each axis is a required status check, so a red axis blocks the merge.
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

The tag-policy job resolves the signed tag to one commit before any checkout.
Every later job checks out that commit, so a moved tag cannot change the release source.

The preflight job records the expected digest for its source archive.
The release job independently creates and extracts its own archive from the checked-out release commit.
It then compares the archive digest with the expected digest.
It creates source-dependent manifests from the extracted tree.
It creates the formula from the verified archive digest.
The native amd64 and arm64 image jobs build from the checked-out release commit.
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
The signing job uses fixed tools and checks out only the verified release commit,
which it needs for the reviewed tag rechecks it runs before it signs and before it publishes.
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
If the command set changes, the updater holds that Codex release.

The workflow makes two candidates. Each candidate starts from the same `main` commit, and each one applies to `main` alone:

| Candidate | Updater | Contents | Result |
| --- | --- | --- | --- |
| `provider` | `update-provider-pins.sh --apply` | Provider pins only | Scope guard, then auto-merge after approval |
| `toolchain` | `update-upstream-pins.sh --apply --toolchain-only` | All other pins | Toolchain scope guard and re-derivation, then human review |

The `provider` candidate contains only these changes:

- Provider `ARG` lines and SHA-256 assignments in `runtime/container/Dockerfile`
- The Gemini CLI pin in `runtime/container/providers/package.json` and `package-lock.json`
- The Codex command inventory in `tests/fixtures/codex-subcommands.txt`
- The OpenCode and Amp CLI pins in `runtime/container/providers-uncertified/package.json` and `package-lock.json`.
  The scope guard does not allow these files, so a candidate that changes them does not auto-merge.
  Amp publishes a build about every four hours, so most refresh runs move its pin.
- The goose CLI pin in `runtime/container/Dockerfile`: the `ARG GOOSE_VERSION` line and the `GOOSE_SHA256` assignments.
  The scope guard does not allow these lines, so a candidate that changes them does not auto-merge.

The `toolchain` candidate contains the base images, the Debian snapshot, the Go and Rust toolchains, and the CI and release tools.
Both candidates can change `runtime/container/Dockerfile`, but they do not change the same lines.

| Job | Environment | Token | Purpose |
| --- | --- | --- | --- |
| `refresh` | none | `contents: read`, `issues: write`, `pull-requests: read` | Builds the candidates and updates the tracking issue. |
| `scope-guard` | none | `contents: read` | Runs `scripts/ci/upstream-refresh-scope-guard.sh` on the `provider` patch, and with `--toolchain` on the `toolchain` patch, each as a non-blocking step: its result decides whether the PR may auto-merge, and its failure does not fail the job. |
| `rederive` | none | `contents: read` | Runs `scripts/ci/upstream-refresh-rederive.sh` on the `toolchain` candidate as a non-blocking step: its failure holds the toolchain candidate and does not fail the job. |
| `publish` | `upstream-refresh` | GitHub App token | Opens the PRs with `scripts/ci/upstream-refresh-publish.sh`. |
| `report` | none | none | Fails the run when a kind failed. |

`scripts/ci/upstream-refresh-candidate.sh` writes each candidate and then resets the tree to `HEAD`.
An open refresh PR of one kind stops a new candidate of that kind only.

Each kind refreshes in its own step of `.github/workflows/upstream-refresh.yml`.
If one kind fails, the job removes the candidate of that kind and keeps the other candidate.
If a kind fails, the `report` job of `.github/workflows/upstream-refresh.yml` fails the run.
The `publish` job does not need the `report` job, so the other candidate still opens its PR.
The tracking issue and an error annotation name the kind that failed.

The updaters retry a failed upstream request at most three times, with a backoff of 2 to 30 seconds.
They retry a transport error, a 429 status, and a 500, 502, 503, or 504 status.
They do not retry a TLS verification failure, a refused redirect, or a spent GitHub API quota.
`internal/metadatautil/upstream_retry.go` reads a response body of at most 256 MiB before it returns the response, so a body that breaks partway is retried too.

If `snapshot.debian.org` is not available, `scripts/update-upstream-pins.sh` holds the current Debian snapshot pin.
The job output and the tracking issue show the hold.
The updater holds the pin only while the pin is at most 7 days old (`WORKCELL_DEBIAN_SNAPSHOT_HOLD_MAX_AGE_DAYS`). Then the run fails.
The updater does not move the snapshot pin to an older date.

The `--apply` run records its Debian snapshot plan in `WORKCELL_DEBIAN_PLAN_RECORD`, and the `--check` run of `scripts/update-upstream-pins.sh` reuses that plan.
So `snapshot.debian.org` can come back or fail between the two runs, and the check still sees the same plan.

The `refresh` job uploads one artifact, `upstream-refresh-candidate`.
It contains a `provider` directory and a `toolchain` directory. Each directory contains `patch`, `diffstat`, and `metadata.json`. The `candidate` field in `metadata.json` names the kind.
The artifact also contains `provider-summary`, the provider bump plan.

The `publish` job publishes each candidate alone.
It applies the patch to `base_sha`. It stops if the tree differs from `tree_oid`. It stops on a symlink, a submodule, a mode change, or a new executable file.
It commits with the GraphQL `createCommitOnBranch` mutation, so GitHub signs the commit.
It checks the commit tree and the signature, and then opens the PR on branch `codex/upstream-refresh-<kind>-<run id>`.

The PR body takes its diffstat from the applied tree and its run link from the workflow context, not from the artifact.

It skips a candidate that is stale against `main`. It skips a candidate when a refresh PR of the same kind from another run is open.
If an earlier attempt of the same run opened the PR, `publish` resumes that PR. It checks the PR head commit and then applies the merge or label step.
If a run stops after it opens the PR, rerun that run or close the PR.
The job output `result` is `passed` only when the guard step exits 0.

The App has no Workflows permission, and it must not get one.
`publish` does not publish a candidate that changes `.github/workflows/`.
It publishes the `toolchain` candidate only when the toolchain guard and the re-derivation both passed.

The job output `toolchain` is the toolchain guard verdict `passed:<base commit>:<tree>` when the toolchain guard step exits 0.
Otherwise it is `failed`.
`publish` stops if the verdict does not name the `base_sha` and `tree_oid` of the candidate.
So the tree check binds the commit to the tree that the guard checked.

The toolchain guard checks the shape of each changed line, not its value.
The `rederive` job checks the values.
It runs on its own runner, apart from the `refresh` job, with a `contents: read` token.
`scripts/ci/upstream-refresh-rederive.sh` runs `update-upstream-pins.sh --apply --toolchain-only` again from a clean checkout of the base commit.
Then it runs the `--check` mode and `scripts/check-pinned-inputs.sh`.
The job installs no tools, because the toolchain updater runs no `cosign`.

The updater looks the Debian snapshot up itself, as in the `refresh` job, so the candidate does not choose it.
`scripts/ci/upstream-refresh-rederive.sh` requires that snapshot to equal the snapshot of the candidate.
A run that crosses midnight UTC can find a newer snapshot, and then the candidate waits for the host command.

The `rederive` job output `toolchain` is `rederived:<base commit>:<tree>` when the tree it gets equals `tree_oid`.
Otherwise it is `failed`.
`publish` stops if that verdict does not name the `base_sha` and `tree_oid` of the candidate.
So a second run of the updater, in a job that cannot write, got each version, digest, and checksum in the PR.
If upstream moved between the two runs, the trees differ, and the candidate waits for the host command.

The guard and rederive scripts remove the runner command files, such as `GITHUB_OUTPUT`, from the environment of their child processes.
That is hygiene, not a boundary, because a child can still read the path from the environment or command line of its parent in `/proc`.

The verdict holds for other reasons.
The job output takes the verdict only when the step outcome is `success`.
The guard exits 0 only when the tree it checked passes, and its verdict names that tree.
The rederive script exits 0 only when the tree it rebuilt equals `tree_oid`.
And `publish` acts only on a verdict that names the `base_sha` and `tree_oid` of the candidate.

`internal/metadatautil/workflows.go` pins each step of the `scope-guard` and `rederive` jobs, and the workflow default shell.
The `rederive` step times out at 15 minutes, before its job at 20.
So a slow re-derivation fails the step and holds the toolchain candidate.
GitHub may report a job that timed out as cancelled, which would stop `publish`.

In `.github/workflows/upstream-refresh.yml`, `publish` still runs when the `rederive` job fails, and then it holds only the `toolchain` candidate.
It does not run when the `refresh` or `scope-guard` job failed, or when the run was cancelled.

If the toolchain guard or the re-derivation did not pass, `scripts/ci/upstream-refresh-publish.sh` stops before any GitHub call for that kind.
The tracking issue and the audit comment then show the host command for that candidate:

```bash
./scripts/publish-upstream-refresh-pr.sh --run-id <run id> --candidate toolchain
```

The command recreates the candidate on the host and compares the patch digest.
It reuses the Debian snapshot from the candidate patch and the start time of the run.
Thus a publication on a later UTC day still recreates the same patch.
Other upstream changes after the run, for example a new image digest, still cause a mismatch.
Then start a new run and publish that run.

This command gives the refresh PR the full PR shape budget on first publication (`--shape-margin 1.0` and `--full-shape-budget`). The candidate identity checks fix its content, so no review fix can grow it.

`publish` also fails closed if GitHub drops the mode of a changed executable file, because the tree check then fails. Publish such a candidate on the host.

The provider scope guard allows only these changes:

- Provider version `ARG` lines and SHA-256 assignments in `runtime/container/Dockerfile`
- The pinned Gemini CLI version line in `runtime/container/providers/package.json`
- The Gemini CLI entry in `runtime/container/providers/package-lock.json`
- The version stamp and source tag in the header of `tests/fixtures/codex-subcommands.txt`

A lockfile change to any other package, for example a new transitive dependency, fails the guard.

If the guard passes, `publish` runs `gh pr merge --auto --merge --match-head-commit` on the signed `provider` commit.
Every provider waits for `cooloff_hours = 48` in [`policy/provider-bumps.toml`](../policy/provider-bumps.toml) before a bump is eligible.
The required checks and a person's approval still gate the merge.

The `refresh` job creates the `needs-human-review` label, because the App token cannot create labels.
If the guard fails, `publish` adds the `needs-human-review` label and does not enable auto-merge.
A `toolchain` PR always gets the `needs-human-review` label, and `publish` never enables auto-merge on it.
Its PR body and the audit comment list each pin line it changes, the old line and then the new line.

The toolchain scope guard applies the patch to the base commit in a private index.
Then it compares each changed file of the new tree with the base tree.
It allows only an edit in place of a regular file, with the same mode and the same number of lines.
Each changed line must stay a pin line of the same key.
That key must appear once in the file.
Some pin lines change only with another pin line of the same file:

- A Go, Hadolint, `rustup-init`, `actionlint`, or `zizmor` SHA-256 line changes only with its version line.
- An OpenSSL or CA certificate package path changes only with `DEBIAN_SNAPSHOT`.
- An OpenSSL or CA certificate SHA-256 line changes only with its package path.

It allows only these files and pin lines:

- `go.mod`: the `go` and `toolchain` lines
- `policy/tool-pins.toml`: `cosign`, `buildx`, `syft`, `buildkit`, `qemu`, and the `actionlint` and `zizmor` version and SHA-256 lines
- `runtime/container/Dockerfile`: `NODE_BASE_IMAGE`, `RUST_TOOLCHAIN_IMAGE`, `GO_VERSION`, the two Go SHA-256 lines, and `RUST_VERSION`
- `tools/validator/Dockerfile`: `VALIDATOR_BASE_IMAGE`, the Go and Rust lines of the runtime Dockerfile, and the Hadolint, `deadcode`, and `rustup` pins
- `runtime/container/debian-bootstrap.env`: the snapshot, and the path and SHA-256 lines of the OpenSSL and CA certificate packages
- `runtime/container/rust/Cargo.toml`: `rust-version`
- `runtime/container/rust/rust-toolchain.toml`: `channel`
- `scripts/ci/build-validator-image.sh`: the default `BUILDKIT_IMAGE`
- `scripts/check-dead-code.sh`: `DEADCODE_VERSION`

An image pin keeps its repository and its tag, and only its digest changes.
The Rust and QEMU images are the exception: their tag carries the version.
A version or a snapshot does not move back.
Any other change fails the guard, for example a `.github/workflows/` change, a new file, or an edit to a `RUN` line.
A release that moves a version back, for example a backport, fails the guard, so a person publishes it from a host.

The guard cannot tell a real digest or checksum from a false one.
So a change of the digest alone, and any other value, publishes only when the re-derivation got the same value.

`publish` posts an audit comment on the tracking issue.
The comment lists each PR, its commit, the merge decision, the cool-off policy, and the bumped versions.

### Accepted risk: writer push after auto-merge

A repository writer can push to the bump branch after `publish` arms auto-merge.
A `pull_request` job cannot stop a writer who can rewrite that job.
The base-trusted fix is `pull_request_target`.
That fix would widen a repository security contract.

The repository has one writer, so the owner accepted this risk.
The `--match-head-commit` flag checks the head only when `publish` arms auto-merge.
The flag does not pin the head after that point.
Revisit this risk when a second maintainer gets write access.

### Upstream refresh administrator steps

`publish` skips with a notice until an administrator does these steps:

1. Create a GitHub App with `Contents: write` and `Pull requests: write`.
   Install it on this repository only.
2. Add two secrets to the `upstream-refresh` environment:
   `WORKCELL_UPSTREAM_REFRESH_APP_CLIENT_ID` and `WORKCELL_UPSTREAM_REFRESH_APP_PRIVATE_KEY`.
3. Set `upstream_refresh_app_id` under `[branch_review]` in `policy/github-hosted-controls.toml` to the App ID.

The App is not on the review bypass list of the `main` ruleset.
So a `provider` PR with auto-merge armed merges only after a person approves it.
The hosted-controls audit permits these two secrets and no others in this environment.
On the review ruleset, the audit allows at most one bypass actor, and only this App.

An operator can still use `./scripts/publish-upstream-refresh-pr.sh --candidate <kind>` for host publication.
The default kind is `toolchain`.

The candidate artifact and issue are operator signals.
They are not integrity evidence.
The `publish` job proves candidate identity again before it writes.

The scope guard accepts a lockfile change only when it edits two places.
The first place is the `version`, `resolved`, and `integrity` lines of the `node_modules/@google/gemini-cli` entry.
The second place is the root `@google/gemini-cli` pin.
The `resolved` URL must be on `registry.npmjs.org` and must name the same version.
Any other lockfile change is out of scope, including an added or removed package.

## Claude mention

`claude-mention.yml` runs `anthropics/claude-code-action` when the repository owner writes `@claude`.
It starts on a new issue or a new comment on an issue or a PR.
An edit does not start it.
It has no PR review trigger. GitHub runs a review event from the PR merge ref, not from `main`.

The job gate compares numeric account IDs, not logins.
The event author, the event sender, and the run actor must be account `22666538` (`omkhar`).
The first step looks up the account ID of the actor that started or reran the run.
Thus another account, a bot, or the reply of the Claude App cannot start the action.

On a PR, the owner must have opened the PR from this repository.
The gate checks the PR author, and a second step checks the author and head repository through the REST API.
The action reads the PR head, so fork or bot content must not reach it with the secret.

The action runs in answer-only mode.
Its tool deny list removes Bash, file edits, and web access.
It also denies reads of `/proc`, the runner temporary directory, Claude Code state, and `.git`.
`/tmp/github-images` stays readable, so Claude can see images in comments.
The `claude_args` input of `.github/workflows/claude-mention.yml` holds this list.

The job token is read-only. The action writes with the Claude GitHub App token that it gets through OIDC.

`internal/metadatautil/claude_mention.go` pins the parsed workflow to a reviewed digest.
The digest ignores comments and the action commit, so a pin bump keeps it.
Only `.github/workflows/claude-mention.yml` can name the action, its secret, or its environment, in any case.
Another workflow cannot choose its environment by an expression.
It also cannot read the secrets context by a computed index or as a whole, for example `toJSON(secrets)`.
`check-pinned-inputs` runs these checks on PRs to `main`.

These checks do not cover a branch that does not merge to `main`.
The environment rule keeps the secret on `main`, so set it as step 5 says.

Text from other people in the thread is still model input.
It can change the answer, but it cannot run a command or change a file.

### Mention failure output

The action hides Claude's output, because that output can hold what Claude read.
When Claude stops with an error, the step `Show why Claude failed` prints four fields of the final result.
They are the HTTP status, the subtype, the terminal reason, and the stop reason.
The step prints only numbers and short lowercase codes, never the text of the result.
A status of 401 means the API rejected `CLAUDE_CODE_OAUTH_TOKEN`. Replace the token as steps 5 and 6 below say.

### Claude mention administrator steps

The owner set up the workflow with these steps. Repeat them to reinstall it or to bump the action pin.

1. Run `gh api repos/anthropics/claude-code-action/commits/v1 --jq .sha`.
2. Put that SHA in the `uses:` line of `.github/workflows/claude-mention.yml`.
3. Add the row for that SHA to `tests/fixtures/actions/inputs.tsv`.
4. Install the Claude GitHub App on this repository only.
5. Create the `claude-mention` environment with no required reviewers. Set its deployment branches to `main` only.
6. Add the `CLAUDE_CODE_OAUTH_TOKEN` secret to that environment only.

`claude setup-token` creates the OAuth token.
`gh secret set CLAUDE_CODE_OAUTH_TOKEN --env claude-mention -R omkhar/workcell` sets it from a hidden prompt.
`check-workflow-refs` fails without the row of step 3.
`internal/metadatautil/workflow_refs.go` reads `tests/fixtures/actions/inputs.tsv` for that check.
The hosted-controls audit does not check the `claude-mention` environment.

## Action and tool pins

Use a full commit SHA for each GitHub Action reference.
List each publisher in [`policy/allowed-actions.toml`](../policy/allowed-actions.toml) before you use its action.

A full commit SHA fixes the selected action version.
The allowlist limits the action publishers.
A new publisher requires a reviewed policy change.

[`policy/tool-pins.toml`](../policy/tool-pins.toml) is the only copy of the reviewed CI and release tool pins.
No workflow holds a tool pin value.
Each job that uses a tool pin runs `scripts/ci/load-tool-pins.sh` as a step with `id: pins`.
That step runs right after the checkout, as the second step of the job, so no earlier step can change its environment.
The loader reads the policy file of the checked-out commit, and it stops on any line, key, or value that it does not expect.

A step binds each pin as `${{ steps.pins.outputs.<key> }}`, and only in its `with:` or `env:` keys.
A `run:` script reads a pin from its environment, never from an expression.
`check-pinned-inputs` runs these checks from `internal/metadatautil/pinnedinputs_toolpins.go` on every workflow:

- It rejects a tool pin value or a tool image name written inline in a workflow.
- It rejects a pins step that does not run exactly `scripts/ci/load-tool-pins.sh`, or that sets `if:` or `continue-on-error:`.
- It rejects `BASH_ENV`, `ENV`, `BASH_FUNC_` and `LD_` names, `PATH`, and `GITHUB_` or `RUNNER_` names in the workflow or job env of a pins job.
- It rejects a YAML merge key (`<<`) in any workflow, job, or step env, and an env given as a YAML alias or an expression.
- It rejects a binding to an unknown key, and a pin variable bound to the wrong key.
- It rejects a step that binds a pin and runs with `always()`, `cancelled()`, `failure()`, or a negated `success()`.
- It requires the BuildKit image to name `moby/buildkit` and the QEMU image to name `tonistiigi/binfmt`, with no registry host, as `scripts/ci/load-tool-pins.sh` does.
- It requires the loader output to equal its own parse of the policy file.

`scripts/update-upstream-pins.sh` writes a tool pin bump to `policy/tool-pins.toml` only.
A tool pin bump does not change a file under `.github/workflows/`.

## Dependabot follow-up

A Dependabot bump of a GitHub Action changes a `uses:` pin.
The new pin has no row in `tests/fixtures/actions/inputs.tsv`, so `check-workflow-refs` fails with `uses-not-cached` (`internal/metadatautil/workflow_refs.go`).

`dependabot-follow-up.yml` runs on each Dependabot PR to `main` that changes a workflow file.
It checks out the PR base commit and the bump commit in different directories.
It runs `workcell-citools` from the base checkout, which is reviewed `main`.
The bump checkout is only input: the job runs no script from it.
The job has `contents: read`, no secrets, and checkouts that keep no credential.
It pushes nothing.

The job runs three commands on the bump checkout:

- `refresh-action-inputs`, which fetches `action.yml` only for a pin with no row
- `refresh-action-inputs --verify`, which checks the new table against the base
- `fixture-patch`, which writes a patch of `tests/fixtures/actions/inputs.tsv` and no other file

`fixture-patch` fails if the table changed but the patch is empty (`internal/metadatautil/fixture_patch_test.go`).
If the patch is not empty, the job uploads it as the `dependabot-follow-up` artifact, which holds one file, `inputs.patch`.
The job then passes, and its summary says that a patch waits on the branch.
The job is not a required check, but `Validate repository` fails on the stale table until the patch is on the branch.
The job fails only on an error, such as a fetch failure or a table that `--verify` rejects (`.github/workflows/dependabot-follow-up.yml`).

`--verify BASE ROOT` passes only when each pin in the `ROOT` workflows has one row, in order.
A pin with a `BASE` row keeps it byte for byte; any other pin must be new.
Its row must match the `action.yml` at that commit, which `--verify` fetches again (`internal/metadatautil/action_inputs_refresh_test.go`).

### Daily Dependabot check

The daily Dependabot check applies the patch with no new credential.

Run these steps for each open Dependabot PR whose `Validate repository` check is red.
Every command runs from a clean checkout of `origin/main` at `MAIN`.
No command runs a script, a hook, or a build from the bump tree.

A relative `core.hooksPath`, such as `.githooks`, would run the hooks of the worktree.
So each `git` command that can run a hook sets `core.hooksPath=/dev/null`.
If a check prints an unexpected line or exits with an error, stop and report the PR to the maintainer.

Step 1: Read the PR.
The author must be `dependabot[bot]`.
The branch must start with `dependabot/github_actions/`.
Every commit author must be `dependabot[bot]`.

```bash
gh api "repos/omkhar/workcell/pulls/${PR}" --jq '.user.login, .head.ref, .head.sha, .head.repo.full_name'
gh api "repos/omkhar/workcell/pulls/${PR}/commits" --jq '[.[].author.login] | unique | join(",")'
```

Set `BRANCH` and `HEAD_SHA` from the output; the repository must be `omkhar/workcell`.

Step 2: Check that the bump changes only `uses:` pins in workflow files.
Each of the last three commands must print nothing.

```bash
git -C "${MAIN}" fetch origin main "refs/heads/${BRANCH}"
BASE_SHA="$(git -C "${MAIN}" merge-base origin/main "${HEAD_SHA}")"
git -C "${MAIN}" diff --no-renames --summary "${BASE_SHA}" "${HEAD_SHA}"
git -C "${MAIN}" diff --no-renames --name-only "${BASE_SHA}" "${HEAD_SHA}" | grep -Ev '^\.github/workflows/[^/]+\.ya?ml$'
git -C "${MAIN}" diff --no-renames -U0 "${BASE_SHA}" "${HEAD_SHA}" | grep -E '^[-+]' | grep -Ev '^(\+\+\+|---) ' | grep -Ev '^[-+] *(- )?uses: [^ ]+@[0-9a-f]{40}( # [^ ]+)?$'
```

Step 3: Find the follow-up run for that exact commit, and download its artifact into an empty directory.
If `RUN_ID` is empty, the run for that commit has not passed yet, so try again at the next check.
If the run has no `dependabot-follow-up` artifact, the bump needs no patch.
The `find` command must print one line, `f inputs.patch`.

```bash
RUN_ID="$(gh run list --repo omkhar/workcell --workflow dependabot-follow-up.yml --event pull_request --commit "${HEAD_SHA}" --status success --json databaseId,headSha --jq "map(select(.headSha == \"${HEAD_SHA}\"))[0].databaseId")"
DL="$(mktemp -d)"
gh run download "${RUN_ID}" --repo omkhar/workcell --name dependabot-follow-up --dir "${DL}"
find "${DL}" -mindepth 1 -printf '%y %P\n'
```

Step 4: Apply the patch in new worktrees of the bump and of its base.
`--numstat` must print one line: two numbers and `tests/fixtures/actions/inputs.tsv`.
`--summary` must print nothing.
`status` must print one line, `M  tests/fixtures/actions/inputs.tsv`.

```bash
WT="$(mktemp -d)"
git -C "${MAIN}" -c core.hooksPath=/dev/null worktree add --detach "${WT}/bump" "${HEAD_SHA}"
git -C "${MAIN}" -c core.hooksPath=/dev/null worktree add --detach "${WT}/base" "${BASE_SHA}"
git -C "${WT}/bump" apply --numstat "${DL}/inputs.patch"
git -C "${WT}/bump" apply --summary "${DL}/inputs.patch"
git -C "${WT}/bump" apply --index "${DL}/inputs.patch"
git -C "${WT}/bump" status --porcelain
```

Step 5: Verify the rows with the tools from `MAIN`.
It must exit with status 0.

```bash
(cd "${MAIN}" && go run ./cmd/workcell-citools refresh-action-inputs --verify "${WT}/base" "${WT}/bump")
```

Step 6: Sign the commit and push it to the bump branch.
Do not force the push. If the branch moved since step 1, Git turns the push down.

```bash
git -C "${WT}/bump" -c core.hooksPath=/dev/null commit --no-verify -S \
  -m ".f Add action input rows for the pins that Dependabot PR #${PR} bumps (the new pins had no rows, so check-workflow-refs failed with uses-not-cached; refresh-action-inputs --verify passes against ${BASE_SHA})" \
  -m "Co-Authored-By: Claude <noreply@anthropic.com>"
git -C "${WT}/bump" -c core.hooksPath=/dev/null push --no-verify origin "HEAD:refs/heads/${BRANCH}"
git -C "${MAIN}" worktree remove --force "${WT}/bump"
git -C "${MAIN}" worktree remove --force "${WT}/base"
```

The push starts CI again, and the next follow-up run and `Validate repository` pass.
After a commit that Dependabot did not write, Dependabot stops rebasing the branch.
To rebase it, comment `@dependabot recreate`, and the check applies the next patch.

The job does not push: a `GITHUB_TOKEN` push starts no CI, and an App token needs a ruleset bypass.

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

The release environment permits the `main` branch only.
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
