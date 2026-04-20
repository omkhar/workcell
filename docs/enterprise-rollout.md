# Enterprise Rollout Today

This page describes the current Workcell rollout model for teams.

Workcell is local-first today:

- supported hosts are Apple Silicon macOS only
- the primary product surface is a host-launched local runtime
- there is no Workcell-managed cloud or remote worker plane yet
- there is no centralized Workcell policy, inventory, or analytics service yet

The current rollout model is to distribute reviewed host-side files and let
operators launch Workcell locally inside the shared VM-plus-container boundary.

## Traceability note

- supported-host and release-window claims map to the validated install matrix
- auth, policy, session, and host-publication claims map to the named anchors
  in [validation-scenarios.md](validation-scenarios.md)
- the "not centralized yet" bullets below are support-boundary statements, not
  claims of automated proof

## What Workcell standardizes today

Workcell already gives teams one shared contract for:

- the runtime boundary and runtime profiles
- provider-home seeding and control-plane masking
- the host-side injection-policy format
- host-side auth, policy, and explainability commands
- detached session records and host-side session control
- host-side publication and release provenance

Those pieces are implemented in the product. They do not depend on a separate
central administration plane.

## What stays host-local today

These parts are still local to each operator host:

- Workcell installation and updates
- `~/.config/workcell/` policy files and included fragments
- managed credential material referenced by `workcell auth`
- `~/.local/state/workcell/targets/local_vm/colima/<profile>/sessions/`
  durable session records
- local launch history, retained debug logs, transcripts, and file traces

If you want team-wide consistency today, distribute reviewed host-side files
through your existing host configuration workflow rather than expecting
Workcell to act as a central policy service.

## Recommended rollout shape

### 1. Standardize the support boundary

Roll out Workcell only to supported Apple Silicon macOS hosts and treat the
current GitHub-hosted install matrix as the tested release window, not as proof
for all macOS variants. Use
[host-support-matrix.md](host-support-matrix.md) as the canonical support
boundary, and treat `linux/amd64` only as the reviewed validation-host bridge
described there rather than as a supported launch host.

### 2. Distribute reviewed host-side files

Use your existing device-management, bootstrap, or dotfile workflow to place:

- reviewed Workcell policy fragments under `~/.config/workcell/`
- org-wide instruction docs referenced from `[documents]`
- reviewed credential files referenced from `[credentials]`
- reviewed SSH config and identity paths referenced from `[ssh]`

Keep repo-local provider control files as imported inputs, not as the live
control plane.

### 3. Prefer direct staged auth inputs, with reviewed Codex host reuse when needed

Direct staged credentials are still the main supported auth path today.

- Codex: `codex_auth`
- Claude: `claude_auth`, `claude_api_key`, and `claude_mcp`
- Gemini: `gemini_env`, `gemini_oauth`, and `gemini_projects`
- Gemini Vertex supplement: `gcloud_adc`

Reviewed Codex host-auth reuse through `codex-home-auth-file` is also
supported on the same staged host-owned path when a team wants to reuse the
existing `~/.codex/auth.json` cache file instead of copying it into a separate
managed location.

Current caveats:

- the built-in Claude macOS resolver scaffold can record intent, but it remains
  fail-closed until a supported export path exists
- `gcloud_adc` is supplemental to Vertex config, not a standalone Gemini auth
  mode

See [injection-policy.md](injection-policy.md) for the current by-provider auth
maturity summary and
[provider-bootstrap-matrix.md](provider-bootstrap-matrix.md) for the explicit
repo-required versus manual bootstrap tiers.

### 4. Scope shared GitHub and SSH inputs deliberately

When teams want shared GitHub CLI or SSH behavior:

- scope `github_hosts` and `github_config` with `providers = [...]`
- keep SSH material in the `[ssh]` section rather than ad hoc copies
- review MCP state explicitly instead of trusting ambient workspace config

This keeps shared inputs least-privilege and visible in policy.

### 5. Keep publication on the host

The supported publication path remains:

1. run the provider inside Workcell
2. review the resulting changes
3. publish from the host with `workcell publish-pr`

Do not treat in-session git publication as equivalent to the host-side release
and signing model.

## What is not centralized yet

Workcell does not yet provide:

- centralized policy distribution inside the product
- org-wide RBAC, SSO, or SCIM features
- centralized session inventory or usage analytics
- a preserved-boundary GUI or IDE client path
- remote or cloud-spawned workspaces

Those are roadmap items, not part of the supported contract today.

## Assurance notes for rollout decisions

- GitHub-hosted CI proves repo shape, smoke behavior, reproducibility, release
  posture, and install/uninstall behavior on GitHub-hosted Apple Silicon
  `macos-26` and `macos-15`.
- GitHub-hosted CI does not prove the full local macOS Colima boundary.
- The strongest boundary claim still depends on local validation and operator
  discipline on supported hosts.
- Lower-assurance paths such as `development`, `breakglass`, prompt autonomy,
  package mutation, and host-side transcript capture should stay explicit in
  rollout guidance.

## Related docs

- [Getting started](getting-started.md)
- [Injection policy](injection-policy.md)
- [Provider matrix](provider-matrix.md)
- [Requirements validation](requirements-validation.md)
- [Validation scenarios](validation-scenarios.md)
- [Roadmap](../ROADMAP.md)
