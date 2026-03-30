# Enterprise Claude Setup with Workcell

This pattern is for teams that want a shared Claude baseline without mounting
whole host homes into the runtime.

## Recommended split

Keep these concerns separate:

- org-wide instructions in `documents.common` or `documents.claude`
- reviewed persisted Claude auth in `credentials.claude_auth`
- reviewed MCP state in `credentials.claude_mcp`
- optional API-key fallback in `credentials.claude_api_key`

## Example policy

```toml
version = 1

[documents]
common = "/Users/example/.config/workcell/org-agent.md"
claude = "/Users/example/.config/workcell/claude-overlay.md"

[credentials]
claude_auth = "/Users/example/.claude.json"
claude_mcp = "/Users/example/.config/workcell/claude-mcp.json"

[credentials.github_hosts]
source = "/Users/example/.config/gh/hosts.yml"
providers = ["claude"]
```

## Why this pattern works

- the reviewed adapter baseline still controls Claude's default settings
- workspace `CLAUDE.md` is imported as a layer instead of becoming the live
  control plane
- the session gets only the reviewed credentials it needs
- final publication still stays on the host with `workcell publish-pr`

## Launch

```bash
workcell --prepare --agent claude --workspace /path/to/repo
```

## Related docs

- [docs/injection-policy.md](../injection-policy.md)
- [docs/adapter-control-planes.md](../adapter-control-planes.md)
