# Onboarding and Authentication

Use an injection policy to give stable inputs to a session. The usual path is
`~/.config/workcell/injection-policy.toml`.

## Host commands

Use the host authentication commands for common operations:

```bash
workcell auth init
workcell auth set --agent codex --credential codex_auth --source /path/to/auth.json
workcell auth status --agent codex
workcell policy validate
workcell why --agent codex --mode strict --credential codex_auth
workcell --agent codex --auth-status --workspace /path/to/repo
```

- `workcell auth status` shows the host policy view.
- `workcell --auth-status` shows the launch view after selector evaluation and
  preprocessing.
- `workcell policy show`, `validate`, and `diff` inspect the merged host policy.
- `workcell why` explains why Workcell selected or filtered one credential. It
  also reports when the policy does not configure a credential and the exact
  `out-of-scope` and `configured-only` states.

To remove the managed copy of a credential, use:

```bash
workcell auth unset --credential codex_auth
```

This command removes the managed credential copy from the selected injection
policy state. It does not remove the original source file.

Direct staged credentials are the primary supported path. Workcell can also
reuse Codex host authentication through `codex-home-auth-file`. The Claude
macOS resolver is a fail-closed scaffold. It does not provide a supported
export path.

The status commands print `provider_bootstrap_*` fields. The `why` command
prints `bootstrap_*` fields. Use these fields with
[provider-bootstrap-matrix.md](provider-bootstrap-matrix.md).

## Supported staged inputs

Workcell can stage:

- common or provider-specific instruction files
- `codex_auth`
- `claude_auth`, `claude_api_key`, and `claude_mcp`
- `copilot_github_token`
- `gemini_env`, `gemini_oauth`, `gemini_projects`, and `gcloud_adc`
- scoped GitHub CLI state through `github_hosts` and `github_config`
- SSH configuration, known hosts, and identities
- explicit files or directories for non-reserved paths

The safe path does not support a host home mount, arbitrary secret environment
variables, or host socket forwarding.

## Copilot token handoff

Configure only `copilot_github_token` for Copilot authentication. The launcher
removes the original staged token from direct runtime mounts. It uses a
temporary handoff mount outside provider state. The entrypoint moves the token
to a temporary runtime file, removes the mounted copy, and replaces itself
without the token in its environment. The wrapper removes the runtime file and gives
`COPILOT_GITHUB_TOKEN` only to the managed Copilot child.

Workcell does not put the token in `COPILOT_HOME`. It does not use host `gh`
auth, `GH_TOKEN`, `GITHUB_TOKEN`, host Copilot state, a host keychain, or a host
home as a Copilot authentication source.

Workcell does not support Antigravity credentials or provider state.

See [injection-policy.md](injection-policy.md) for the complete schema. See
[examples/injection-policy.toml](examples/injection-policy.toml) for an
example.
