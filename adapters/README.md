# Provider Adapters

Each adapter maps the shared Workcell runtime into one provider's native
control plane.

Current adapters (each README covers auth methods, managed control-plane files,
and behavior):

- [`codex/`](codex/README.md)
- [`claude/`](claude/README.md)
- [`copilot/`](copilot/README.md)
- [`gemini/`](gemini/README.md)

Unsupported fail-closed scaffolds:

- [`antigravity/`](antigravity/README.md)

## Common adapter contract

Every supported adapter has these properties:

- One VM and container runtime boundary is common to all adapters.
- The adapter is thin. Provider configuration is not the runtime boundary.
- Each start builds a session-local provider home from immutable baselines and
  explicit inputs.
- Each adapter accepts only explicit credential keys.
- It does not pass host homes, keychains, sockets, or ambient CLI auth.
- The wrapper rejects provider unsafe flags in every mode.
- Breakglass changes the container posture. It does not change the provider
  unsafe-flag policy.

The cross-adapter mapping tables live in
[`../docs/adapter-control-planes.md`](../docs/adapter-control-planes.md).

Adapter rules:

- keep the adapter thin
- prefer native provider config over wrapper-only policy
- do not claim the adapter is the primary boundary
- keep lower-assurance GUI or IDE paths clearly separate from Tier 1 CLI paths

## Adding a new provider

Per-provider Go tables contain credential keys, container paths, and reserved
targets. These tables are in `internal/adapters/data.go`. Add a row when you
implement an adapter. The row must match `providerid.CredentialMetadataProviders`
order. Also add the provider configuration tree under `adapters/<name>/`.

These registry changes do not make a provider supported. Support also requires
launcher, auth, policy, tests, documents, and live certification. A provider
directory without a registry row is a fail-closed scaffold.

The file `internal/adapters/adapters.go` contains the public API. Injection,
policy, and runtime code use this API.

See [`../docs/extending-adapters.md`](../docs/extending-adapters.md) for worked
examples. Each example identifies its related invariants and threats.
The porting checklist is in
[`../workflows/adapter-porting.md`](../workflows/adapter-porting.md).
