# Provider Quickstarts

| Provider | Tier 1 surface today | Native control plane | Quickstart |
|---|---|---|---|
| Codex | CLI | `~/.codex/config.toml`, `AGENTS.md`, rules, MCP config | [examples/quickstart-codex.md](examples/quickstart-codex.md) |
| Claude | Claude Code CLI | `~/.claude/settings.json`, `CLAUDE.md`, `.mcp.json`, auth mirrors, hooks, host-side macOS auth resolver scaffold | [examples/quickstart-claude.md](examples/quickstart-claude.md) |
| GitHub Copilot CLI | CLI | session-local `COPILOT_HOME`, `COPILOT_CACHE_HOME`, token handoff, custom instructions disabled, skill/dynamic-retrieval overrides blocked | [examples/quickstart-copilot.md](examples/quickstart-copilot.md) |
| Gemini | Gemini CLI | `~/.gemini/settings.json`, `GEMINI.md`, `.env`, `projects.json` | [examples/quickstart-gemini.md](examples/quickstart-gemini.md) |

Planned provider parity:

| Provider | Target surface | Required before support |
|---|---|---|
| Google Antigravity CLI | planned fail-closed Tier 1 CLI adapter; not current support | `--agent antigravity`, pinned official install/auth provenance, explicit Google auth staging, session-local provider home/cache, unsafe-argument policy, quickstart, deterministic tests, and live provider certification |

GUI and IDE surfaces are lower assurance unless they act only as clients to
the same bounded runtime.

See [injection-policy.md](injection-policy.md) for provider auth
maturity and [enterprise-rollout.md](enterprise-rollout.md) for the
current team rollout model.
