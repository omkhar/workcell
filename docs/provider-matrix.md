# Provider Matrix

Workcell keeps one shared runtime boundary and adapts each provider into it
through a native control-plane mapping.

## Current support

| Provider | Tier 1 surface | Managed control plane | Staged inputs | Notes |
|---|---|---|---|---|
| Codex | CLI | `~/.codex/config.toml`, `managed_config.toml`, `requirements.toml`, rules, MCP config, rendered `AGENTS.md` | `codex_auth` | Workcell supports directly staged `codex_auth` and `codex-home-auth-file` host reuse. |
| Claude | Claude Code CLI | `~/.claude/settings.json`, rendered `CLAUDE.md`, `.mcp.json`, auth mirrors, reviewed Bash hook | `claude_auth`, `claude_api_key`, `claude_mcp` | Workcell supports directly staged `claude_auth` and `claude_api_key`. The built-in macOS resolver fails closed. |
| GitHub Copilot CLI | CLI | Workcell-owned Copilot home, cache, and policy controls | `copilot_github_token` | Workcell supports a directly staged `copilot_github_token`. See the [GitHub Copilot CLI Delivery Record](copilot-linux-local-compat-plan.md). |
| Gemini | Gemini CLI | `~/.gemini/settings.json`, rendered `GEMINI.md`, `.env`, OAuth creds, `projects.json`, trusted folders | `gemini_env`, `gemini_oauth`, `gemini_projects`, `gcloud_adc` | Gemini's own sandbox is not the Tier 1 boundary here; `gcloud_adc` is supplemental to Vertex config |

### Gemini CLI account change

On June 18, 2026, Gemini CLI stopped service for free, Pro, and Ultra personal
accounts. Gemini CLI access remains available through Gemini Code Assist
Standard and Enterprise licenses. It also remains available through paid Gemini
and Gemini Enterprise Agent Platform API keys. See the
[Google announcement](https://developers.googleblog.com/an-important-update-transitioning-gemini-cli-to-antigravity-cli/).

The Gemini Tier 1 adapter supports these auth paths. Use
`gemini_oauth` only for reviewed cached OAuth state. Use `gemini_env` for
Gemini Code Assist (GCA), a paid API key, or Vertex environment configuration.
Use `gcloud_adc` only as a Vertex supplement. It is not a standalone auth path.
Google, not Workcell, refuses the retired account types.

## Unsupported provider scaffold

| Provider | Target Tier 1 surface | Required control plane | Required auth input | Support status |
|---|---|---|---|---|
| Google Antigravity CLI | `workcell --agent antigravity --workspace ...` | Workcell must map or block settings, permissions, subagents, plugins, MCP, hooks, and instructions. | Workcell must define the exact staged credential names. | `unsupported`; Workcell has not pinned official CLI provenance. |

The Copilot delivery record at
[docs/copilot-linux-local-compat-plan.md](copilot-linux-local-compat-plan.md)
describes the shipped controls. The current support boundary is the table above
and the quickstart in
[docs/examples/quickstart-copilot.md](examples/quickstart-copilot.md).

GitHub Copilot CLI has interactive and programmatic modes. It accepts an
environment token and a configurable `COPILOT_HOME`. It also has permissive
tool flags. The Workcell adapter maps or blocks these surfaces.

Before you sign a Copilot support-claim change, complete the live certification
in the Copilot delivery record.

Antigravity remains unsupported and fails closed. Before support, Workcell must
add a pinned install path, explicit auth, an adapter, tests, and live
certification.

For provider auth maturity and rollout caveats, see
[docs/injection-policy.md](injection-policy.md) and
[docs/provider-bootstrap-matrix.md](provider-bootstrap-matrix.md).

## Tier Rules

- A Tier 1 provider CLI runs fully inside the bounded runtime.
- A Tier 2 GUI or IDE is a client of the bounded runtime.
- A Tier 3 surface runs outside the bounded runtime. Workcell makes no
  equivalent local isolation claim.

Copilot cloud agent and Copilot IDE extensions are Tier 3. Antigravity desktop
and IDE surfaces are also Tier 3. Host-native provider CLIs are Tier 3. Only a
reviewed client of the bounded runtime can qualify for Tier 2.

Do not force one provider's control model onto another. Keep one shared
boundary and one thin adapter per product.

## Validation traceability

Use [`policy/operator-contract.toml`](../policy/operator-contract.toml) for the
operator workflow. Use
[requirements-validation.md](requirements-validation.md) for requirement
evidence. Use [validation-scenarios.md](validation-scenarios.md) for scenario
and script references.

The Tier 1, 2, and 3 rule is a support classification. It is not a claim that
GUI, IDE, or cloud paths receive the same validation depth as the Tier 1 CLI
path.
