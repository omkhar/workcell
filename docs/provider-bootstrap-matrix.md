# Provider Bootstrap Matrix

This page defines the host bootstrap contract for provider auth inputs.

Use it with:

- [Injection policy](injection-policy.md)
- [Provider matrix](provider-matrix.md)
- [Validation scenarios](validation-scenarios.md)

The host-side commands expose the same bootstrap summary on one reviewed path:

- `workcell auth status` prints `provider_bootstrap_*` for the selected agent
- `workcell --auth-status` prints the same fields after selector evaluation and
  resolver preprocessing
- `workcell why` prints `bootstrap_*` for one credential decision

This page uses these support levels:

- `repo-required`: Repository tests prove policy, staging, and status output.
- `certification-only`: A live runtime or provider test must pass before a
  support claim.
- `manual`: The path is supplemental, operator-driven, or intentionally
  fail-closed.

These levels apply to the Workcell bootstrap and staging contract. Provider
authentication stays in the manual end-to-end lane unless evidence states a
different level.

Unsupported providers appear in a separate section. The auth commands do not
accept them.

## Current Matrix

| Provider | Auth path | Bootstrap path | Support | Evidence | Notes |
|---|---|---|---|---|---|
| Codex | direct staged `codex_auth` | `direct-staged` | `repo-required` | `tests/scenarios/shared/test-auth-commands.sh`, `tests/scenarios/shared/test-auth-status.sh`, `tests/scenarios/shared/test-policy-commands.sh` | primary supported Codex path |
| Codex | `[credentials.codex_auth] resolver = "codex-home-auth-file"` | `host-resolver` | `repo-required` | `tests/scenarios/shared/test-codex-resolver-launcher.sh`, `internal/authresolve/resolve_credential_sources_test.go` | reuses the reviewed host `~/.codex/auth.json` file without passing the host home through to the runtime |
| Claude | direct staged `claude_auth` | `direct-staged` | `repo-required` | `tests/scenarios/shared/test-auth-commands.sh`, `tests/scenarios/shared/test-auth-status.sh` | reviewed exported Claude auth file |
| Claude | direct staged `claude_api_key` | `direct-staged` | `repo-required` | `tests/scenarios/shared/test-auth-commands.sh`, `tests/scenarios/shared/test-auth-status.sh` | helper-backed API key path |
| Claude | `[credentials.claude_auth] resolver = "claude-macos-keychain"` | `host-export-scaffold` | `manual` | `tests/scenarios/shared/test-auth-commands.sh`, `tests/scenarios/shared/test-auth-status.sh`, `tests/scenarios/shared/test-policy-commands.sh` | records intent and stays fail-closed until a supported export path exists |
| GitHub Copilot CLI | direct staged `copilot_github_token` | `direct-staged` | `repo-required` | `tests/scenarios/shared/test-auth-commands.sh`, `tests/scenarios/shared/test-auth-status.sh`, `tests/scenarios/shared/test-policy-commands.sh`, `scripts/container-smoke.sh` | converted to a temporary host-mounted token handoff outside mounted provider state, removed from direct runtime mounts, consumed into a transient runtime handoff file with the Workcell entrypoint as PID 1, and exported to the managed Copilot child as `COPILOT_GITHUB_TOKEN`; host `gh` auth, host keychains, `GH_TOKEN`, `GITHUB_TOKEN`, and host Copilot provider state (`~/.copilot`, `~/.config/github-copilot`, `~/.cache/github-copilot`) are not auth sources |
| Gemini | direct staged `gemini_env` | `direct-staged` | `repo-required` | `tests/scenarios/shared/test-auth-status.sh` | reviewed Gemini Code Assist (GCA), API key, or Vertex environment file path |
| Gemini | direct staged `gemini_oauth` | `direct-staged` | `repo-required` | `tests/scenarios/shared/test-auth-status.sh` | reviewed cached Gemini OAuth path |
| Gemini | direct staged `gemini_projects` supplement | `project-registry-supplement` | `manual` | `tests/scenarios/shared/test-auth-status.sh`, `internal/authpolicy/manage_test.go` | reviewed Gemini project registry input; not a standalone auth mode |
| Gemini | direct staged `gcloud_adc` supplement | `vertex-supplement` | `manual` | `scripts/verify-invariants.sh`, `docs/examples/gemini-vertex-setup.md` | supplemental Vertex input only; not a standalone Gemini auth mode |

## Copilot CLI Bootstrap Notes

Copilot accepts only the staged `copilot_github_token` credential. Workcell
removes the original token from direct runtime mounts. It also removes the
staged copy from the mounted injection bundle.

For an authenticated start, Workcell uses a temporary host-mounted handoff.
This mount is outside provider state. The entrypoint moves the token to a
temporary runtime file. It unlinks the mounted file. Then it uses `exec` to
replace itself without the token environment.

The entrypoint remains PID 1, without Docker `--init`. Thus,
`/proc/1/environ` does not contain the token.

The wrapper reads and unlinks the runtime file. It exports
`COPILOT_GITHUB_TOKEN` only to the managed Copilot child. It does not copy the
token to `COPILOT_HOME`. Development and debug commands do not receive a token
handoff.

Host Copilot state, keychains, ambient tokens, and whole-home state are not
safe-path inputs.

Repository tests prove the deterministic bootstrap row. The maintainer must run
a live, authenticated `copilot -p` test before a signed commit changes the
Copilot support claim.

## Unsupported Antigravity CLI Scaffold

Google Antigravity CLI has no supported bootstrap row. Workcell recognizes the
provider name and stops before runtime preparation.

Before support, Workcell must pin the official install and auth model. It must
add explicit Google auth input. Host account state, browser profiles,
keychains, home directories, and provider caches must stay excluded.

Support also requires deterministic auth, policy, bootstrap, and unsafe-flag
tests. A live provider test with staged credentials must pass.

## Remote Target Bootstrap

Preview remote targets also have a host bootstrap contract:

| Target | Bootstrap path | Support | Evidence | Notes |
|---|---|---|---|---|
| `remote_vm/aws-ec2-ssm/compat` | reviewed broker plan via `workcell --target aws-ec2-ssm --dry-run` | `repo-required` for diagnostics, `certification-only` for live smoke | `tests/scenarios/shared/test-aws-remote-vm-dry-run.sh`, `tests/scenarios/shared/test-aws-ec2-ssm-launch-smoke.sh`, `internal/remotevm/conformance_test.go`, `docs/aws-ec2-ssm-preview.md` | requires `aws`, `session-manager-plugin`, brokered Session Manager access, and no inbound public SSH on the supported path |
| `remote_vm/gcp-vm/compat` | reviewed broker plan via `workcell --target gcp-vm --dry-run` | `repo-required` for diagnostics, `certification-only` for live smoke | `tests/scenarios/shared/test-gcp-remote-vm-dry-run.sh`, `tests/scenarios/shared/test-gcp-vm-launch-smoke.sh`, `internal/remotevm/conformance_test.go`, `docs/gcp-vm-preview.md` | requires `gcloud`, brokered IAP access, a VM without an external NAT IP, and no inbound public SSH on the supported path |

## Handoff Meanings

The bootstrap summary fields also report the remaining operator handoff:

- `none`: the selected auth path is launch-ready on the reviewed path
- `host-stage-file`: stage the reviewed auth material directly with
  `workcell auth set --source ...`
- `host-provider-cache`: Workcell expects the reviewed provider cache file to
  exist on the host and resolves it into ordinary staged input
- `host-export`: Workcell can describe the intended host export, but the
  operator must still produce a reviewed file before launch

## Related Examples

- [Quickstart: Codex](examples/quickstart-codex.md)
- [Quickstart: Claude](examples/quickstart-claude.md)
- [Quickstart: Copilot](examples/quickstart-copilot.md)
- [Quickstart: Gemini](examples/quickstart-gemini.md)
- [Gemini Vertex AI setup](examples/gemini-vertex-setup.md)

There is no Antigravity quickstart. Workcell must add the adapter, auth input,
tests, and certification evidence before support.
