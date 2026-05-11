# Codex Adapter

The Codex adapter maps the shared Workcell boundary into Codex-native controls.
Adapter layout (paths relative to `adapters/codex/`):

- `.codex/config.toml`: managed Codex configuration template seeded into the
  session-local Codex home as `~/.codex/config.toml`
- `.codex/AGENTS.md`: managed agent guidance seeded into the session-local
  Codex home
- `.codex/rules/`: managed execpolicy rules seeded into the session-local
  Codex home
- `.codex/agents/`: managed sub-agent guidance
- `managed_config.toml`: workcell-side managed-mode TOML consumed by the
  launcher
- `requirements.toml`: workcell-side adapter requirements contract
- `mcp/config.toml`: MCP server config

Workcell re-seeds this state into the session-local Codex home on launch.
Repo-local control files stay masked on the safe path and are imported only as
reviewed layers.

Codex's own Linux `workspace-write` sandbox is pinned off on the managed
Workcell path. Workcell already provides the outer VM-plus-container boundary,
and the current Codex Linux sandbox depends on unprivileged user namespaces
that are unavailable inside the Tier 1 container.

Final branch publication stays on the host through `workcell publish-pr`, not
from inside the container session.

Codex CLI is Tier 1 when it runs fully inside the bounded runtime. App support
is only valid once it becomes a client of the same bounded executor.
