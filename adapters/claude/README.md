# Claude Adapter

The Claude adapter maps the shared Workcell runtime into Claude Code's native
files and guardrails.

Managed surfaces:

- `~/.claude/settings.json`
- rendered `~/.claude/CLAUDE.md`
- `~/.mcp.json`
- Claude auth compatibility files when injected
- a reviewed `PreToolUse` Bash hook as defense in depth

Key points:

- hooks are guardrails, not the security boundary
- the default MCP template is empty
- `claude_api_key`, `claude_auth`, and `claude_mcp` are the supported
  credential/injection paths
- GUI or IDE use is lower assurance unless it is only a client to the same
  bounded runtime
