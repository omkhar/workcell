# Enterprise Claude Setup with Workcell

This pattern is for teams that want a shared Claude baseline without mounting
whole host homes into the runtime.

It assumes supported Apple Silicon macOS hosts and the current local-first
Workcell product shape. Workcell does not yet ship a centralized enterprise
policy or session-administration plane; teams distribute reviewed host-side
files through their existing host configuration workflow today.

## Recommended split

Keep these concerns separate:

- org-wide instructions in `documents.common` or `documents.claude`
- reviewed Claude API-key access in `credentials.claude_api_key`
- reviewed MCP state in `credentials.claude_mcp`
- optional fail-closed macOS resolver scaffold in `credentials.claude_auth`

## Example policy

```toml
version = 1

[documents]
common = "/Users/example/.config/workcell/org-agent.md"
claude = "/Users/example/.config/workcell/claude-overlay.md"

[credentials.claude_api_key]
source = "/Users/example/.config/workcell/claude-api-key.txt"

[credentials.claude_mcp]
source = "/Users/example/.config/workcell/claude-mcp.json"

[credentials.github_hosts]
source = "/Users/example/.config/gh/hosts.yml"
providers = ["claude"]
```

## Why this pattern works

- the reviewed adapter baseline still controls Claude's default settings
- workspace `CLAUDE.md` is imported as a layer instead of becoming the live
  control plane
- the session gets only the reviewed credentials it needs
- the macOS Claude resolver can still be recorded separately, but it is
  currently fail-closed until a supported export path exists
- final publication still stays on the host with `workcell publish-pr`

## Launch

```bash
workcell --prepare --agent claude --workspace /path/to/repo
```

## Related docs

- [docs/injection-policy.md](../injection-policy.md)
- [docs/adapter-control-planes.md](../adapter-control-planes.md)
- [docs/enterprise-rollout.md](../enterprise-rollout.md)
- [docs/requirements-validation.md](../requirements-validation.md)
- [docs/validation-scenarios.md](../validation-scenarios.md)
