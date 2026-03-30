# Provider Adapters

Each adapter maps the shared Workcell runtime into one provider's native
control plane.

Current adapters:

- `codex/`
- `claude/`
- `gemini/`

Adapter rules:

- keep the adapter thin
- prefer native provider config over wrapper-only policy
- do not claim the adapter is the primary boundary
- keep lower-assurance GUI or IDE paths clearly separate from Tier 1 CLI paths
