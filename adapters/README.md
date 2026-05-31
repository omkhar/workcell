# Provider Adapters

Each adapter maps the shared Workcell runtime into one provider's native
control plane.

Current adapters:

- `codex/`
- `claude/`
- `gemini/`

Planned fail-closed scaffolds:

- `copilot/`

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
