# Provider Matrix

`Workcell` keeps one shared runtime boundary and exposes thin adapters for
provider-native control planes.

## Summary

The runtime boundary is shared. The adapter layer changes by provider.

CLI adapters are the primary Tier 1 target because they can stay fully inside
the bounded runtime. GUI adapters are Tier 2 only when they are clients to that
same runtime, not host-native executors.

## Matrix

| Provider | Primary Tier 1 surface | Native control plane | Boundary fit | Notes |
|---|---|---|---|---|
| Codex | CLI | `~/.codex/config.toml`, `AGENTS.md`, `.rules`, MCP config | Clean | Best fit for runtime-plus-adapter design |
| Codex | App / GUI | app plus `app-server` | Partial | Tier 2 only when execution stays in the bounded runtime |
| Claude | Claude Code CLI | `~/.claude/settings.json`, imported `CLAUDE.md`, `.mcp.json`, hooks | Partial | Hooks are guardrails, not the boundary; reviewed `.mcp.json` and auth can be injected explicitly |
| Claude | IDE / GUI workflows | IDE and host integrations | Partial | Lower assurance unless attached to the same bounded workspace/runtime |
| Gemini | Gemini CLI | `~/.gemini/settings.json`, imported `GEMINI.md`, injected `projects.json` | Partial | Internal sandbox is not the primary boundary here; repo `.gemini/` stays masked on the safe path |
| Gemini | IDE / GUI workflows | IDE integration and host UI surfaces | Partial | Tier 2 only if execution is still fully remote/containerized |

## Adapter rule

Do not force one provider's control model onto another. Keep:

1. one shared runtime boundary
2. one shared invariant set
3. one provider adapter per product
