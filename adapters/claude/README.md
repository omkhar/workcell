# Claude Adapter

This adapter maps the shared Workcell runtime boundary into Claude Code's
native control surface:

- `~/.claude/settings.json`
- `CLAUDE.md`
- `.mcp.json`
- optional hooks as guardrails

Claude hooks are defense in depth, not the security boundary.

The strict baseline seeds an empty `.mcp.json`. Networked or package-fetching
MCP servers must be opted into explicitly in a broader runtime mode.
Reviewed Claude auth and MCP state can be injected explicitly with
`credentials.claude_auth` and `credentials.claude_mcp`.

Claude CLI is Tier 1 when it runs inside the shared bounded runtime. GUI or IDE
surfaces are lower assurance unless they are attached to that same bounded
runtime rather than executing on the host.
