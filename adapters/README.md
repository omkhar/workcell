# Provider Adapters

Each adapter maps the shared runtime and invariant model into a provider's
native control surface.

Current targets:

- `codex/`
- `claude/`
- `gemini/`

Adapter rules:

- keep the adapter thin
- prefer native provider configuration over wrapper-only policy
- never claim the adapter is the primary boundary
- keep CLI and GUI assurance tiers separate
