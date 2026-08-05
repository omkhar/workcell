# Provider Matrix

Workcell keeps one shared runtime boundary and adapts each provider into it
through a native control-plane mapping.

## Current support

| Provider | Tier 1 surface today | Managed control plane | Long-lived auth inputs | Notes |
|---|---|---|---|---|
| Codex | CLI | `~/.codex/config.toml`, `managed_config.toml`, `requirements.toml`, rules, MCP config, rendered `AGENTS.md` | `codex_auth` | direct staged `codex_auth` and `codex-home-auth-file` host reuse are supported |
| Claude | Claude Code CLI | `~/.claude/settings.json`, rendered `CLAUDE.md`, `.mcp.json`, auth mirrors, reviewed Bash hook | `claude_auth`, `claude_api_key`, `claude_mcp` | direct staged `claude_auth` and `claude_api_key` are supported; the built-in macOS resolver scaffold remains fail-closed |
| GitHub Copilot CLI | CLI | Workcell-owned session-local `COPILOT_HOME`, `COPILOT_CACHE_HOME`, and GitHub Copilot config/cache directories, host-mounted token handoff plus transient runtime handoff, logs, cache state, custom instructions disabled, and skill/dynamic-retrieval overrides blocked | `copilot_github_token` | supported Copilot token credential: a directly staged `copilot_github_token`; Workcell removes the token file and staged direct-mount copy from direct runtime mounts, passes a temporary handoff mount outside provider state, runs the Workcell entrypoint as PID 1 for token handoff launches, exports it as `COPILOT_GITHUB_TOKEN` only to the managed Copilot child after unlinking the runtime handoff file, and does not use host `gh` auth, host keychains, `GH_TOKEN`, `GITHUB_TOKEN`, or host Copilot provider state (`~/.copilot`, `~/.config/github-copilot`, `~/.cache/github-copilot`) |
| Gemini | Gemini CLI | `~/.gemini/settings.json`, rendered `GEMINI.md`, `.env`, OAuth creds, `projects.json`, trusted folders | `gemini_env`, `gemini_oauth`, `gemini_projects`, `gcloud_adc` | Gemini's own sandbox is not the Tier 1 boundary here; `gcloud_adc` is supplemental to Vertex config |

### Gemini CLI account change

On June 18, 2026, Gemini CLI stopped service for free, Pro, and Ultra personal
accounts. Gemini CLI access remains available through Gemini Code Assist
Standard and Enterprise licenses. It also remains available through paid Gemini
and Gemini Enterprise Agent Platform API keys. See the
[Google announcement](https://developers.googleblog.com/an-important-update-transitioning-gemini-cli-to-antigravity-cli/).

The Gemini Tier 1 adapter remains supported for these active auth paths. Use
`gemini_oauth` only for reviewed cached OAuth state. Use `gemini_env` for
Gemini Code Assist (GCA), a paid API key, or Vertex environment configuration.
Use `gcloud_adc` only as a Vertex supplement. It is not a standalone auth path.
Google, not Workcell, refuses the retired account types.

## Unsupported provider scaffold

| Provider | Target Tier 1 surface | Required control plane | Required auth input | Support status |
|---|---|---|---|---|
| Google Antigravity CLI | `workcell --agent antigravity --workspace ...` | session-local provider home/cache, settings, permissions, subagents, plugins, MCP, sandbox state, hooks, and reviewed instruction imports once official CLI provenance is pinned | explicit staged Google auth material, exact key names still pending official install/auth implementation | fail-closed scaffold; not current support |

The Copilot parity plan at
[docs/copilot-linux-local-compat-plan.md](copilot-linux-local-compat-plan.md)
is historical planning context; the current support boundary is the table
above and the quickstart in
[docs/examples/quickstart-copilot.md](examples/quickstart-copilot.md).

GitHub Copilot CLI has interactive and programmatic modes. It accepts an
environment token and a configurable `COPILOT_HOME`. It also has permissive
tool flags. The Workcell adapter maps or blocks these surfaces.

A live, authenticated `copilot -p` test is a pre-signing gate. Run it before a
signed commit changes the Copilot support claim.

Antigravity remains unsupported and fails closed. Before support, Workcell must
add a pinned install path, explicit auth, an adapter, tests, and live
certification.

For provider auth maturity and rollout caveats, see
[docs/injection-policy.md](injection-policy.md) and
[docs/provider-bootstrap-matrix.md](provider-bootstrap-matrix.md).

## Tiering rule

- Tier 1: provider CLI runs fully inside the bounded runtime
- Tier 2: GUI or IDE surface is only a client to that same bounded runtime
- Tier 3: host-native GUI, cloud, or web-only guidance with no claim of
  equivalent local isolation

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
