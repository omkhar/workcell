# Validation scenarios

Workcell uses more than one validation layer.
No single test proves the complete runtime boundary or release process.

Use these files as the sources of truth:

- [`policy/operator-contract.toml`](../policy/operator-contract.toml) defines the supported operator workflows.
- [`policy/requirements.toml`](../policy/requirements.toml) maps requirements to evidence.
- [`tests/scenarios/manifest.json`](../tests/scenarios/manifest.json) lists the scenarios.
- [Use-case matrix](use-case-matrix.md) shows the tested use cases.
- [Scenario gaps](scenario-gaps.md) lists known evidence gaps.

## Validation layers

The main local commands have different purposes.

| Command | Purpose |
| --- | --- |
| `./scripts/dev-quick-check.sh` | Run the fast format, lint, contract, and unit checks. |
| `./scripts/build-and-test.sh` | Run host-native tests. Use `--docker` for the pinned validator image. |
| `./scripts/container-smoke.sh` | Build the runtime image and test the container boundary. |
| `./scripts/verify-invariants.sh` | Test launcher, policy, profile, and host-boundary invariants. |
| `./scripts/verify-reproducible-build.sh` | Compare two runtime builds. |
| `./scripts/validate-repo.sh` | Run the selected repository validation profile. |
| `./scripts/pre-merge.sh` | Plan and run a local validation profile. |
| `./scripts/verify-release-bundle.sh` | Rebuild and compare the release bundle during release preflight. |

`pre-merge.sh` has these profiles:

- `repo-core` runs deterministic repository validation.
- `pr-parity` mirrors the required checks for a PR that targets `main`.
- `release-preflight` adds the release checks.

The full release-bundle comparison runs only in `release-preflight` and the
`Release` workflow.
The invariant checks still inspect the release-bundle entrypoint in other profiles.

Use `./scripts/repo-publish-pr.sh` to publish a PR that targets `main`.
The wrapper requires fresh `pr-parity` evidence.
For an approved large adapter PR, use both approved adapter flags:

- Use `--label approved-large-certified-adapter` during local parity.
- Use `--approved-large-certified-adapter` during host publication.

## Scenario inventory

The manifest contains 22 scenarios:

- 17 secretless, repo-required scenarios
- two secretless certification scenarios
- three credentialed certification scenarios

The repo-required runner uses this command:

```bash
./scripts/run-scenario-tests.sh --repo-required
```

The runner uses one job by default.
This setting prevents races on shared host state.
Set `WORKCELL_SCENARIO_JOBS` above `1` only when lower determinism is acceptable.

### Main traceability anchors

Use these scenario identifiers for release claims:

- `shared/auth-commands` and `shared/auth-status` test authentication.
- `shared/codex-resolver-launcher` tests the Codex resolver.
- `shared/claude-resolver-launcher` tests the Claude resolver.
- `shared/policy-commands` tests policy commands.
- `shared/assurance-dry-run` tests assurance labels.
- `shared/compat-target-dry-run` tests Docker Desktop selection.
- `shared/aws-remote-vm-dry-run` tests AWS preview plans.
- `shared/gcp-remote-vm-dry-run` tests GCP preview plans.
- `shared/agent-launch-smoke` tests the local runtime launch.
- `shared/docker-desktop-launch-smoke` tests the Docker Desktop launch.
- `shared/publish-pr` tests PR publication.
- `shared/publish-github-release` tests the release entrypoint and signed-tag gate.
- `shared/home-control-plane-manifest` tests home control-plane files.
- `shared/session-commands` tests session operations.
- `shared/copilot-session-dry-run` tests the Copilot token handoff.
- `shared/shellproto-lib` tests shell protocol inputs.
- `shared/install-lifecycle` tests install, update, rollback, and isolated cleanup logic.
- `claude-swe/hook-parametric` tests the Claude command hook.

The remote VM contract also has deterministic Go tests:

- `internal/remotevm/contract_test.go`
- `internal/remotevm/fake_target_test.go`
- `internal/remotevm/conformance_test.go`

Requirements checks cover planned managed workstations, enterprise evidence,
and host expansion.
These checks prove traceability only.
They do not make these paths supported runtime targets.

## Canonical build environment

Four reviewed scripts source `scripts/lib/canonical-build-env.sh`:

- `scripts/dev-quick-check.sh`
- `scripts/validate-repo.sh`
- `scripts/verify-github-hosted-controls.sh`
- `scripts/check-public-contract.sh`

The direct-entrypoint mutation test covers the first three scripts.
The validation-entrypoint tests cover the public-contract script and its caller.

The helper rejects nonempty shell-visible ambient `GO*` and `CGO*` variables.
It permits only these Go values and storage paths:

- exact `GOENV=off`
- exact `GOWORK=off`
- empty `GOFLAGS`
- caller-selected `GOPATH`, `GOCACHE`, and `GOMODCACHE`

GitHub runners can set passive tool-cache aliases.
The helper accepts only `GOROOT_<major>_<minor>_{X64,ARM64}` names.
It removes each accepted alias before a child process starts.
It rejects a near-match.

The helper also rejects these ambient inputs:

- external compiler and package-tool selectors
- `NETRC` and `GCM_INTERACTIVE`
- nonempty `BASH_ENV` and `ENV`
- each retained `BASH_FUNC_*` entry
- noncanonical `GIT_*` overrides
- system and global Git configuration
- system and global Git attributes

The privileged shebang ignores Bash startup files and imported functions.
The entrypoints also clear `CDPATH` during root discovery.
Run each entrypoint directly.
This guarantee does not cover an arbitrary interpreter.

The gate does not scrub `SHELLOPTS`, `BASHOPTS`, or `BASH_XTRACEFD`.
It also does not scrub descendant `CDPATH`.
A child shell can consume this state.

This gate starts after the shell process starts.
It does not authenticate these inputs:

- an arbitrary direct `go` command
- the local Git administrative plane
- the worktree, index, refs, hooks, or object storage
- `.git/info/attributes`
- later `PATH` resolution or tool binaries
- caller-selected Go storage paths and their contents
- network policy or credential files under `HOME`, including `.netrc`
- general process-network variables

The three caller-selected Go storage paths are explicitly lower assurance.
The gate does not prove tool identity or build-input identity.
Release preflight uses separate checks for those properties.

## Local certification

Local certification uses a live runtime boundary.
It is not part of the repo-required lane.

Run the secretless certification scenarios with:

```bash
./scripts/run-scenario-tests.sh --secretless-only --certification-only
```

This tier contains:

- `shared/agent-launch-smoke` for `macos/arm64/local_vm/colima/strict`
- `shared/docker-desktop-launch-smoke` for `macos/arm64/local_compat/docker-desktop/compat`

The Docker Desktop path has lower assurance.
Its test requires a healthy Apple Silicon macOS Docker Desktop host.
It also requires Docker seccomp support.
It does not prove AppArmor or SELinux parity with Colima.

Run credentialed certification with:

```bash
./scripts/run-scenario-tests.sh --all --certification-only
```

This command runs all five certification scenarios.
Three scenarios require credentials:

- `shared/aws-ec2-ssm-launch-smoke`
- `shared/gcp-vm-launch-smoke`
- `shared/copilot-provider-e2e`

The AWS test requires a reviewed SSM-managed EC2 target.
Set `WORKCELL_AWS_EC2_SSM_TARGET_ID` and `WORKCELL_AWS_EC2_SSM_REGION`.

The GCP test requires a reviewed IAP-reachable Compute Engine target.
Set `WORKCELL_GCP_VM_TARGET_ID`, `WORKCELL_GCP_VM_ZONE`, and
`WORKCELL_GCP_VM_PROJECT`.

The AWS and GCP targets remain preview-only and launch-blocked.
Their certification tests do not promote them to supported targets.

The Copilot test stages `copilot_github_token`.
Set `WORKCELL_E2E_COPILOT_GITHUB_TOKEN` for this test.
It starts a managed development shell.
It then runs a non-destructive authenticated `copilot -p` request.

Complete the applicable live test before you sign a commit that changes a support claim.
This rule applies to a new or materially changed end-to-end workflow or backend.
It also applies to a materially changed certification-only validation path.

Antigravity remains unsupported and fails closed.
Do not claim support before deterministic evidence and live certification exist.

## Manual authenticated tests

Use `./scripts/provider-e2e.sh` for authenticated provider tests.
This path is separate from deterministic CI.

Use it to test:

- provider login reuse
- provider-specific authentication selection
- injected MCP state
- project-registry behavior
- provider behavior that requires a live account

Do not put provider credentials in the workspace or repository configuration.
Use the injection policy or the reviewed provider test path.

## Documentation evidence

`policy/operator-contract.toml` maps each supported workflow to its documents and evidence.
`./scripts/verify-operator-contract.sh` checks those mappings.
[Requirements validation](requirements-validation.md) describes the requirement checks.

Live Copilot certification remains outside the repo-required lane.
Gemini OAuth, `gemini_projects`, and `gcloud_adc` remain manual provider tests.
Antigravity has no quickstart because Workcell does not support it.

## GitHub and local proof

GitHub-hosted CI proves these properties:

- repository validation and workflow hygiene
- runtime-image smoke behavior
- reproducible builds and release-preflight logic
- bundle installation and launcher-link removal on `macos-26` and `macos-15`
- Homebrew installation and removal on `macos-26` and `macos-15`
- signing and attestation logic for a release tag

GitHub-hosted CI does not prove the strict macOS Colima boundary.
Use local certification for that proof.

These files define local and hosted lane parity:

- [`policy/workflow-lane-policy.json`](../policy/workflow-lane-policy.json)
- [`policy/workflow-lanes.json`](../policy/workflow-lanes.json)

Use `./scripts/ci-plan.sh` to show the selected local and hosted lanes.

### Changed-file planning limits

`ci-plan.sh` does not fetch.
It uses resident `origin/<base>` state when that state exists.
It uses a local base only after two absence checks.
It requires one resident merge base.

The planner requires a root `.git` directory.
It rejects common-directory redirection and shallow graphs.
The planner rejects nonregular or split-index state.
It also rejects hidden flags, conversion filters, present gitlinks, and unsafe ancestry.

The planner creates a separate zero-stat index.
It does not change the real index.
It hashes each present stage-0 regular file with conversions disabled.
It keeps valid UTF-8 paths in NUL-framed data until JSON encoding.
It rejects other path bytes.

The planner honors only the `HEAD` version of `.gitignore`.
It rejects worktree, index, and untracked case variants of that file.
It keeps tracked, staged, deleted, and normal untracked paths visible.

This guarantee assumes that repository state does not change during planning.
The planner is not fully hermetic.
Explicit paths bypass automatic changed-file discovery.
`pr-parity` rechecks the tree and status before it writes evidence.
It does not lock Git administrative state.

## Out of scope

Do not treat these paths as equal to the default path:

- host-native GUI execution
- arbitrary container commands outside the managed development path
- `breakglass`
- whole-home mounts
- host socket or credential-state passthrough
