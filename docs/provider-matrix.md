# Provider Matrix

Workcell keeps one shared runtime boundary and adapts each provider into it
through a native control-plane mapping.

## Current support

| Provider | Tier 1 surface today | Managed control plane | Long-lived auth inputs | Notes |
|---|---|---|---|---|
| Codex | CLI | `~/.codex/config.toml`, `managed_config.toml`, `requirements.toml`, rules, MCP config, rendered `AGENTS.md` | `codex_auth` | direct staged `codex_auth` and `codex-home-auth-file` host reuse are supported |
| Claude | Claude Code CLI | `~/.claude/settings.json`, rendered `CLAUDE.md`, `.mcp.json`, auth mirrors, reviewed Bash hook | `claude_auth`, `claude_api_key`, `claude_mcp` | direct staged `claude_auth` and `claude_api_key` are supported; the built-in macOS resolver scaffold remains fail-closed |
| Gemini | Gemini CLI | `~/.gemini/settings.json`, rendered `GEMINI.md`, `.env`, OAuth creds, `projects.json`, trusted folders | `gemini_env`, `gemini_oauth`, `gemini_projects`, `gcloud_adc` | Gemini's own sandbox is not the Tier 1 boundary here; `gcloud_adc` is supplemental to Vertex config |

### Upstream change: Gemini CLI retirement on June 18, 2026

Google has announced that Gemini CLI stops serving requests for the free,
Pro, and Ultra personal-account login tiers on June 18, 2026, in favor of
the closed-source Antigravity CLI. Access continues for Gemini Code Assist
Standard/Enterprise licenses and for billed Gemini API-key and Vertex auth
([announcement](https://developers.googleblog.com/an-important-update-transitioning-gemini-cli-to-antigravity-cli/)).
**Reviewed posture: the Gemini Tier 1 adapter stays shipped and supported
for the auth inputs Google keeps serving — `gemini_env`/`gemini_oauth` with
a Code Assist Standard/Enterprise license, and billed API-key or
`gcloud_adc`/Vertex inputs.** The free, Pro, and Ultra personal-account
OAuth login is what upstream retires; those accounts are refused by Google,
not by Workcell, while the adapter, control-plane mapping, and pinned CLI
remain intact. An Antigravity adapter is
a committed follow-on provider-parity track with a different binary and
control-plane surface, following the same Tier 1 evidence bar as every
provider; sequencing is tracked in [ROADMAP.md](../ROADMAP.md).

## Planned provider parity

| Provider | Planned Tier 1 surface | Planned managed control plane | Planned auth input | Support status |
|---|---|---|---|---|
| GitHub Copilot CLI | `workcell --agent copilot --workspace ...` | session-local `COPILOT_HOME`, `COPILOT_CACHE_HOME`, `~/.copilot` config, permissions, sessions, logs, plugins, hooks, MCP/LSP state, and reviewed instruction imports | explicit staged token such as `copilot_github_token`, exported only to the managed child as `COPILOT_GITHUB_TOKEN` | fail-closed scaffold; not current support |

The current sequencing plan for the Copilot parity track is
[docs/copilot-linux-local-compat-plan.md](copilot-linux-local-compat-plan.md).
That page is planning-only and does not change the supported provider set.

Copilot support must land with the same evidence bar as the current providers:
adapter baseline, auth/bootstrap explainability, unsafe-argument rejection,
workspace control-plane masking, quickstart, scenario coverage, and live
provider certification. Host `~/.copilot`, host keychains, `GH_TOKEN`,
`GITHUB_TOKEN`, ambient GitHub CLI auth, and whole-home passthrough are not
acceptable Tier 1 inputs.

GitHub documents Copilot CLI as a terminal agent with interactive and
programmatic modes, environment-token auth, configurable `COPILOT_HOME`, and
permissive tool flags such as `--allow-all` and `--yolo`. Workcell treats
those product surfaces as implementation inputs that must be mapped or blocked
before support is claimed.

For provider auth maturity and rollout caveats, see
[docs/injection-policy.md](injection-policy.md) and
[docs/provider-bootstrap-matrix.md](provider-bootstrap-matrix.md).

## Tiering rule

- Tier 1: provider CLI runs fully inside the bounded runtime
- Tier 2: GUI or IDE surface is only a client to that same bounded runtime
- Tier 3: host-native GUI, cloud, or web-only guidance with no claim of
  equivalent local isolation

Copilot cloud agent, IDE extensions, and host-native Copilot CLI execution are
Tier 3 unless a future integration makes them clients of the same bounded
Workcell session plane. The planned support target is the local Copilot CLI
adapter running inside Tier 1.

Do not force one provider's control model onto another. Keep one shared
boundary and one thin adapter per product.

## Validation traceability

Use [`policy/operator-contract.toml`](../policy/operator-contract.toml) for the
supported operator workflow surface, [docs/requirements-validation.md](requirements-validation.md)
for the machine-checked requirement and evidence mapping, and
[docs/validation-scenarios.md](validation-scenarios.md) for the concrete
scenario and script anchors behind the auth and control-plane caveats.

The Tier 1, 2, and 3 rule is a support classification. It is not a claim that
GUI, IDE, or cloud paths receive the same validation depth as the Tier 1 CLI
path.
