# Codex Adapter

This adapter maps the shared Workcell runtime boundary into Codex-native
controls:

- `~/.codex/config.toml`
- `AGENTS.md`
- `.rules`
- managed configuration
- MCP policy/templates with no live defaults

Workcell re-seeds these controls into the session-local Codex home on each
launch. Use the host `workcell` launcher to select the runtime profile,
autonomy mode, and injection policy; provider-native overrides that widen trust
are blocked inside the container rather than delegated to repo-local state.

For first-run and operator flows, start with the top-level `README.md` quick
start. For the full control-plane mapping, see
`docs/adapter-control-planes.md`.

CLI is Tier 1 when it runs fully inside the bounded runtime.

Codex app support is planned Tier 2 work. It is only valid once the app is
acting as a client to the same bounded executor rather than running on the
host.
