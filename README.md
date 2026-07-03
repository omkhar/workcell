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

## 5-minute path

Install Workcell, create the host-side auth policy, inspect the derived
posture, then launch:

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

## Install options

### Tagged release bundle

Download a tagged release bundle, unpack it, and run the supported installer:

```bash
tar -xzf workcell-vX.Y.Z.tar.gz
cd workcell-vX.Y.Z
./scripts/install.sh
```

On Apple Silicon macOS, `./scripts/install.sh` installs only the missing
required Homebrew formulas (`colima`, `docker`, `gh`, `git`, `go`) before it
links the launcher. Use `./scripts/install.sh --no-install-deps` to leave the
system unchanged and get a warning summary of anything still missing.

### Tagged Homebrew formula asset

Tagged releases can publish a versioned `workcell.rb` asset. Download it from
the release page and install it locally with Homebrew:

```bash
curl -LO https://github.com/omkhar/workcell/releases/download/vX.Y.Z/workcell.rb
brew install --formula ./workcell.rb
```

The formula declares the same required host dependencies: `colima`, `docker`,
`gh`, `git`, and `go`.

### Source checkout

For contributors and local repo review:

```bash
git clone https://github.com/omkhar/workcell.git
cd workcell
./scripts/install.sh
```

`./scripts/install.sh` is the supported installer entrypoint. The
`scripts/install-workcell.sh` helper remains an internal implementation detail.

## Requirements

- **macOS** (Apple Silicon only). Workcell manages a dedicated
  [Colima](https://github.com/abiosoft/colima) VM profile using Apple's
  Virtualization.Framework. Linux and Windows host platforms are not currently
  supported.
- **Homebrew** available on the host if you want the installer to auto-install
  missing required packages.
- Required host packages: `colima`, `docker`, `gh`, `git`, and `go`.
  `./scripts/install.sh` installs only the missing ones on supported macOS
  hosts by default, or you can install them yourself with
  `brew install colima docker gh git go`.

## Onboarding and auth

The supported way to feed stable inputs into sessions is an explicit injection
policy, usually at `~/.config/workcell/injection-policy.toml`.

Use the host-side auth helpers instead of hand-editing the common case:

```bash
workcell auth init
workcell auth set --agent codex --credential codex_auth --source /path/to/auth.json
workcell auth status --agent codex
workcell auth unset --agent codex --credential codex_auth
workcell policy validate
workcell why --agent codex --mode strict --credential codex_auth
workcell --agent codex --auth-status --workspace /path/to/repo
```

`workcell auth status` shows the host policy view. `--auth-status` shows the
derived launch view after selector evaluation and preprocessing.
`workcell policy show|validate|diff` inspects the merged host policy, and
`workcell why` explains why one credential is selected, out of scope, filtered,
or still only configured on the host side.

Direct staged credentials are the primary supported auth path today. Built-in
resolver coverage now includes Codex host-auth reuse through
`codex-home-auth-file`, while the Claude macOS resolver remains a fail-closed
scaffold until a supported export path exists.

`workcell auth status` and `workcell --auth-status` print
`provider_bootstrap_*` lines, and `workcell why` prints `bootstrap_*` lines for
the selected credential. Use those fields with
[docs/provider-bootstrap-matrix.md](docs/provider-bootstrap-matrix.md) to see
whether a path is repo-required, certification-only, or manual.

Workcell can stage:

- common or provider-specific instruction fragments
- provider-native credentials such as `codex_auth`, `claude_auth`,
  `claude_api_key`, `claude_mcp`, `copilot_github_token`, `gemini_env`,
  `gemini_oauth`, `gemini_projects`, and `gcloud_adc`
- scoped GitHub CLI credentials through `github_hosts` and `github_config`
- SSH config, known hosts, and identities
- explicit copied files or directories for non-reserved paths

It does not support whole-home passthrough, arbitrary environment-variable
secret injection, or host socket forwarding on the safe path.

Copilot auth is intentionally narrow: configure `copilot_github_token` and let
Workcell stage it through reviewed host-side inputs. For an auth-required
Copilot launch, the launcher removes the original staged token file from direct
runtime mounts, passes a temporary host-mounted token handoff outside mounted
provider state, the runtime entrypoint consumes it into a transient handoff
file, unlinks the mounted file, and re-execs without the token in its
environment. The wrapper unlinks that runtime file before exporting the value
as `COPILOT_GITHUB_TOKEN` only to the managed Copilot child process.
Workcell does not copy or pre-stage the token into `COPILOT_HOME`. Host `gh`
auth, `GH_TOKEN`, `GITHUB_TOKEN`, host Copilot provider state (`~/.copilot`,
`~/.config/github-copilot`, `~/.cache/github-copilot`), keychains, and
whole-home state are not readiness or auth sources. Antigravity credentials and
provider-home state are not supported inputs yet.

See [docs/injection-policy.md](docs/injection-policy.md) and
[docs/examples/injection-policy.toml](docs/examples/injection-policy.toml).
The by-provider bootstrap tiers and handoffs live in
[docs/provider-bootstrap-matrix.md](docs/provider-bootstrap-matrix.md).

## Provider quickstarts

| Provider | Tier 1 surface today | Native control plane | Quickstart |
|---|---|---|---|
| Codex | CLI | `~/.codex/config.toml`, `AGENTS.md`, rules, MCP config | [docs/examples/quickstart-codex.md](docs/examples/quickstart-codex.md) |
| Claude | Claude Code CLI | `~/.claude/settings.json`, `CLAUDE.md`, `.mcp.json`, auth mirrors, hooks, host-side macOS auth resolver scaffold | [docs/examples/quickstart-claude.md](docs/examples/quickstart-claude.md) |
| GitHub Copilot CLI | CLI | session-local `COPILOT_HOME`, `COPILOT_CACHE_HOME`, token handoff, custom instructions disabled, skill/dynamic-retrieval overrides blocked | [docs/examples/quickstart-copilot.md](docs/examples/quickstart-copilot.md) |
| Gemini | Gemini CLI | `~/.gemini/settings.json`, `GEMINI.md`, `.env`, `projects.json` | [docs/examples/quickstart-gemini.md](docs/examples/quickstart-gemini.md) |

Planned provider parity:

| Provider | Target surface | Required before support |
|---|---|---|
| Google Antigravity CLI | planned fail-closed Tier 1 CLI adapter; not current support | `--agent antigravity`, pinned official install/auth provenance, explicit Google auth staging, session-local provider home/cache, unsafe-argument policy, quickstart, deterministic tests, and live provider certification |

GUI and IDE surfaces are lower assurance unless they act only as clients to
the same bounded runtime.

See [docs/injection-policy.md](docs/injection-policy.md) for provider auth
maturity and [docs/enterprise-rollout.md](docs/enterprise-rollout.md) for the
current team rollout model.

## Mode map

Workcell uses two terms throughout the docs:

- `Tier 1`: a provider CLI running fully inside the bounded Workcell runtime
- `strict`: the default managed Tier 1 runtime mode

`--mode` selects one of four lanes:

| `--mode` | Intended use | Key properties |
|---|---|---|
| `strict` | default provider lane | bounded VM plus container, reviewed network posture, repo control-plane masking, provider-focused entrypoint, `--agent-autonomy yolo` by default |
| `development` | managed interactive development lane | same boundary and masking as `strict`, managed non-provider command execution, broader dependency egress, visibly lower assurance than `strict` |
| `build` | image preparation and dependency refresh | broader egress for rebuild and preparation work |
| `breakglass` | explicit higher-trust debugging path | requires `--ack-breakglass=YYYY-MM-DD` using today's UTC date; visibly lower assurance |

`--container-mutability` is orthogonal to `--mode`: `ephemeral` (the
default) allows package-manager mutations and labels the session
`managed-mutable`, while `readonly` blocks package-manager writes and
gives the strongest managed posture available — `--mode strict
--container-mutability readonly` is the lane to pick when no
lower-assurance downgrade is acceptable.

Other defaults that matter:

- `--agent` is always required; there is no default provider
- `--agent-autonomy yolo` is the default; `--agent-autonomy prompt` is the
  explicit lower-assurance opt-out
- `--cache-profile off` is the default
- `--cache-profile standard` keeps a workspace-scoped persistent non-secret
  cache plane for package and compiler caches, but it is an explicit
  lower-assurance path
- strict launches prepare the reviewed runtime image automatically when needed
- interactive launches show a spinner with elapsed time by default; use
  `--no-spinner` to force plain heartbeat updates instead
- `--prepare` and `--prepare-only` remain useful when you want to make that step explicit

## Safe-path expectations

- Workcell launches the selected provider directly inside the bounded runtime
- there is no separate "start a container, then attach the agent" step
- `publish-pr` runs on the host so signed commits, signed-range verification,
  and GitHub publication stay outside the Tier 1 container, and it blocks
  unsigned publish ranges and over-broad branch diffs before push so published
  PRs stay reviewable; `main` is the only supported PR base by default, and
  non-`main` bases remain an explicit lower-assurance draft-only escape hatch
  with an explicit preflight warning that repo-owned PR checks are not expected
  for that base; reviewed, live-certified adapter support PRs may use the
  bounded `approved-large-certified-adapter` label plus
  `--approved-large-certified-adapter` publication flag when they cannot be
  split without invalidating certification evidence
- completed and aborted launches are recorded as durable host-side session
  records that you can inspect with `workcell session ...`
- `workcell session diff` compares the current workspace against the clean git
  base recorded at launch and fails closed when the launch started dirty, when
  no launch git base was recorded, or when the workspace is not a self-contained
  git worktree
- `--debug-log`, `--file-trace-log`, and `--audit-transcript` are explicit
  lower-assurance operator choices and are off by default

Useful operator flows:

For changes to this repository, publish main-based PRs through the repo wrapper
after fresh local parity evidence:

```bash
./scripts/pre-merge.sh --profile pr-parity
./scripts/repo-publish-pr.sh --workspace /path/to/repo --branch feature/name \
  --title-file /tmp/pr-title.txt \
  --body-file /tmp/pr-body.md \
  --commit-message-file /tmp/commit-message.txt
```

`workcell publish-pr` is the lower-level host-side helper. Use it directly for
operator repositories that do not carry Workcell's repo-local parity wrapper,
or for the explicitly lower-assurance non-`main` draft path.

Use `--target colima|docker-desktop|aws-ec2-ssm|gcp-vm` to select the managed
runtime backend.

```bash
workcell --agent codex --prepare --workspace /path/to/repo
workcell --agent codex --prepare-only --workspace /path/to/repo
workcell --target docker-desktop --agent codex --workspace /path/to/repo
workcell --target aws-ec2-ssm --target-id i-1234567890abcdef0 --agent codex --workspace /path/to/repo --dry-run
workcell --target gcp-vm --target-id workcell-phase8-cert --agent codex --workspace /path/to/repo --dry-run
workcell --agent codex --mode development --workspace /path/to/repo -- bash -lc 'git status'
workcell session list
workcell session list --verbose
workcell session start --agent codex --workspace /path/to/repo
workcell session delete --id SESSION_ID
workcell session attach --id 20260408T120000Z-1a2b3c4d
workcell session send --id 20260408T120000Z-1a2b3c4d --message "continue with tests"
workcell session stop --id 20260408T120000Z-1a2b3c4d
workcell session show --id 20260408T120000Z-1a2b3c4d
workcell session show --id 20260408T120000Z-1a2b3c4d --text
workcell session logs --id 20260408T120000Z-1a2b3c4d --kind audit
workcell session timeline --id 20260408T120000Z-1a2b3c4d
workcell session diff --id 20260408T120000Z-1a2b3c4d
workcell session export --id 20260408T120000Z-1a2b3c4d --output /tmp/workcell-session.json
workcell policy show
workcell policy diff
workcell why --agent codex --mode strict --credential codex_auth
workcell --agent codex --doctor --workspace /path/to/repo
workcell --agent codex --inspect --workspace /path/to/repo
workcell --agent codex --auth-status --workspace /path/to/repo
workcell --gc
./scripts/update-upstream-pins.sh --check
./scripts/publish-provider-bump-pr.sh
workcell --logs audit --colima-profile wcl-...
# Lower-level host publication helper for repositories without a repo wrapper.
workcell publish-pr --workspace /path/to/repo --branch feature/name \
  --title-file /tmp/pr-title.txt \
  --body-file /tmp/pr-body.md \
  --commit-message-file /tmp/commit-message.txt
# Reviewed exception: live-certified adapter PRs that cannot be split.
workcell publish-pr --workspace /path/to/repo --branch feature/name \
  --approved-large-certified-adapter \
  --title-file /tmp/pr-title.txt \
  --body-file /tmp/pr-body.md \
  --commit-message-file /tmp/commit-message.txt
# Lower-assurance exception: non-main bases stay draft-only.
workcell publish-pr --workspace /path/to/repo --branch feature/name \
  --base feature/review-stack --allow-non-main-base \
  --title-file /tmp/pr-title.txt \
  --body-file /tmp/pr-body.md \
  --commit-message-file /tmp/commit-message.txt
```

For the preview-only AWS and GCP remote VM broker paths and their certification
gates, see [docs/aws-ec2-ssm-preview.md](docs/aws-ec2-ssm-preview.md) and
[docs/gcp-vm-preview.md](docs/gcp-vm-preview.md).

`workcell session list --verbose` adds target, workspace transport, git branch,
and worktree columns without changing the default compact inventory view.
`workcell session show --text` renders stable key=value lines for the same
target-aware record, and `workcell session start|send|stop` emit stable
key=value summaries so host-side detached control stays scriptable.
`workcell --gc` removes stale Workcell-owned temp scratch, disposable
session-audit directories, broken latest-log pointers, and over-budget runtime
image cache entries without deleting durable session records. It also removes
stale regenerateable Workcell build cache entries.

## Release posture

Tagged releases are rebuilt and verified before publication. The release path:

- reruns validation, smoke, and reproducibility checks
- reruns repo-mounted validator and release-helper paths under an explicit
  caller UID/GID with isolated writable home, cache, and tmp roots instead of
  relying on ambient container-root defaults, including passwd-less caller UIDs
- verifies from GitHub-owned sources that the release install matrix still
  targets the newest two GitHub-hosted Apple Silicon macOS runner labels
- refuses to publish if any reviewed provider, Linux base image, Linux
  toolchain, or release-build pin is behind the latest tracked upstream
- verifies pinned Codex, Claude, Copilot, and Gemini releases against upstream
  metadata as part of the reviewed provider set; Antigravity gets the same
  gate before any future support claim
- publishes from the archived source bundle rather than the live checkout
- gates publication on bundle and Homebrew install verification on
  GitHub-hosted Apple Silicon `macos-26` and `macos-15`
- signs the image, source bundle, Homebrew formula asset, published image
  digest file, checksums, build-input manifest, control-plane manifest,
  builder-environment manifest, and both SBOMs with keyless Sigstore/Cosign
- publishes GitHub-native attestations when the reviewed hosted controls say
  the repository visibility and GitHub plan support them for every published
  primary release artifact, as an additional verification surface rather than a
  replacement for Sigstore

That install matrix is the current release-gated support window. Other macOS
versions may work, but they are not currently proven by tagged-release CI.

Forks can keep the GitHub attestation gates off. The upstream repo treats
those settings as hosted control-plane state and audits them accordingly.

See [docs/provenance.md](docs/provenance.md) and
[docs/github-workflows.md](docs/github-workflows.md).

## Docs map

### Product and security docs

| Topic | File |
|---|---|
| Getting started | [docs/getting-started.md](docs/getting-started.md) |
| Support tiers | [docs/support-tiers.md](docs/support-tiers.md) |
| Diagnostics and support matrix | [docs/diagnostics-and-support-matrix.md](docs/diagnostics-and-support-matrix.md) |
| Security invariants | [docs/invariants.md](docs/invariants.md) |
| Threat model | [docs/threat-model.md](docs/threat-model.md) |
| Provider matrix | [docs/provider-matrix.md](docs/provider-matrix.md) |
| Adapter control planes | [docs/adapter-control-planes.md](docs/adapter-control-planes.md) |
| Injection policy | [docs/injection-policy.md](docs/injection-policy.md) |
| Validation coverage | [docs/validation-scenarios.md](docs/validation-scenarios.md) |
| Requirements validation | [docs/requirements-validation.md](docs/requirements-validation.md) |
| Scenario gaps | [docs/scenario-gaps.md](docs/scenario-gaps.md) |
| Use-case coverage | [docs/use-case-matrix.md](docs/use-case-matrix.md) |
| Session supervisor design | [docs/workcell-session-supervisor-design.md](docs/workcell-session-supervisor-design.md) |
| Managed workstation contract | [docs/managed-workstation-contract.md](docs/managed-workstation-contract.md) |
| Enterprise evidence baseline | [docs/enterprise-evidence-baseline.md](docs/enterprise-evidence-baseline.md) |
| Host expansion readiness | [docs/host-expansion-readiness.md](docs/host-expansion-readiness.md) |
| Provenance and signing | [docs/provenance.md](docs/provenance.md) |
| GitHub automation | [docs/github-workflows.md](docs/github-workflows.md) |

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
