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
- preview GCP remote VM diagnostics and certification gate:
  `shared/gcp-remote-vm-dry-run`, `shared/gcp-vm-launch-smoke`
- local runtime certification smoke: `shared/agent-launch-smoke`
- host publication handoff and main-only PR-base safeguards: `shared/publish-pr`
- host-side session inventory and control plus detached workspace-mode
  remediation: `shared/session-commands`
- persistent cache-plane contract checks: `shared/assurance-dry-run`
- canonical preview-only remote VM contract:
  `internal/remotevm/contract_test.go`,
  `internal/remotevm/fake_target_test.go`,
  `internal/remotevm/conformance_test.go`
- roadmap gate and evidence-map traceability for managed workstations,
  enterprise evidence, and host expansion:
  `scripts/verify-requirements-coverage.sh`,
  `scripts/verify-operator-contract.sh`
- planned provider-parity roadmap traceability:
  `ROADMAP.md`, `docs/provider-matrix.md`,
  `docs/provider-bootstrap-matrix.md`
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
- `./scripts/verify-release-bundle.sh` (release-preflight only: it runs in
  the `release-preflight` validate profile and in `release.yml`, not in the
  standard PR lane)
- `./scripts/verify-reproducible-build.sh`
- `./scripts/pre-merge.sh` profiles:
  - `repo-core` for deterministic repo-required validation
  - `pr-parity` for the local mirror of required `main`-based PR checks
  - `release-preflight` for the additional mirrored release-facing lanes
- `./scripts/repo-publish-pr.sh` for `main`-based PR publication after fresh
  local `pr-parity` evidence exists
  - reviewed, live-certified adapter support PRs that cannot be split use
    `--label approved-large-certified-adapter` for local parity and
    `--approved-large-certified-adapter` during host publication

They cover repo shape, runtime contracts, smoke behavior, and reproducibility.
The G0a1a1 canonical-build-environment unit covers `validate-repo`,
`dev-quick-check`, and the hosted-controls verifier. These three roots
explicitly source `scripts/lib/canonical-build-env.sh`.

The gate rejects every nonempty ambient shell-identifier `GO*` and `CGO*`
variable except exact `GOENV=off`, exact `GOWORK=off`, empty `GOFLAGS`, and
caller-selected `GOPATH`, `GOCACHE`, and `GOMODCACHE` storage paths. Passive
hosted-runner tool-cache aliases matching exact
`GOROOT_<major>_<minor>_{X64,ARM64}` grammar are not Go tool inputs and are
removed before any descendant runs; near-matches still fail closed. The gate also
rejects ambient external compiler/tool selectors, `NETRC`, `GCM_INTERACTIVE`,
nonempty `BASH_ENV` or `ENV`, every retained `BASH_FUNC_*` entry,
noncanonical shell-identifier `GIT_*` overrides, and system/global Git config
and attributes. Invalid identifiers exposed by the shell fail generically
before indirect expansion; raw invalid names hidden by the shell and not
recognized as tool variables are outside this unit. Set-empty `BASH_ENV` and
`ENV` are removed so ordinary Bash descendants cannot consume them. The three
allowed storage paths and their contents are explicitly lower assurance; exact
tool and build-input identity remains a separate certification dependency.
Production/default graph checks remain untagged.

G0a1a1 does not yet claim complete CI/release workflow-root closure. G0a1a2
must close canonical-state propagation through trusted-entrypoint children,
including forged-sentinel behavior. G0a1b must close the shared validate job's
containerized archive boundary and early token lifecycle. G0a1c must close
`ci-plan` and its direct `pre-merge` caller with resident-only base selection,
no implicit network or authentication, and fail-closed changed-file
collection. G0a2 must wire and test the remaining build/release helpers,
including `build-and-test`, pin checks and updates, smoke and manifest
generation, invariant/release-bundle/reproducibility checks, and
upstream-release and `verify-build-input-manifest` verification. After G0a2,
the direct `job-pr-shape`, `job-docs`, `job-mutation`, `job-fuzz`,
`check-workflows`, and `check-release-tag-signature` roots remain for G0b.
Release certification stays blocked until these dependencies land.

The gate begins only after the shell process starts. Its startup-state claim
therefore requires the reviewed privileged shebang, which ignores `BASH_ENV`,
`ENV`, and imported functions, and clears `CDPATH` during root discovery.
Launching a root through an arbitrary interpreter is outside this unit. The
gate also does not constrain arbitrary direct `go` invocations, authenticate
the local repository's Git administrative metadata, or make
candidate-controlled scripts trusted. The whole local administrative plane,
including repository config, refs, index and worktree state, hooks, object and
alternates storage, replace/graft/shallow data, and `.git/info/attributes`,
remains an immutable-tree, controller, and scan dependency. It also does not
authenticate later `PATH` resolution, tool binaries, or the three allowed
storage roots and their module/build-cache contents. Ambient Go selector
variables for module proxy, checksum, and auth behavior are rejected, but
G0a1a does not authenticate the resulting network policy or credential files
selected through `HOME` (including `.netrc`). General process networking
variables and the contents reachable through the three allowed storage paths
also remain outside this unit. Beyond the direct startup-code surfaces above,
shell semantic and tracing state such as `SHELLOPTS`, `BASHOPTS`,
`BASH_XTRACEFD`, and descendant `CDPATH` remain a G0a2a hygiene dependency.

They also now cover canonical requirement traceability, host-side policy
inspection and explainability, host-side detached session inventory, control,
logs/timeline, clean-base diff/export behavior, and operator-contract parity.
They also now carry the canonical preview-only `remote_vm` contract through the
deterministic `internal/remotevm` fake target and shared conformance harness,
the explicit `docker-desktop` compat target through deterministic
backend-selection, state-root-routing, and fail-closed diagnostics, and the
preview-only `aws-ec2-ssm` and `gcp-vm` remote VM targets through deterministic
broker-plan diagnostics and fail-closed live gating. Planning gates for managed
workstations, enterprise evidence, and host expansion are covered as
requirements and documentation traceability, not as runtime support scenarios.

`./scripts/validate-repo.sh` runs the repo-required scenario tier through:

- `./scripts/run-scenario-tests.sh --repo-required`

That scenario runner executes serially by default so repo-required checks do
not race on shared host-side state. Set `WORKCELL_SCENARIO_JOBS` above `1`
only for explicit local debugging where you accept lower determinism.

## Local certification smoke

These checks are still valuable, but they are intentionally not part of the
repo-required validation path because they depend on a live runtime boundary.

- `./scripts/run-scenario-tests.sh --secretless-only --certification-only`

Today that secretless certification tier includes:

- `shared/agent-launch-smoke` for local macOS Colima prepare-only and
  provider-version smoke on the managed path
- `shared/docker-desktop-launch-smoke` for the explicit
  `local_compat/docker-desktop/compat` path on healthy macOS Docker Desktop
  hosts with Docker seccomp available; this is lower assurance than the strict
  Colima path and does not assert AppArmor/SELinux daemon parity

Credentialed certification smoke is in the `provider-e2e` lane and requires
`--all` plus the relevant live credentials:

- `./scripts/run-scenario-tests.sh --all --certification-only`

That credentialed tier includes:

- `shared/aws-ec2-ssm-launch-smoke` for the credentialed
  `remote_vm/aws-ec2-ssm/compat` preview boundary against a reviewed
  SSM-managed EC2 target
- `shared/gcp-vm-launch-smoke` for the credentialed
  `remote_vm/gcp-vm/compat` preview boundary against a reviewed
  IAP-reachable Compute Engine target without an external NAT IP
- `shared/copilot-provider-e2e` for the credentialed Copilot CLI adapter path:
  explicit `copilot_github_token` staging, managed development shell, and a
  non-destructive authenticated `copilot -p` probe

Copilot CLI has deterministic adapter, auth, bootstrap, policy, and smoke
coverage in the repo-required lane. Live provider-authenticated certification
of a non-destructive `copilot -p` launch with staged credentials remains a
maintainer pre-signing gate for changes that promote or materially alter the
Copilot support claim, and stays separate from repo-required validation because
it needs live provider credentials. Antigravity remains planned/fail-closed and
must add the same deterministic and live evidence before claiming support.

Remote VM live smoke remains certification-only as well, but it is currently a
provider-e2e preview gate documented in
[`docs/aws-ec2-ssm-preview.md`](aws-ec2-ssm-preview.md) and
[`docs/gcp-vm-preview.md`](gcp-vm-preview.md) rather than a repo-required
scenario. Set `WORKCELL_AWS_EC2_SSM_TARGET_ID` and
`WORKCELL_AWS_EC2_SSM_REGION` before running the AWS smoke. Set
`WORKCELL_GCP_VM_TARGET_ID`, `WORKCELL_GCP_VM_ZONE`, and
`WORKCELL_GCP_VM_PROJECT` before running the GCP smoke.

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
| `docs/examples/quickstart-copilot.md` | `tests/scenarios/shared/test-agent-launch-smoke.sh`, `tests/scenarios/shared/test-auth-commands.sh`, `tests/scenarios/shared/test-auth-status.sh`, `tests/scenarios/shared/test-policy-commands.sh`, `scripts/container-smoke.sh`; live `scripts/provider-e2e.sh --agent copilot` remains certification-only |
| `docs/examples/quickstart-gemini.md` | `tests/scenarios/shared/test-auth-status.sh` for the staged `gemini_env` path and `tests/scenarios/shared/test-publish-pr-dry-run.sh` for the host-side publication steps; OAuth plus supplemental `gemini_projects` and `gcloud_adc` remain manual provider-e2e validation paths |
| `docs/provider-bootstrap-matrix.md` | `tests/scenarios/shared/test-auth-commands.sh`, `tests/scenarios/shared/test-auth-status.sh`, `tests/scenarios/shared/test-policy-commands.sh`, `tests/scenarios/shared/test-codex-resolver-launcher.sh`, `tests/scenarios/shared/test-claude-resolver-launcher.sh`, `scripts/container-smoke.sh` |
| `workcell session start --agent copilot` | `tests/scenarios/shared/test-copilot-session-dry-run.sh` for detached-session launch metadata, dry-run token redaction, and non-dry-run Copilot token handoff assembly/consumption cleanup; live provider-authenticated Copilot CLI certification remains covered by certification-only provider-e2e |
| `docs/examples/enterprise-claude-setup.md` | `tests/scenarios/shared/test-auth-commands.sh`, `tests/scenarios/shared/test-auth-status.sh`, `tests/scenarios/shared/test-policy-commands.sh`, `tests/scenarios/shared/test-publish-pr-dry-run.sh` |

There is no Antigravity quickstart row until the matching adapter, credential
path, scenario evidence, and live certification land.

## Manual authenticated smoke

`./scripts/provider-e2e.sh` is the reviewed path for provider-authenticated
checks. It is deliberately separate from default CI and from the repo-required
scenario tier so the default path stays deterministic and secretless.

Use it when you need to verify:

- real provider login reuse
- provider-specific auth selection
- injected MCP or project-registry behavior
- provider UX that only shows up with a live account
- Copilot or future Antigravity CLI behavior that depends on live staged
  provider credentials

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
`pre-merge` profile will execute and which selected lanes remain GitHub-only.

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
