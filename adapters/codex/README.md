# Codex Adapter

This adapter maps the shared Workcell runtime boundary into Codex-native
controls:

- `~/.codex/config.toml`
- `AGENTS.md`
- `.rules`
- managed configuration
- MCP policy/templates with no live defaults

CLI is Tier 1 when it runs fully inside the bounded runtime.

Codex app support is planned Tier 2 work. It is only valid once the app is
acting as a client to the same bounded executor rather than running on the
host.
