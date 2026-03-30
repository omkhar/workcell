# Codex Adapter

The Codex adapter maps the shared Workcell boundary into Codex-native controls:

- `~/.codex/config.toml`
- `managed_config.toml`
- `requirements.toml`
- `AGENTS.md`
- rules
- MCP config

Workcell re-seeds this state into the session-local Codex home on launch.
Repo-local control files stay masked on the safe path and are imported only as
reviewed layers.

Final branch publication stays on the host through `workcell publish-pr`, not
from inside the container session.

Codex CLI is Tier 1 when it runs fully inside the bounded runtime. App support
is only valid once it becomes a client of the same bounded executor.
