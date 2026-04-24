# Validation Scenarios

Workcell uses several validation layers. No single test proves the full
boundary or release story by itself.

Use this page with:

- [`policy/operator-contract.toml`](../policy/operator-contract.toml) for the
  normative operator workflow inventory
- [docs/use-case-matrix.md](use-case-matrix.md) for what is currently covered
- [docs/requirements-validation.md](requirements-validation.md) for the
  machine-checked requirement-to-evidence mapping
- [docs/scenario-gaps.md](scenario-gaps.md) for what is still missing

## Traceability anchors

[`tests/scenarios/manifest.json`](../tests/scenarios/manifest.json) is the
canonical scenario index.

Use these anchors when checking release-facing claims:

- auth and resolver posture:
  `shared/auth-commands`, `shared/auth-status`,
  `shared/claude-resolver-launcher`, `shared/codex-resolver-launcher`
- lower-assurance mode claims: `shared/assurance-dry-run`
- compat target selection and fail-closed diagnostics: `shared/compat-target-dry-run`
- preview AWS remote VM diagnostics and certification gate:
  `shared/aws-remote-vm-dry-run`, `shared/aws-ec2-ssm-launch-smoke`
- local runtime certification smoke: `shared/agent-launch-smoke`
- host publication handoff and main-only PR-base safeguards: `shared/publish-pr`
- host-side session inventory and control plus detached workspace-mode
  remediation: `shared/session-commands`
- persistent cache-plane contract checks: `shared/assurance-dry-run`
- canonical preview-only remote VM contract:
  `internal/remotevm/contract_test.go`,
  `internal/remotevm/fake_target_test.go`,
  `internal/remotevm/conformance_test.go`
- Claude hook coverage: `claude-swe/hook-parametric`
- supported GitHub-hosted macOS release window:
  `scripts/verify-github-macos-release-test-runners.sh`
- Gemini Vertex supplemental `gcloud_adc` and allowlist behavior:
  `scripts/verify-invariants.sh`

## Repo-required deterministic checks

These are the checks that must stay green for normal repo validation. They run
without provider credentials and must not depend on live Colima or cloud
state:

- `./scripts/dev-quick-check.sh`
- `./scripts/build-and-test.sh` (host-native by default; `--docker` reruns repo validation inside the pinned CI validator container from a disposable snapshot)
- `./scripts/container-smoke.sh`
- `./scripts/verify-invariants.sh`
- `./scripts/verify-release-bundle.sh`
- `./scripts/verify-reproducible-build.sh`
- `./scripts/pre-merge.sh` profiles:
  - `repo-core` for deterministic repo-required validation
  - `pr-parity` for the local mirror of required `main`-based PR checks
  - `release-preflight` for the additional mirrored release-facing lanes
- `./scripts/repo-publish-pr.sh` for `main`-based PR publication after fresh
  local `pr-parity` evidence exists

They cover repo shape, runtime contracts, smoke behavior, and reproducibility.
They also now cover canonical requirement traceability, host-side policy
inspection and explainability, host-side detached session inventory, control,
logs/timeline, clean-base diff/export behavior, and operator-contract parity.
They also now carry the canonical preview-only `remote_vm` contract through the deterministic `internal/remotevm` fake target and shared conformance harness, the explicit `docker-desktop` compat target through deterministic backend-selection, state-root-routing, and fail-closed diagnostics, and the preview-only `aws-ec2-ssm` remote VM target through deterministic broker-plan diagnostics and fail-closed live gating.

`./scripts/validate-repo.sh` runs the repo-required scenario tier through:

- `./scripts/run-scenario-tests.sh --repo-required`

That scenario runner executes serially by default so repo-required checks do
not race on shared host-side state. Set `WORKCELL_SCENARIO_JOBS` above `1`
only for explicit local debugging where you accept lower determinism.

## Local certification smoke

These checks are still valuable, but they are intentionally not part of the
repo-required validation path because they depend on a live runtime boundary.

- `./scripts/run-scenario-tests.sh --secretless-only --certification-only`

Today that certification tier includes:

- `shared/agent-launch-smoke` for local macOS Colima prepare-only and
  provider-version smoke on the managed path
- `shared/docker-desktop-launch-smoke` for the explicit `local_compat/docker-desktop/compat` path on healthy macOS Docker Desktop hosts
- `shared/aws-ec2-ssm-launch-smoke` for the credentialed
  `remote_vm/aws-ec2-ssm/compat` preview boundary against a reviewed
  SSM-managed EC2 target

AWS remote VM live smoke remains certification-only as well, but it is
currently a provider-e2e preview gate documented in
[`docs/aws-ec2-ssm-preview.md`](aws-ec2-ssm-preview.md) rather than a
repo-required scenario. Set `WORKCELL_AWS_EC2_SSM_TARGET_ID` and
`WORKCELL_AWS_EC2_SSM_REGION` before running that smoke.

Certification smoke is where local boundary proof belongs. It should stay
available and documented, but it must not be the reason repo validation fails
on a machine that lacks the live runtime prerequisites.

When a change introduces or materially changes a supported end-to-end
workflow, backend, support-tier claim, or certification-only validation path,
this certification tier becomes a pre-signing gate: complete the relevant live
smoke successfully before signing the commit that claims the new support.

## Documentation example coverage

Release-facing examples are expected to map to existing automated evidence even
when the repo does not add a dedicated new scenario for each page.

| Guide | Primary evidence |
|---|---|
| `README.md` install, launch, and session snippets | `scripts/verify-invariants.sh`, `scripts/container-smoke.sh`, `tests/scenarios/shared/test-session-commands.sh`, `cmd/workcell-hostutil/main_test.go` |
| `docs/getting-started.md` | `scripts/verify-invariants.sh`, `tests/scenarios/shared/test-auth-commands.sh`, `tests/scenarios/shared/test-auth-status.sh` |
| `docs/examples/quickstart-codex.md` | `tests/scenarios/shared/test-auth-commands.sh`, `tests/scenarios/shared/test-auth-status.sh`, `tests/scenarios/shared/test-codex-resolver-launcher.sh`, `tests/scenarios/shared/test-publish-pr-dry-run.sh` |
| `docs/examples/quickstart-claude.md` | `tests/scenarios/shared/test-auth-commands.sh`, `tests/scenarios/shared/test-auth-status.sh`, `tests/scenarios/shared/test-claude-resolver-launcher.sh`, `tests/scenarios/shared/test-publish-pr-dry-run.sh` |
| `docs/examples/quickstart-gemini.md` | `tests/scenarios/shared/test-auth-status.sh` for the staged `gemini_env` path and `tests/scenarios/shared/test-publish-pr-dry-run.sh` for the host-side publication steps; OAuth and `gcloud_adc` remain manual provider-e2e validation paths |
| `docs/provider-bootstrap-matrix.md` | `tests/scenarios/shared/test-auth-commands.sh`, `tests/scenarios/shared/test-auth-status.sh`, `tests/scenarios/shared/test-policy-commands.sh`, `tests/scenarios/shared/test-codex-resolver-launcher.sh`, `tests/scenarios/shared/test-claude-resolver-launcher.sh` |
| `docs/examples/enterprise-claude-setup.md` | `tests/scenarios/shared/test-auth-commands.sh`, `tests/scenarios/shared/test-auth-status.sh`, `tests/scenarios/shared/test-policy-commands.sh`, `tests/scenarios/shared/test-publish-pr-dry-run.sh` |

## Manual authenticated smoke

`./scripts/provider-e2e.sh` is the reviewed path for provider-authenticated
checks. It is deliberately separate from default CI and from the repo-required
scenario tier so the default path stays deterministic and secretless.

Use it when you need to verify:

- real provider login reuse
- provider-specific auth selection
- injected MCP or project-registry behavior
- provider UX that only shows up with a live account

## GitHub CI vs local boundary proof

GitHub-hosted CI proves:

- repo validation and workflow hygiene
- smoke behavior in the reviewed runtime image
- reproducibility and release-preflight logic
- bundle install/uninstall and Homebrew install/uninstall on GitHub-hosted
  Apple Silicon `macos-26` and `macos-15`
- signing and attestation logic on tagged releases

GitHub-hosted CI does not prove the full macOS Colima boundary. That remains a
local exercise.

Workcell now also keeps a machine-checked local parity inventory in:

- [`policy/workflow-lane-policy.json`](../policy/workflow-lane-policy.json)
- [`policy/workflow-lanes.json`](../policy/workflow-lanes.json)

Use `./scripts/ci-plan.sh` to see which mirrored lanes a given local
`pre-merge` profile will execute and which lanes remain GitHub-only.

## Credential placement rule

Provider credentials belong in the injection policy or the reviewed manual
provider-e2e path. They do not belong in the workspace, repo config, or
ambient host passthrough.

## Out of scope

These are not treated as equivalent to the default path:

- host-native GUI execution
- arbitrary container commands outside the explicit managed `development` or `--allow-arbitrary-command` paths
- `breakglass`
- whole-home or socket passthrough
