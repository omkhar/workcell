# Workcell

`Workcell` is a bounded local runtime for coding agents. It keeps the active
agent inside a dedicated VM plus hardened container boundary and exposes thin
provider adapters instead of pretending every agent product shares one control
plane.

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
- common git hook-bypass flags and inline hook-path overrides are blocked on the Tier 1 path

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
workcell --agent codex --mode strict --workspace /path/to/repo
man workcell
```

The safe path is `strict`. `build` widens network access for dependency and
build traffic. `breakglass` is explicit, higher trust, and visibly different.

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
- `scripts/container-smoke.sh`: direct container build and adapter smoke tests
- `scripts/verify-invariants.sh`: local invariant regression checks against the
  launcher and Codex policy

Full Colima plus Virtualization.Framework boundary verification remains a local
or self-hosted macOS exercise. GitHub-hosted CI validates the repo shape and
container path, not the full host boundary.

## Release posture

Release automation publishes a multi-arch runtime image, SBOMs, checksums, and
signed provenance materials. Tagged releases are revalidated before signing and
publishing. See `docs/github-workflows.md` and `docs/provenance.md`.

## Implementation goals

- make the safe path the default path
- preserve normal coding ergonomics across supported providers
- make `breakglass` mode explicit and externally sandboxed
- ship invariant tests with the runtime, not as a follow-up
- document any lower-assurance modes rather than implying parity
