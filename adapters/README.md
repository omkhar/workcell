# Provider Adapters

Each adapter maps the shared Workcell runtime into one provider's native
control plane.

Current adapters (each README covers auth methods, managed control-plane files,
and behavior):

- [`codex/`](codex/README.md)
- [`claude/`](claude/README.md)
- [`copilot/`](copilot/README.md)
- [`gemini/`](gemini/README.md)

Planned fail-closed scaffolds:

- [`antigravity/`](antigravity/README.md)

## Common adapter contract

Every supported adapter follows the same shape:

- one shared runtime VM-plus-container boundary; the adapter is thin and its
  provider config is defense in depth, not the boundary
- a session-local provider home rebuilt each launch from immutable baselines
  under `adapters/<name>/`, explicit injection-policy inputs, and masked
  workspace imports (`runtime/container/home-control-plane.sh`)
- explicit credential keys only — no host home, keychain, socket, or ambient CLI
  auth passthrough (see [`../docs/injection-policy.md`](../docs/injection-policy.md)
  and [`../docs/invariants.md`](../docs/invariants.md))
- provider-native unsafe-flag rejection in the wrapper
  (`runtime/container/provider-policy.sh`), exempted only by `breakglass`

The cross-adapter mapping tables live in
[`../docs/adapter-control-planes.md`](../docs/adapter-control-planes.md).

Adapter rules:

- keep the adapter thin
- prefer native provider config over wrapper-only policy
- do not claim the adapter is the primary boundary
- keep lower-assurance GUI or IDE paths clearly separate from Tier 1 CLI paths

## Adding a new provider

Per-provider Go tables (credential keys, container paths, reserved
targets) live in `internal/adapters/data.go` — promoting a planned provider is
a single row append to the `providers` slice plus the per-provider config tree
under `adapters/<name>/`. There is no longer a per-provider Go sub-package; see
`internal/adapters/adapters.go` for the public API that the injection, policy,
and runtime paths consume. A provider directory without a registry row is only a
fail-closed scaffold.

For step-by-step worked examples — adding a credential type and extending or
adding an adapter, each annotated with the invariants and threat-model items it
touches — see [`../docs/extending-adapters.md`](../docs/extending-adapters.md).
The porting checklist is in
[`../workflows/adapter-porting.md`](../workflows/adapter-porting.md).
