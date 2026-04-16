# Provider Matrix

Workcell keeps one shared runtime boundary and adapts each provider into it
through a native control-plane mapping.

## Current support

| Provider | Tier 1 surface today | Managed control plane | Long-lived auth inputs | Notes |
|---|---|---|---|---|
| Codex | CLI | `~/.codex/config.toml`, `managed_config.toml`, `requirements.toml`, rules, MCP config, rendered `AGENTS.md` | `codex_auth` | best fit for the shared boundary model |
| Claude | Claude Code CLI | `~/.claude/settings.json`, rendered `CLAUDE.md`, `.mcp.json`, auth mirrors, reviewed Bash hook | `claude_auth`, `claude_api_key`, `claude_mcp` | direct staged `claude_auth` and `claude_api_key` are supported; the built-in macOS resolver scaffold remains fail-closed |
| Gemini | Gemini CLI | `~/.gemini/settings.json`, rendered `GEMINI.md`, `.env`, OAuth creds, `projects.json`, trusted folders | `gemini_env`, `gemini_oauth`, `gemini_projects`, `gcloud_adc` | Gemini's own sandbox is not the Tier 1 boundary here; `gcloud_adc` is supplemental to Vertex config |

For provider auth maturity and rollout caveats, see
[docs/injection-policy.md](injection-policy.md).

## Tiering rule

- Tier 1: provider CLI runs fully inside the bounded runtime
- Tier 2: GUI or IDE surface is only a client to that same bounded runtime
- Tier 3: host-native GUI, cloud, or web-only guidance with no claim of
  equivalent local isolation

Do not force one provider's control model onto another. Keep one shared
boundary and one thin adapter per product.
