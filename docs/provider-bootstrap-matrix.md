# Provider Bootstrap Matrix

This page records the reviewed host-side bootstrap and explainability contract
for provider auth inputs.

Use it with:

- [Injection policy](injection-policy.md)
- [Provider matrix](provider-matrix.md)
- [Validation scenarios](validation-scenarios.md)

The host-side commands expose the same bootstrap summary on one reviewed path:

- `workcell auth status` prints `provider_bootstrap_*` for the selected agent
- `workcell --auth-status` prints the same fields after selector evaluation and
  resolver preprocessing
- `workcell why` prints `bootstrap_*` for one credential decision

Support tiers on this page mean:

- `repo-required`: host-side policy, staging, and explainability are proven by
  deterministic repo validation
- `certification-only`: the path needs live runtime or provider smoke before
  Workcell claims it as supported
- `manual`: the path is operator-driven, supplemental, or intentionally
  fail-closed rather than fully automated on the reviewed path

These tiers describe the Workcell bootstrap and staging contract. Live
provider-authenticated UX remains the separate manual provider-e2e lane unless
the evidence explicitly says otherwise.

Roadmap-only provider entries are called out separately. They are not accepted
by `workcell auth`, `workcell --auth-status`, or `workcell why` until the
matching adapter and evidence land.

## Current Matrix

| Provider | Auth path | Bootstrap path | Support | Evidence | Notes |
|---|---|---|---|---|---|
| Codex | direct staged `codex_auth` | `direct-staged` | `repo-required` | `tests/scenarios/shared/test-auth-commands.sh`, `tests/scenarios/shared/test-auth-status.sh`, `tests/scenarios/shared/test-policy-commands.sh` | primary supported Codex path |
| Codex | `[credentials.codex_auth] resolver = "codex-home-auth-file"` | `host-resolver` | `repo-required` | `tests/scenarios/shared/test-codex-resolver-launcher.sh`, `internal/authresolve/resolve_credential_sources_test.go` | reuses the reviewed host `~/.codex/auth.json` file without passing the host home through to the runtime |
| Claude | direct staged `claude_auth` | `direct-staged` | `repo-required` | `tests/scenarios/shared/test-auth-commands.sh`, `tests/scenarios/shared/test-auth-status.sh` | reviewed exported Claude auth file |
| Claude | direct staged `claude_api_key` | `direct-staged` | `repo-required` | `tests/scenarios/shared/test-auth-commands.sh`, `tests/scenarios/shared/test-auth-status.sh` | helper-backed API key path |
| Claude | `[credentials.claude_auth] resolver = "claude-macos-keychain"` | `host-export-scaffold` | `manual` | `tests/scenarios/shared/test-auth-commands.sh`, `tests/scenarios/shared/test-auth-status.sh`, `tests/scenarios/shared/test-policy-commands.sh` | records intent and stays fail-closed until a supported export path exists |
| Gemini | direct staged `gemini_env` | `direct-staged` | `repo-required` | `tests/scenarios/shared/test-auth-status.sh` | reviewed API key or Vertex env-file path |
| Gemini | direct staged `gemini_oauth` | `direct-staged` | `repo-required` | `tests/scenarios/shared/test-auth-status.sh` | reviewed cached Gemini OAuth path |
| Gemini | direct staged `gemini_projects` supplement | `project-registry-supplement` | `manual` | `tests/scenarios/shared/test-auth-status.sh`, `internal/authpolicy/manage_test.go` | reviewed Gemini project registry input; not a standalone auth mode |
| Gemini | direct staged `gcloud_adc` supplement | `vertex-supplement` | `manual` | `scripts/verify-invariants.sh`, `docs/examples/gemini-vertex-setup.md` | supplemental Vertex input only; not a standalone Gemini auth mode |

## Planned Copilot CLI Bootstrap Track

GitHub Copilot CLI is the next provider-parity track, but it has no supported
bootstrap row yet. Before it can join the current matrix, Workcell must add an
explicit staged token credential such as `copilot_github_token`, export it only
to the managed Copilot child process as `COPILOT_GITHUB_TOKEN`, and prevent
host `~/.copilot`, host keychains, `GH_TOKEN`, `GITHUB_TOKEN`, ambient
`gh auth token`, or whole-home state from becoming implicit inputs.

The Copilot bootstrap row is supportable only after deterministic auth-status,
policy, bootstrap-summary, control-plane seeding, and unsafe-argument tests
exist. Those tests must prove Copilot auth fallback fails closed outside the
Workcell-staged `COPILOT_GITHUB_TOKEN` path. The row also needs live provider
certification proving a non-destructive `copilot -p` launch with staged
credentials.

## Remote Target Bootstrap

Preview remote targets also carry an explicit host-side bootstrap contract.
Today that matrix is:

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
- [Quickstart: Gemini](examples/quickstart-gemini.md)
- [Gemini Vertex AI setup](examples/gemini-vertex-setup.md)

There is no Copilot quickstart until Copilot CLI support lands with matching
adapter, auth, and certification evidence.
