# Workcell

[![CI](https://github.com/omkhar/workcell/actions/workflows/ci.yml/badge.svg)](https://github.com/omkhar/workcell/actions/workflows/ci.yml)
[![Docs](https://github.com/omkhar/workcell/actions/workflows/docs.yml/badge.svg)](https://github.com/omkhar/workcell/actions/workflows/docs.yml)
[![Security](https://github.com/omkhar/workcell/actions/workflows/security.yml/badge.svg)](https://github.com/omkhar/workcell/actions/workflows/security.yml)

Workcell runs coding agents inside a bounded local runtime on Apple Silicon
macOS: a dedicated Colima VM plus a hardened container inside that VM. It ships
Tier 1 adapters for Codex, Claude Code, GitHub Copilot CLI, and Gemini that
seed each provider's native control plane without pretending provider config is
the security boundary. Google Antigravity CLI remains a queued fail-closed
follow-on; current releases do not support `--agent antigravity`.

This project is for teams that want local agent velocity without turning the
host home directory, keychain, provider state, or local sockets into the trust
boundary.

## Why Workcell

- keep the runtime boundary explicit: dedicated VM, hardened container, minimal
  mounts
- keep provider adapters native: one shared boundary, thin provider-specific
  control-plane mapping
- keep publication on the host: signed commits, signed-range verification, and
  GitHub publication stay out of Tier 1
- keep verification paths nonroot by default: runtime and validator images
  default to a named unprivileged `workcell` user, while repo-mounted
  validation lanes pass explicit caller UID/GID and isolated writable state,
  with a synthesized isolated home when the caller UID has no passwd entry in
  the image
- keep lower-assurance paths visible: `development`, package mutation,
  transcripts, and `breakglass` are labeled instead of implied

## How it compares

| Approach | Primary boundary | Provider-native control plane | Host-side signed publication | Lower-assurance paths called out |
|---|---|---|---|---|
| Host-native provider CLI | host user session | yes | no | rarely |
| Generic container wrapper | container only, often mixed with host state | often partial | varies | often unclear |
| Workcell | dedicated Colima VM plus hardened container | yes | yes | yes |

## Project status

- pre-1.0 and still tightening the public contract
- Apple Silicon macOS hosts only today; Linux and Windows are not currently
  supported as launch hosts
- local host-launched runtime first; cloud-facing paths today are the
  preview-only `remote_vm/aws-ec2-ssm/compat` and
  `remote_vm/gcp-vm/compat` broker plans, and their live smokes remain
  certification-only
- CLI surfaces for Codex, Claude, Copilot, and upstream-served Gemini auth
  modes plus host-side detached session control and inspection commands
- GitHub Copilot CLI uses explicit `copilot_github_token` staging through
  reviewed host-side inputs, converts it to a host-mounted token handoff outside
  mounted provider state, moves it through a transient runtime handoff file,
  and exports its value as `COPILOT_GITHUB_TOKEN` only to the managed Copilot
  child process, with isolated `COPILOT_HOME` and `COPILOT_CACHE_HOME`; host
  `gh` auth, Copilot provider state (`~/.copilot`,
  `~/.config/github-copilot`, `~/.cache/github-copilot`), keychains, and
  whole-home state are not safe-path inputs
- Google Antigravity CLI is queued behind the same evidence bar and remains
  planned/fail-closed until Workcell ships adapter, auth, quickstart,
  deterministic evidence, and live certification together
- GitHub-hosted CI verifies repo shape, reproducibility, release posture, and
  secretless runtime behavior
- GitHub-hosted CI verifies bundle install/uninstall and Homebrew
  install/uninstall on Apple Silicon `macos-26` and `macos-15` on pushes to
  `main`, manual dispatch, and PRs labeled `approved-heavy-ci`
- the real macOS Colima boundary is still a local operator exercise because
  GitHub-hosted Linux runners cannot prove it
- the canonical host support boundary lives in
  [policy/host-support-matrix.tsv](policy/host-support-matrix.tsv), and
  `--doctor` / `--inspect` emit matching host and `support_matrix_*` lines
- Workcell does not yet ship a centralized enterprise policy, inventory, or
  analytics plane; team rollout today relies on distributing reviewed
  host-side files

Breaking changes should be called out in [CHANGELOG.md](CHANGELOG.md) and
tracked in [ROADMAP.md](ROADMAP.md).

## Community

- use GitHub Discussions for usage questions, operator workflow notes, and
  open-ended design conversations
- use GitHub issues for confirmed bugs and concrete feature requests
- use [SECURITY.md](SECURITY.md) for security-sensitive reports

See [SUPPORT.md](SUPPORT.md), [CONTRIBUTING.md](CONTRIBUTING.md), and
[CITATION.cff](CITATION.cff) for the contributor and operator contract.

## Choose your path

Pick the entry point that matches what you need. Each is a short labeled list of
links; the full index is in the [Docs map](#docs-map) below.

- **Operators — run Workcell locally**: [5-minute path](#5-minute-path) ·
  [install options](docs/install.md) ·
  [onboarding and auth](docs/onboarding-and-auth.md) ·
  [provider quickstarts](docs/provider-quickstarts.md) ·
  [command reference](#command-reference) ·
  [mode map](docs/mode-map.md) ·
  [safe-path expectations](docs/safe-path-expectations.md)
- **Enterprise evaluators — assess the assurance model**:
  [enterprise evidence baseline](docs/enterprise-evidence-baseline.md) ·
  [threat model](docs/threat-model.md) ·
  [security invariants](docs/invariants.md) ·
  [support tiers](docs/support-tiers.md) ·
  [enterprise rollout](docs/enterprise-rollout.md)
- **Contributors — work on Workcell**: [repository layout](#repository-layout) ·
  [contributor workflow](CONTRIBUTING.md) ·
  [agent guidelines](AGENTS.md) ·
  [improvement-tracks plan](docs/improvement-tracks-implementation-plan.md)

## 5-minute path

Install Workcell, create the host-side auth policy, inspect the derived
posture, then launch. `./scripts/install.sh` below assumes you are in a
verified or source tree; to install a tagged release instead, use the verified
one-command path `./scripts/install-release.sh --version vX.Y.Z` (see
[Install](#install) for the tag clone, signature checks, and the optional
`--attestation` gate).

```bash
./scripts/install.sh
workcell auth init
workcell auth set \
  --agent codex \
  --credential codex_auth \
  --source /Users/example/.config/workcell/codex-auth.json
workcell --agent codex --doctor --workspace /path/to/repo
workcell --agent codex --inspect --workspace /path/to/repo
workcell --agent codex --workspace /path/to/repo
```

For Copilot, use the provider-specific credential instead of the Codex auth
file:

```bash
workcell auth set \
  --agent copilot \
  --credential copilot_github_token \
  --source /Users/example/.config/workcell/copilot-github-token.txt
workcell --agent copilot --workspace /path/to/repo
```

See [docs/getting-started.md](docs/getting-started.md) for the release install
path and provider-specific onboarding. For team rollout patterns on today's
local-first product, see [docs/enterprise-rollout.md](docs/enterprise-rollout.md).
Use [policy/host-support-matrix.tsv](policy/host-support-matrix.tsv) to interpret the
host support boundary that `--doctor` and `--inspect` report.

## Install

On Apple Silicon macOS, the recommended path is the one-command **verified
release install**, which downloads a tagged release, verifies its cosign
signature and digest fail-closed **before any bundle code runs**, and only then
installs. `install-release.sh` is not a standalone release asset, so get it
from the repository over TLS (a trusted source) rather than the unverified
bundle — clone the repo, then run it:

```bash
brew install cosign git gnupg   # verifier tools must exist before verification runs (macOS ships neither gnupg nor, on a clean host, git)
git clone --branch vX.Y.Z --depth 1 https://github.com/omkhar/workcell.git
cd workcell
git tag -v vX.Y.Z        # verify the tag signature before running the installer
./scripts/install-release.sh --version vX.Y.Z
```

Clone the **tag** (`--branch vX.Y.Z`), not the mutable default branch: the
pre-trust installer runs before any release verification, so it must come from
the signed, immutable release commit rather than whatever `main` currently
holds. `git tag -v` authenticates that commit against the maintainer signing
key **before** you execute the installer — import and confirm the key
fingerprint from [SECURITY.md](SECURITY.md#signing-key) first.

The verifier tools (`cosign`, and `git`/`gnupg` for the clone and tag check)
must already be installed, because verification runs **before** the bundle
installer that provides the other host packages (`colima`, `docker`, `go`); pass
`-- --no-install-deps` for a launcher-only install. For an **additional** GitHub
attestation check, append `--attestation` — that step needs `gh` installed and
authenticated (`brew install gh && gh auth login`) and network access. To verify
and install straight from the release page without a clone, use the manual
cosign flow in [docs/getting-started.md](docs/getting-started.md); if you already
have a verified, unpacked release tree, run `./scripts/install.sh` from inside
it.

For the Homebrew formula asset, the source checkout path, and the full host
requirements, see [docs/install.md](docs/install.md).

To reclaim stale runtime/cache/temp state without uninstalling, run
`workcell --gc`. To remove Workcell, run `./scripts/uninstall.sh` (`--dry-run`
first to preview) for a bundle/source install, or `brew uninstall workcell` for
a Homebrew formula install. Both are detailed in
[docs/install-lifecycle.md](docs/install-lifecycle.md).

## Command reference

The supported commands at a glance; follow the links for the full behavior and
options.

- `workcell --agent <name> --workspace /path/to/repo` — launch a managed agent
  session (see the [5-minute path](#5-minute-path) and
  [provider quickstarts](docs/provider-quickstarts.md)).
- `--target colima|docker-desktop|aws-ec2-ssm|gcp-vm` — select the runtime
  backend ([safe-path expectations](docs/safe-path-expectations.md)).
- `--prepare` and `--prepare-only` — pre-build the runtime image before, or
  instead of, launching ([safe-path expectations](docs/safe-path-expectations.md)).
- `--doctor`, `--inspect`, and `--auth-status` — inspect host readiness, a
  resolved launch plan, and auth posture
  ([onboarding and auth](docs/onboarding-and-auth.md)).
- `workcell why` — explain a credential or configuration decision
  ([onboarding and auth](docs/onboarding-and-auth.md)).
- `workcell session` — manage detached sessions, including
  `workcell session start`, `workcell session list`, and
  `workcell session diff` ([safe-path expectations](docs/safe-path-expectations.md)).
- `workcell publish-pr` — the host-side PR publication helper
  ([safe-path expectations](docs/safe-path-expectations.md)).

## Docs map

### Operator reference

| Topic | File |
|---|---|
| Install and requirements | [docs/install.md](docs/install.md) |
| Onboarding and auth | [docs/onboarding-and-auth.md](docs/onboarding-and-auth.md) |
| Provider quickstarts | [docs/provider-quickstarts.md](docs/provider-quickstarts.md) |
| Mode map | [docs/mode-map.md](docs/mode-map.md) |
| Safe-path expectations | [docs/safe-path-expectations.md](docs/safe-path-expectations.md) |
| Release posture | [docs/release-posture.md](docs/release-posture.md) |

### Product and security docs

| Topic | File |
|---|---|
| Getting started | [docs/getting-started.md](docs/getting-started.md) |
| Support tiers | [docs/support-tiers.md](docs/support-tiers.md) |
| Diagnostics and support matrix | [docs/diagnostics-and-support-matrix.md](docs/diagnostics-and-support-matrix.md) |
| Security invariants | [docs/invariants.md](docs/invariants.md) |
| Threat model | [docs/threat-model.md](docs/threat-model.md) |
| CI/CD threat model | [docs/ci-threat-model.md](docs/ci-threat-model.md) |
| OWASP agentic mapping | [docs/owasp-agentic-mapping.md](docs/owasp-agentic-mapping.md) |
| Provider matrix | [docs/provider-matrix.md](docs/provider-matrix.md) |
| Provider bootstrap matrix | [docs/provider-bootstrap-matrix.md](docs/provider-bootstrap-matrix.md) |
| Adapter control planes | [docs/adapter-control-planes.md](docs/adapter-control-planes.md) |
| Injection policy | [docs/injection-policy.md](docs/injection-policy.md) |
| Validation coverage | [docs/validation-scenarios.md](docs/validation-scenarios.md) |
| Requirements validation | [docs/requirements-validation.md](docs/requirements-validation.md) |
| Scenario gaps | [docs/scenario-gaps.md](docs/scenario-gaps.md) |
| Use-case coverage | [docs/use-case-matrix.md](docs/use-case-matrix.md) |
| Session supervisor design | [docs/workcell-session-supervisor-design.md](docs/workcell-session-supervisor-design.md) |
| Managed workstation contract | [docs/managed-workstation-contract.md](docs/managed-workstation-contract.md) |
| Enterprise evidence baseline | [docs/enterprise-evidence-baseline.md](docs/enterprise-evidence-baseline.md) |
| Enterprise rollout | [docs/enterprise-rollout.md](docs/enterprise-rollout.md) |
| Host expansion readiness | [docs/host-expansion-readiness.md](docs/host-expansion-readiness.md) |
| AWS EC2 SSM preview | [docs/aws-ec2-ssm-preview.md](docs/aws-ec2-ssm-preview.md) |
| GCP VM preview | [docs/gcp-vm-preview.md](docs/gcp-vm-preview.md) |
| Provenance and signing | [docs/provenance.md](docs/provenance.md) |
| GitHub automation | [docs/github-workflows.md](docs/github-workflows.md) |
| Artifact retention policy | [docs/retention-policy.md](docs/retention-policy.md) |

### Project docs

| Topic | File |
|---|---|
| Contributor workflow | [CONTRIBUTING.md](CONTRIBUTING.md) |
| Support | [SUPPORT.md](SUPPORT.md) |
| Code of conduct | [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) |
| Governance | [GOVERNANCE.md](GOVERNANCE.md) |
| Maintainers | [MAINTAINERS.md](MAINTAINERS.md) |
| Roadmap | [ROADMAP.md](ROADMAP.md) |
| Changelog | [CHANGELOG.md](CHANGELOG.md) |
| Security reporting | [SECURITY.md](SECURITY.md) |
| Stability and exit-code contract | [docs/stability-contract.md](docs/stability-contract.md) |
| Standards watchlist | [docs/standards-watchlist.md](docs/standards-watchlist.md) |

## Repository layout

- `runtime/`: VM and container boundary implementation
- `policy/`: shared contract layer and hosted-control policy
- `adapters/`: provider-native baselines for Codex, Claude, Copilot, and
  Gemini, plus fail-closed Antigravity planning scaffolding
- `cmd/`: host-side and runtime-side Go entrypoints (the `workcell-*` binaries)
- `internal/`: shared Go packages backing the `cmd/` binaries
- `scripts/`: launcher, validation, release, audit, and bootstrap entrypoints
- `verify/`: invariant-oriented verification material
- `man/`: workcell.1 manpage
- `tests/`: scenario manifests and fixtures
- `tools/`: developer tooling (markdownlint, validator image)
- `docs/`: user-facing design, quickstarts, install, and release docs
- `workflows/`: implementation notes such as adapter porting guidance

## License

Workcell is licensed under Apache-2.0. See `LICENSE`.
