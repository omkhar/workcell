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

Each adapter directory has an `adapter.toml` manifest (strict schema and
loader: `internal/adapters/manifest.go`). The manifest is the source of the
per-provider credential keys, container paths, reserved targets, and egress
endpoints. Run the four `scripts/generate-adapters-*.sh` generators to
generate `internal/adapters/data_gen.go`,
`internal/providerid/providerid_gen.go`,
`scripts/lib/launcher/generated-adapters.sh`, and
`runtime/container/generated-adapters.sh` from the manifests. Do not edit the
generated files by hand. Also add the provider configuration tree under
`adapters/<name>/`.

These registry changes do not make a provider supported. Support also requires
launcher, auth, policy, tests, documents, and live certification. A provider
directory with a planned manifest is a fail-closed scaffold.

The golden test in `internal/adapters/gen_test.go` compares the generated
files with the generator output. The parity test in
`internal/adapters/manifest_test.go` compares each manifest with the generated
tables, the launcher `--agent` dispatch, and the Rust launcher table.

The file `internal/adapters/adapters.go` contains the public API. Injection,
policy, and runtime code use this API.

See [`../docs/extending-adapters.md`](../docs/extending-adapters.md) for worked
examples. Each example identifies its related invariants and threats.
The porting checklist is in
[`../workflows/adapter-porting.md`](../workflows/adapter-porting.md).
