# Workcell

`Workcell` provides a bounded local runtime with explicit isolation and policy
controls for coding agents, including Codex, Claude, and Gemini. It keeps the
active agent inside a dedicated VM plus hardened container boundary and exposes
thin provider adapters instead of pretending every agent product shares one
control plane.

This project is organized around these priorities, in order:

1. Developer experience
2. Simplicity
3. Security invariant preservation
4. Performance
5. Idiomatic correctness

These priorities apply only inside a fixed invariant set. Developer experience
and simplicity can shape the safe path, but they do not justify puncturing the
runtime boundary or weakening defined security guarantees.

The invariant set is non-negotiable. The numbered priority order applies only
after the boundary and trust assumptions are fixed.

## Design stance

This repository does not treat prompts, provider config files, or IDE settings
as the main security boundary. The primary boundary is an external runtime:

- a dedicated Colima VM profile on macOS
- a hardened inner container that runs the selected agent
- each provider's native policy surface inside that container

That layered design exists because the strongest practical local boundary on
this host is the VM. The inner container is the packaging and workflow layer,
not the only wall.

The design explicitly avoids common boundary punctures:

- no host home directory mount
- no host `docker.sock`
- no SSH, GPG, or other agent socket passthrough
- no host `~/.codex` or similar persistent auth and state passthrough
- no host credential helper, keychain, or browser-profile passthrough
- no ambient secret or SSH-agent passthrough; host-side docs, config, and
  secret material may enter the session only through an explicit Workcell
  injection policy that stages them into per-session runtime state
- public in-container `/usr/local/bin` launch surfaces sanitize hostile loader
  environment variables before bash or provider wrapper logic begins; internal
  `/usr/local/libexec/workcell/*.sh` support scripts are not supported operator
  entrypoints
- common git hook-bypass flags and inline hook-path overrides are blocked on the Tier 1 path
- repo-local control-plane files such as `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`,
  `.codex/`, `.claude/`, `.gemini/`, and mutable git hook/config paths are
  masked read-only on the safe path
- per-session home control-plane files are re-seeded on each provider launch
- `/tmp` is mounted `noexec`; `TMPDIR` points at ephemeral exec-capable state
  under `/state/tmp`
- on the `strict` profile, direct native ELF execution from mutable
  `/workspace` and `/state` paths is blocked, and mutable shebang scripts
  cannot point straight at protected real runtimes or loaders that target them
- nested coding-agent CLI launches are blocked or mediated on the safe path,
  and the public `node` surface blocks direct execution of the shipped provider
  entrypoints, repackaged workspace copies of the shipped provider package
  trees, and native addon loading instead of treating them as ordinary
  workspace code

## Scope

Supported tiers:

- Tier 1: CLI agents fully inside the dedicated runtime boundary
- Tier 2: GUI variants only when the GUI is a client to that same bounded
  runtime
- Tier 3: host-native GUI, cloud, or web guidance only; no claim of equivalent
  host-bound guarantees

Initial adapters target:

- Codex CLI now, with Codex app integration planned only after a bounded client
  path is implemented
- Claude Code CLI
- Gemini CLI
- GUI variants only with explicit downgrade notes when the boundary is weaker

## Quick start

```bash
./scripts/install.sh
workcell --prepare --agent codex --workspace /path/to/repo
workcell --agent codex --workspace /path/to/repo
workcell --agent claude --agent-autonomy prompt --workspace /path/to/repo
workcell --prepare --agent gemini --agent-arg --version --workspace /path/to/repo
man workcell
```

The safe path is `strict`, and it is also the default. `--prepare` is the
recommended first-run path: it seeds or refreshes the reviewed runtime image
inside the isolated VM, then continues with the requested launch. `build`
still exists as the explicit wider-egress runtime profile for dependency and
image creation work. `breakglass` is explicit, higher trust, visibly
different, and requires `--ack-breakglass`, but the managed in-container
entrypoint still does not auto-inject unsafe provider flags.
There is no default agent; pass `--agent codex`, `--agent claude`, or
`--agent gemini` explicitly on every launch.
Workcell launches the selected provider directly inside the bounded runtime.
There is no separate container attach or “start Codex and then connect to it”
step on the safe path.
Workcell does default the selected provider to no-prompt autonomy:
`--agent-autonomy yolo` is the default, and `--agent-autonomy prompt` is the
explicit opt-out when you want the provider’s ordinary approval flow.
Prompt autonomy is also a lower-assurance choice: provider-native approval
state or session-local policy amendments can change during the live session, so
Workcell surfaces that posture explicitly in the launch audit output.
`--agent-arg` is repeatable and appends provider-native argv at launch without
making you repeat `codex`, `claude`, or `gemini` yourself. `--agent-arg`
values are still treated as ordinary user-supplied provider argv and go
through the same in-container denylist as raw `-- ...` arguments.
`--injection-policy` lets you stage reviewed per-session inputs such as
org-wide instructions, persistent provider credentials, SSH material, and
read-only config files without passing through host homes or sockets. If
`~/.config/workcell/injection-policy.toml` exists, Workcell uses it by
default; `--no-default-injection-policy` disables that for a specific launch.
`--vm-cpu`, `--vm-memory`, and `--vm-disk` tune the dedicated Colima VM;
`--container-cpu` and `--container-memory` tune the inner runtime container
without changing the reviewed profile defaults for other launches.
With `--container-mutability ephemeral`, Workcell also allows `apt` and
`apt-get` to reach the pinned Debian snapshot endpoints so transient build
tooling can be installed without opening the session to arbitrary distro
mirrors. Successful package mutations are explicitly treated as a
lower-assurance in-session downgrade: maintainer scripts run as root inside the
mutable container, so Workcell warns, records the downgrade in runtime state,
and keeps that warning attached to later provider launches until the container
evaporates on exit.

By default, Workcell expects a git worktree and only forwards the selected
provider command through the bounded runtime. Use `--allow-nongit-workspace`
only for an intentional marker-based workspace. `--allow-arbitrary-command`
exists only for lower-assurance boundary debugging with
`--ack-arbitrary-command`; it bypasses the managed in-container entrypoint and
is recorded in the host audit log as a downgraded path.
The safe path requires self-contained git admin state inside the mounted
workspace; linked worktrees with external gitdirs are rejected.

Common recovery paths:

- First launch or missing reviewed runtime image:
  `workcell --prepare --agent codex --workspace /path/to/repo`
  Replace `codex` with `claude` or `gemini` for the corresponding provider.
- First launch with provider prompts enabled:
  `workcell --prepare --agent claude --agent-autonomy prompt --workspace /path/to/repo`
- One-off provider flags without repeating the provider command:
  `workcell --agent gemini --agent-arg --version --workspace /path/to/repo`
- Conflicting unmanaged Colima profile:
  `workcell --repair-profile --agent codex --workspace /path/to/repo`

`--repair-profile` deletes only the conflicting derived Colima profile so
Workcell can recreate it with reviewed mounts and policy. On `strict`, it also
acts like `--prepare`, because the recreated profile starts empty.

## Session injections

The supported way to place consistent material into every session is an
operator-owned injection policy, not ad hoc host passthrough.

Workcell treats injected content as three separate classes:

- common or provider-specific non-secret instructions that are rendered into
  provider-native home docs like `AGENTS.md`, `CLAUDE.md`, and `GEMINI.md`
- provider-native credentials mounted read-only from their original host paths
  and copied into the ephemeral session home at launch time
- copied files or directories placed into either `/state/injected/...` or a
  non-reserved path under the ephemeral session home. Public copies are staged
  through the launcher-owned bundle; secret copies are mounted read-only from
  their original host paths and copied in-session.
- SSH material mounted read-only from its original host paths and copied into
  the ephemeral in-container `~/.ssh` with strict permissions

The immutable adapter baselines under `adapters/` are never mutated in place.
Repo-local `AGENTS.md`, `CLAUDE.md`, and `GEMINI.md` are masked inside the
workspace on the safe path and imported into the provider-native home docs
instead. Managed provider policy such as Codex `rules/default.rules` stays
read-only inside the session.

Example policy:

```toml
version = 1

[documents]
common = "/Users/example/.config/workcell/common-agent.md"
claude = "/Users/example/.config/workcell/claude-extra.md"

[credentials]
codex_auth = "/Users/example/.codex/auth.json"
claude_auth = "/Users/example/.config/claude-code/auth.json"
claude_api_key = "/Users/example/.config/workcell/claude-api-key.txt"
claude_mcp = "/Users/example/.config/workcell/claude-mcp.json"
gemini_env = "/Users/example/.config/workcell/gemini.env"
gemini_projects = "/Users/example/.config/workcell/gemini-projects.json"
github_hosts = "/Users/example/.config/gh/hosts.yml"

[ssh]
enabled = true
config = "/Users/example/.ssh/config"
known_hosts = "/Users/example/.ssh/known_hosts"
identities = ["/Users/example/.ssh/id_workcell"]

[[copies]]
source = "/Users/example/.config/corp-ca.pem"
target = "/state/injected/corp-ca.pem"
classification = "public"

[[copies]]
source = "/Users/example/.config/workcell/repo-token.txt"
target = "~/.config/workcell/token.txt"
classification = "secret"
```

Use `[credentials]` for reusable provider or GitHub auth when you do not want
to log in on every launch. Workcell validates those host files, mounts them
read-only for the current session, and then copies them into the ephemeral
in-container home. Generic `[[copies]]` and `[ssh]` entries are still staged
through the launcher-owned injection bundle only for non-secret material. SSH
inputs and `classification = "secret"` copies use the same direct-mount model
as `[credentials]` and are copied into the session from their original host
paths.

Use it explicitly:

```bash
workcell --prepare --agent codex --workspace /path/to/repo \
  --injection-policy ~/.config/workcell/injection-policy.toml
```

Or keep it as the default:

```bash
mkdir -p ~/.config/workcell
$EDITOR ~/.config/workcell/injection-policy.toml
workcell --agent codex --workspace /path/to/repo
```

Intentional non-goals for the safe path:

- no host `SSH_AUTH_SOCK` forwarding
- no whole-home or provider-state passthrough
- no arbitrary environment-variable secret injection
- no writes back into the staged host bundle or immutable adapter baselines

## Repository layout

- `runtime/`: shared VM plus container boundary
- `policy/`: generic policy contracts and operator expectations
- `adapters/`: thin provider mappings for Codex, Claude, and Gemini
- `verify/`: invariant verification specs and harnesses
- `docs/`: threat model, assurance tiers, and provider matrix
- `workflows/`: launch, migration, and downgrade-path notes
- `scripts/`: installers, launchers, and verification entrypoints

## Verification

- `scripts/validate-repo.sh`: repo-local shell, JSON, TOML, YAML, and manpage
  checks
- `scripts/check-workflows.sh`: `actionlint` and `zizmor`
- `scripts/check-pinned-inputs.sh`: pin-policy checks for non-ecosystem release
  inputs such as the Debian snapshot, immutable base-image digests, and exact
  runtime and validator package sets, pinned BuildKit, QEMU, Cosign, and Syft
  workflow inputs, plus integrity validation for the committed provider
  lockfile graph
- `scripts/verify-upstream-codex-release.sh`: re-verifies the pinned Codex
  release assets against OpenAI's published Sigstore bundle
- `scripts/verify-build-input-manifest.sh`: deterministic local verification
  for the release build-input manifest generator
- `scripts/container-smoke.sh`: direct container build and adapter smoke tests
- `scripts/verify-invariants.sh`: local invariant regression checks against the
  launcher and shipped adapter policy
- `scripts/verify-reproducible-build.sh`: deterministic per-platform OCI
  runtime export verification under a fixed `SOURCE_DATE_EPOCH`
- `scripts/verify-release-bundle.sh`: deterministic source bundle verification
  under a fixed `SOURCE_DATE_EPOCH`
- `scripts/dev-remote-validate.sh`: stage the current working tree to a remote
  amd64 builder such as `builder@bewear`, build an ephemeral remote helper
  container there from `tools/remote-validator/Dockerfile`, and run
  `validate-repo`, `container-smoke`,
  `verify-reproducible-build`, and `verify-release-bundle` inside that
  container as a single pre-push preflight. The default remote snapshot is the
  local index; `--snapshot worktree` and `--include-untracked` are explicit
  opt-ins. The remote reproducibility lane is intentionally native
  `linux/amd64` only; the canonical multi-architecture release gate remains the
  local macOS plus GitHub release path.

Full Colima plus Virtualization.Framework boundary verification remains a local
or self-hosted macOS exercise. GitHub-hosted CI validates the repo shape and
container path, not the full host boundary.

Recommended split:

- local macOS: `scripts/verify-invariants.sh`, launcher UX work, and any
  Colima or Virtualization.Framework debugging
- remote amd64 builder: `./scripts/dev-remote-validate.sh`
  This path stages the selected snapshot to a root-owned remote temp workspace
  when `sudo` is available, then runs the heavy checks inside an ephemeral
  helper container on that host.
  This is still a lower-assurance trusted-builder path because the helper
  container talks to the remote host Docker daemon. Treat it as a performance
  and parity aid, not as a provenance or multi-tenant isolation boundary.
- GitHub CI and release: final policy, signing, attestations, and publication

Each real launch appends a durable audit record under the managed Colima profile
directory with the workspace, runtime profile, network mode, selected adapter,
and whether the run stayed on the managed Tier 1 path or used an explicitly
lower-assurance mode. `build` sessions additionally record when the temporary
bootstrap allowlist was applied for image creation.

## Release posture

Release automation publishes a multi-arch runtime image, SBOMs, checksums, and
signed provenance materials. Tagged releases are revalidated before signing and
publishing. Releases publish the final multi-arch image by first pushing pinned
single-platform manifests, then assembling the published manifest list in a
fixed `amd64` then `arm64` order. Releases also publish directly signed source
bundle, build-input, builder-environment, and SBOM artifacts. The signed
checksum manifest additionally covers the source bundle, published image digest,
build input manifest, builder environment manifest, and both release SBOMs even
when GitHub attestations are not enabled. The build-input manifest covers the
runtime image build context that the Dockerfile actually consumes, plus the
release and verification inputs that gate publication. See
`docs/github-workflows.md` and `docs/provenance.md`.

## Implementation goals

- make the safe path the default path
- preserve normal coding ergonomics across supported providers
- make `breakglass` mode explicit, acknowledged, and externally sandboxed
- ship invariant tests with the runtime, not as a follow-up
- document any lower-assurance modes rather than implying parity

## License

Workcell is licensed under Apache-2.0. See `LICENSE`.
