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
workcell --agent gemini --agent-arg --version --workspace /path/to/repo
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
Workcell does default the selected provider to no-prompt autonomy:
`--agent-autonomy yolo` is the default, and `--agent-autonomy prompt` is the
explicit opt-out when you want the provider’s ordinary approval flow.
`--agent-arg` is repeatable and appends provider-native argv at launch without
making you repeat `codex`, `claude`, or `gemini` yourself. `--agent-arg`
values are still treated as ordinary user-supplied provider argv and go
through the same in-container denylist as raw `-- ...` arguments.

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
- First launch with provider prompts enabled:
  `workcell --prepare --agent claude --agent-autonomy prompt --workspace /path/to/repo`
- One-off provider flags without repeating the provider command:
  `workcell --agent gemini --agent-arg --version --workspace /path/to/repo`
- Conflicting unmanaged Colima profile:
  `workcell --repair-profile --prepare --agent codex --workspace /path/to/repo`

`--repair-profile` deletes only the conflicting derived Colima profile so
Workcell can recreate it with reviewed mounts and policy. On `strict`, it also
acts like `--prepare`, because the recreated profile starts empty.

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

Full Colima plus Virtualization.Framework boundary verification remains a local
or self-hosted macOS exercise. GitHub-hosted CI validates the repo shape and
container path, not the full host boundary.

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
