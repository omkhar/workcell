# Onboarding and Auth

The supported way to feed stable inputs into sessions is an explicit injection
policy, usually at `~/.config/workcell/injection-policy.toml`.

Use the host-side auth helpers instead of hand-editing the common case:

```bash
workcell auth init
workcell auth set --agent codex --credential codex_auth --source /path/to/auth.json
workcell auth status --agent codex
workcell auth unset --agent codex --credential codex_auth
workcell policy validate
workcell why --agent codex --mode strict --credential codex_auth
workcell --agent codex --auth-status --workspace /path/to/repo
```

`workcell auth status` shows the host policy view. `--auth-status` shows the
derived launch view after selector evaluation and preprocessing.
`workcell policy show|validate|diff` inspects the merged host policy, and
`workcell why` explains why one credential is selected, out of scope, filtered,
or still only configured on the host side.

Direct staged credentials are the primary supported auth path today. Built-in
resolver coverage now includes Codex host-auth reuse through
`codex-home-auth-file`, while the Claude macOS resolver remains a fail-closed
scaffold until a supported export path exists.

`workcell auth status` and `workcell --auth-status` print
`provider_bootstrap_*` lines, and `workcell why` prints `bootstrap_*` lines for
the selected credential. Use those fields with
[provider-bootstrap-matrix.md](provider-bootstrap-matrix.md) to see
whether a path is repo-required, certification-only, or manual.

Workcell can stage:

- common or provider-specific instruction fragments
- provider-native credentials such as `codex_auth`, `claude_auth`,
  `claude_api_key`, `claude_mcp`, `copilot_github_token`, `gemini_env`,
  `gemini_oauth`, `gemini_projects`, and `gcloud_adc`
- scoped GitHub CLI credentials through `github_hosts` and `github_config`
- SSH config, known hosts, and identities
- explicit copied files or directories for non-reserved paths

It does not support whole-home passthrough, arbitrary environment-variable
secret injection, or host socket forwarding on the safe path.

Copilot auth is intentionally narrow: configure `copilot_github_token` and let
Workcell stage it through reviewed host-side inputs. For an auth-required
Copilot launch, the launcher removes the original staged token file from direct
runtime mounts, passes a temporary host-mounted token handoff outside mounted
provider state, the runtime entrypoint consumes it into a transient handoff
file, unlinks the mounted file, and re-execs without the token in its
environment. The wrapper unlinks that runtime file before exporting the value
as `COPILOT_GITHUB_TOKEN` only to the managed Copilot child process.
Workcell does not copy or pre-stage the token into `COPILOT_HOME`. Host `gh`
auth, `GH_TOKEN`, `GITHUB_TOKEN`, host Copilot provider state (`~/.copilot`,
`~/.config/github-copilot`, `~/.cache/github-copilot`), keychains, and
whole-home state are not readiness or auth sources. Antigravity credentials and
provider-home state are not supported inputs yet.

See [injection-policy.md](injection-policy.md) and
[examples/injection-policy.toml](examples/injection-policy.toml).
The by-provider bootstrap tiers and handoffs live in
[provider-bootstrap-matrix.md](provider-bootstrap-matrix.md).
