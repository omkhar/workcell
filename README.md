# Workcell

Workcell runs coding agents inside a bounded local runtime on macOS: a
dedicated Colima VM plus a hardened container inside that VM. It supports
Codex, Claude Code, and Gemini through thin provider adapters that seed each
provider's native control plane without pretending the provider config itself
is the security boundary.

## Priorities

1. Developer experience
2. Simplicity
3. Security invariant preservation
4. Performance
5. Idiomatic correctness

Those priorities apply only after the boundary and invariants are fixed.
Workcell does not trade away the VM-plus-container boundary for convenience.

## Requirements

- **macOS** (Apple Silicon or Intel). Workcell manages a dedicated
  [Colima](https://github.com/abiosoft/colima) VM profile using Apple's
  Virtualization.Framework. Linux and Windows host platforms are not currently
  supported.
- **Homebrew**, **Colima**, and **Docker CLI** installed on the host:
  `brew install colima docker`
- **git**, **python3**, and **GitHub CLI** (`gh`) available on `$PATH`

## Design stance

Workcell treats the external runtime as primary:

- the selected workspace is the only intended writable host mount
- host homes, keychains, credential helpers, and agent sockets stay out
- `docker.sock`, `ssh-agent`, GPG agent sockets, and provider state are not
  passed through on the safe path
- repo-local control-plane files such as `AGENTS.md`, `CLAUDE.md`,
  `GEMINI.md`, `.codex/`, `.claude/`, and `.gemini/` are masked inside the
  mounted workspace and imported into provider homes instead
- lower-assurance paths are explicit, acknowledged, and surfaced in audit
  output

The result is one shared boundary with provider-native adapters on top, not a
fake universal agent abstraction.

## Start here

Read the repo in this order:

1. start with the quick start below
2. use [docs/invariants.md](docs/invariants.md) for the contract
3. use the provider quickstarts for provider-specific setup
4. use [docs/provenance.md](docs/provenance.md) and
   [docs/github-workflows.md](docs/github-workflows.md) for release and
   maintainer details

Workcell uses two terms that matter throughout the docs:

- `Tier 1`: a provider CLI running fully inside the bounded Workcell runtime
- `strict`: the default managed Tier 1 runtime mode

## Mode map

| Path | Intended use | Key properties |
|---|---|---|
| `strict` | default developer lane | bounded VM plus container, reviewed network posture, repo control-plane masking, `--agent-autonomy yolo` by default |
| `strict --container-mutability readonly` | strongest managed lane | package-manager writes blocked; no package-mutation downgrade |
| `build` | image preparation and dependency refresh | broader egress for rebuild and preparation work |
| `breakglass` | explicit higher-trust debugging path | requires `--ack-breakglass`; visibly lower assurance |

Other defaults that matter:

- `--agent` is always required; there is no default provider
- `--agent-autonomy yolo` is the default; `--agent-autonomy prompt` is the
  explicit lower-assurance opt-out
- `--cache-profile off` is the default
- `--prepare` is the recommended first-run path

## Quick start

Install the launcher symlink and verify the host prerequisites:

```bash
./scripts/install.sh
workcell --prepare --agent codex --workspace /path/to/repo
```

Launch a provider inside the bounded runtime:

```bash
workcell --agent codex --workspace /path/to/repo
workcell --agent claude --workspace /path/to/repo
workcell --agent gemini --workspace /path/to/repo
```

Useful operator flows:

```bash
workcell --prepare-only --agent codex --workspace /path/to/repo
workcell --inspect --agent codex --workspace /path/to/repo
workcell --doctor --agent codex --workspace /path/to/repo
workcell --auth-status --agent codex --workspace /path/to/repo
workcell --logs audit --colima-profile workcell-...
workcell publish-pr --workspace /path/to/repo --branch feature/name \
  --title-file /tmp/pr-title.txt \
  --body-file /tmp/pr-body.md \
  --commit-message-file /tmp/commit-message.txt
```

What to expect from the safe path:

- Workcell launches the selected provider directly inside the bounded runtime
- there is no separate "start a container, then attach the agent" step
- `publish-pr` runs on the host so signed commits and GitHub publication stay
  outside the Tier 1 container
- `--debug-log`, `--file-trace-log`, and `--audit-transcript` are explicit
  lower-assurance operator choices and are off by default

## Local development

Run the local check suite before pushing:

```bash
./build_and_test.sh             # builds validator container, runs all checks
./build_and_test.sh --list      # list files that would be checked (no Docker needed)
./build_and_test.sh --strict    # exit on first failure
./build_and_test.sh --auto-fix  # run markdownlint --fix before linting
```

This runs inside the same Docker container that CI uses. Release-specific
checks (upstream verification, release bundle) only run in CI. Requires
Docker (Docker Desktop or colima), except `--list` which runs on the host.

## Session inputs

The supported way to feed stable inputs into sessions is an explicit injection
policy, usually at `~/.config/workcell/injection-policy.toml`.

For host-side setup, Workcell also ships `workcell auth init|set|unset|status`
to manage explicit policy-backed credentials without widening the runtime
boundary. Those host-side auth commands only rewrite the entrypoint policy
file, so credentials declared through `includes = [...]` must still be updated
in the owning fragment.

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

## Provider support

| Provider | Tier 1 surface today | Native control plane | Quickstart |
|---|---|---|---|
| Codex | CLI | `~/.codex/config.toml`, `AGENTS.md`, rules, MCP config | [docs/examples/quickstart-codex.md](docs/examples/quickstart-codex.md) |
| Claude | Claude Code CLI | `~/.claude/settings.json`, `CLAUDE.md`, `.mcp.json`, auth mirrors, hooks, host-side macOS auth resolver scaffold | [docs/examples/quickstart-claude.md](docs/examples/quickstart-claude.md) |
| Gemini | Gemini CLI | `~/.gemini/settings.json`, `GEMINI.md`, `.env`, `projects.json` | [docs/examples/quickstart-gemini.md](docs/examples/quickstart-gemini.md) |

GUI and IDE surfaces are lower assurance unless they act only as clients to
the same bounded runtime.

## Release posture

Tagged releases are rebuilt and verified before publication. The release path:

- reruns validation, smoke, and reproducibility checks
- publishes from the archived source bundle rather than the live checkout
- signs the image, source bundle, checksums, build-input manifest,
  control-plane manifest, builder-environment manifest, and both SBOMs with
  keyless Sigstore/Cosign
- publishes GitHub-native attestations in the canonical upstream repository as
  an additional verification surface, not a replacement for Sigstore

Forks can keep the GitHub attestation gate off. The upstream repo treats that
setting as hosted control-plane state and audits it accordingly.

See [docs/provenance.md](docs/provenance.md) and
[docs/github-workflows.md](docs/github-workflows.md).

## Documentation map

| Topic | File |
|---|---|
| Security invariants | [docs/invariants.md](docs/invariants.md) |
| Threat model | [docs/threat-model.md](docs/threat-model.md) |
| Provider matrix | [docs/provider-matrix.md](docs/provider-matrix.md) |
| Adapter control planes | [docs/adapter-control-planes.md](docs/adapter-control-planes.md) |
| Injection policy | [docs/injection-policy.md](docs/injection-policy.md) |
| Validation coverage | [docs/validation-scenarios.md](docs/validation-scenarios.md) |
| Scenario gaps | [docs/scenario-gaps.md](docs/scenario-gaps.md) |
| Use-case coverage | [docs/use-case-matrix.md](docs/use-case-matrix.md) |
| Provenance and signing | [docs/provenance.md](docs/provenance.md) |
| GitHub automation | [docs/github-workflows.md](docs/github-workflows.md) |
| Contributor workflow | [CONTRIBUTING.md](CONTRIBUTING.md) |
| Security reporting | [SECURITY.md](SECURITY.md) |

## Repository layout

- `runtime/`: VM and container boundary implementation
- `policy/`: shared contract layer and hosted-control policy
- `adapters/`: provider-native baselines for Codex, Claude, and Gemini
- `scripts/`: launcher, validation, release, and audit entrypoints
- `verify/`: invariant-oriented verification material
- `docs/`: user-facing design, quickstarts, and release docs
- `workflows/`: implementation notes such as adapter porting guidance

## License

Workcell is licensed under Apache-2.0. See `LICENSE`.
