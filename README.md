# Workcell

[![CI](https://github.com/omkhar/workcell/actions/workflows/ci.yml/badge.svg)](https://github.com/omkhar/workcell/actions/workflows/ci.yml)
[![Docs](https://github.com/omkhar/workcell/actions/workflows/docs.yml/badge.svg)](https://github.com/omkhar/workcell/actions/workflows/docs.yml)
[![Security](https://github.com/omkhar/workcell/actions/workflows/security.yml/badge.svg)](https://github.com/omkhar/workcell/actions/workflows/security.yml)

Workcell runs coding agents inside a bounded local runtime on Apple Silicon
macOS: a dedicated Colima VM plus a hardened container inside that VM. It
supports
Codex, Claude Code, and Gemini through thin provider adapters that seed each
provider's native control plane without pretending provider config is the
security boundary.

This project is for teams that want local agent velocity without turning the
host home directory, keychain, provider state, or local sockets into the trust
boundary.

## Why Workcell

- keep the runtime boundary explicit: dedicated VM, hardened container, minimal
  mounts
- keep provider adapters native: one shared boundary, thin provider-specific
  control-plane mapping
- keep publication on the host: signed commits and GitHub publication stay out
  of Tier 1
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
- Apple Silicon macOS hosts only today
- CLI surfaces for Codex, Claude, and Gemini
- GitHub-hosted CI verifies repo shape, reproducibility, release posture, and
  secretless runtime behavior
- GitHub-hosted CI continuously verifies bundle install/uninstall and Homebrew
  install/uninstall on Apple Silicon `macos-26` and `macos-15`
- the real macOS Colima boundary is still a local operator exercise because
  GitHub-hosted Linux runners cannot prove it

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

See [docs/getting-started.md](docs/getting-started.md) for the release install
path and provider-specific onboarding.

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
workcell policy validate
workcell why --agent codex --mode strict --credential codex_auth
workcell --agent codex --auth-status --workspace /path/to/repo
```

`workcell auth status` shows the host policy view. `--auth-status` shows the
derived launch view after selector evaluation and preprocessing.
`workcell policy show|validate|diff` inspects the merged host policy, and
`workcell why` explains why one credential is selected, filtered, or still only
configured on the host side.

Workcell can stage:

- common or provider-specific instruction fragments
- provider-native credentials such as `codex_auth`, `claude_auth`,
  `claude_api_key`, `claude_mcp`, `gemini_env`, `gemini_oauth`,
  `gemini_projects`, and `gcloud_adc`
- scoped GitHub CLI credentials through `github_hosts` and `github_config`
- SSH config, known hosts, and identities
- explicit copied files or directories for non-reserved paths

It does not support whole-home passthrough, arbitrary environment-variable
secret injection, or host socket forwarding on the safe path.

See [docs/injection-policy.md](docs/injection-policy.md) and
[docs/examples/injection-policy.toml](docs/examples/injection-policy.toml).

## Provider quickstarts

| Provider | Tier 1 surface today | Native control plane | Quickstart |
|---|---|---|---|
| Codex | CLI | `~/.codex/config.toml`, `AGENTS.md`, rules, MCP config | [docs/examples/quickstart-codex.md](docs/examples/quickstart-codex.md) |
| Claude | Claude Code CLI | `~/.claude/settings.json`, `CLAUDE.md`, `.mcp.json`, auth mirrors, hooks, host-side macOS auth resolver scaffold | [docs/examples/quickstart-claude.md](docs/examples/quickstart-claude.md) |
| Gemini | Gemini CLI | `~/.gemini/settings.json`, `GEMINI.md`, `.env`, `projects.json` | [docs/examples/quickstart-gemini.md](docs/examples/quickstart-gemini.md) |

GUI and IDE surfaces are lower assurance unless they act only as clients to
the same bounded runtime.

## Mode map

Workcell uses two terms throughout the docs:

- `Tier 1`: a provider CLI running fully inside the bounded Workcell runtime
- `strict`: the default managed Tier 1 runtime mode

| Path | Intended use | Key properties |
|---|---|---|
| `strict` | default provider lane | bounded VM plus container, reviewed network posture, repo control-plane masking, provider-focused entrypoint, `--agent-autonomy yolo` by default |
| `strict --container-mutability readonly` | strongest managed lane | package-manager writes blocked; no package-mutation downgrade |
| `development` | managed interactive development lane | same boundary and masking as `strict`, managed non-provider command execution, broader dependency egress, visibly lower assurance than `strict` |
| `build` | image preparation and dependency refresh | broader egress for rebuild and preparation work |
| `breakglass` | explicit higher-trust debugging path | requires `--ack-breakglass`; visibly lower assurance |

Other defaults that matter:

- `--agent` is always required; there is no default provider
- `--agent-autonomy yolo` is the default; `--agent-autonomy prompt` is the
  explicit lower-assurance opt-out
- `--cache-profile off` is the default
- strict launches prepare the reviewed runtime image automatically when needed
- `--prepare` and `--prepare-only` remain useful when you want to make that step explicit

## Safe-path expectations

- Workcell launches the selected provider directly inside the bounded runtime
- there is no separate "start a container, then attach the agent" step
- `publish-pr` runs on the host so signed commits and GitHub publication stay
  outside the Tier 1 container
- completed and aborted launches are recorded as durable host-side session
  records that you can inspect with `workcell session ...`
- `workcell session diff` compares the current workspace against the clean git
  base recorded at launch and fails closed when the launch started dirty
- `--debug-log`, `--file-trace-log`, and `--audit-transcript` are explicit
  lower-assurance operator choices and are off by default

Useful operator flows:

```bash
workcell --agent codex --prepare --workspace /path/to/repo
workcell --agent codex --prepare-only --workspace /path/to/repo
workcell --agent codex --mode development --workspace /path/to/repo -- bash -lc 'git status'
workcell session list
workcell session show --id 20260408T120000Z-1a2b3c4d
workcell session diff --id 20260408T120000Z-1a2b3c4d
workcell session export --id 20260408T120000Z-1a2b3c4d --output /tmp/workcell-session.json
workcell policy show
workcell policy diff
workcell why --agent codex --mode strict --credential codex_auth
workcell --agent codex --doctor --workspace /path/to/repo
workcell --agent codex --inspect --workspace /path/to/repo
workcell --agent codex --auth-status --workspace /path/to/repo
./scripts/update-upstream-pins.sh --check
./scripts/publish-provider-bump-pr.sh
workcell --logs audit --colima-profile wcl-...
workcell publish-pr --workspace /path/to/repo --branch feature/name \
  --title-file /tmp/pr-title.txt \
  --body-file /tmp/pr-body.md \
  --commit-message-file /tmp/commit-message.txt
```

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
- `adapters/`: provider-native baselines for Codex, Claude, and Gemini
- `scripts/`: launcher, validation, release, audit, and bootstrap entrypoints
- `verify/`: invariant-oriented verification material
- `docs/`: user-facing design, quickstarts, install, and release docs
- `workflows/`: implementation notes such as adapter porting guidance

## License

Workcell is licensed under Apache-2.0. See `LICENSE`.
