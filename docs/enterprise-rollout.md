# Enterprise rollout

This page describes the current Workcell rollout model for teams.

Workcell uses a local-first model.
It supports Apple Silicon macOS hosts only.
The primary product interface starts a local runtime from the host.
Workcell does not supply a managed cloud worker or remote worker.
It also does not supply a central policy, inventory, or analytics service.

Use [Enterprise evidence baseline](enterprise-evidence-baseline.md) for the current evidence map.

## Evidence authority

Use these sources for rollout decisions:

- [`policy/host-support-matrix.tsv`](../policy/host-support-matrix.tsv) defines host support.
- [`policy/operator-contract.toml`](../policy/operator-contract.toml) defines supported workflows.
- [Validation scenarios](validation-scenarios.md) identifies test evidence.
- [Retention policy](retention-policy.md) defines hosted workflow artifact retention.
- [Provenance](provenance.md) defines release verification.

A support statement does not prove an automated control.
Use the named evidence for each claim.

## Current team contract

Workcell supplies one team contract for these items:

- runtime boundaries and runtime profiles
- provider-state input and control-plane masks
- host injection policies
- host authentication and policy commands
- detached session records and session control
- host PR publication and release provenance

These items do not require a central Workcell service.

## Host-local state

These items remain on each operator host:

- Workcell installation and updates
- policy files under `~/.config/workcell/`
- managed credential files that `workcell auth` uses
- session records under the selected Workcell state root
- launch history, debug logs, transcripts, and file traces

Use your existing host-configuration system to distribute reviewed files.
Do not use Workcell as a central policy service.

## Recommended rollout

### 1. Define the support boundary

Install Workcell only on supported Apple Silicon macOS hosts.
Treat the hosted macOS matrix as a tested release window.
Do not use that matrix as proof for all macOS versions.

### 2. Distribute reviewed files

Use your device-management or bootstrap system to place these files:

- Workcell policy fragments under `~/.config/workcell/`
- organization instructions that `[documents]` entries reference
- credential files that `[credentials]` entries reference
- SSH configuration and identity files that `[ssh]` entries reference

Keep repository provider-control files as imported inputs.
Do not use those files as the live control plane.

### 3. Use staged authentication

Use direct staged credentials as the primary authentication path.

- Codex uses `codex_auth`.
- Claude uses `claude_auth`, `claude_api_key`, and `claude_mcp`.
- Gemini uses `gemini_env`, `gemini_oauth`, and `gemini_projects`.
- Gemini Vertex can also use `gcloud_adc`.
- Copilot uses `copilot_github_token`.

Codex can reuse the reviewed host file that `codex-home-auth-file` selects.
This path stages only the selected `auth.json` file.

The Claude macOS resolver records operator intent but fails closed.
No supported export path exists.

`gcloud_adc` supplements Gemini Vertex configuration.
It is not a separate Gemini authentication mode.

Do not distribute Copilot provider state, GitHub CLI authentication, keychain data, or ambient tokens.
Antigravity is unsupported and fails closed.
Do not distribute Antigravity provider state or credentials.

See [Injection policy](injection-policy.md) for the exact input schema.
See [Provider bootstrap matrix](provider-bootstrap-matrix.md) for test tiers.

Before a Copilot enterprise rollout, document these items:

- organization policy and license requirements
- token ownership
- audit requirements
- telemetry and content-capture policy
- the exact staged-token path

Strict Copilot mode rejects provider telemetry and content-capture environment variables.
Mark each exception as lower assurance.
Document each exception.
Acknowledge each exception.
Audit each exception.
Test each exception.

### 4. Limit shared GitHub and SSH inputs

Set `providers = [...]` on shared `github_hosts` and `github_config` inputs.
Put SSH inputs in the `[ssh]` table.
Review MCP state before you stage it.

These steps keep shared inputs visible and limited.

### 5. Publish from the host

Use this publication sequence:

1. Run the provider inside Workcell.
2. Review the changes.
3. Run local `pr-parity`.
4. Run `./scripts/repo-publish-pr.sh` on the host.

Do not publish from the managed session.
Host publication preserves the reviewed signature and policy path.

## Interfaces that are not central

Workcell does not supply these interfaces:

- central policy distribution
- organization RBAC, SSO, or SCIM
- central session inventory or usage analytics
- a supported GUI or IDE client
- supported remote or cloud workspaces

These interfaces are outside the current support contract.

## Assurance limits

GitHub CI proves repository validation, smoke behavior, reproducibility, and release posture.

On `macos-26` and `macos-15`, hosted CI proves these install properties:

- bundle installation
- launcher-link removal
- man-page-link removal
- Homebrew installation and formula removal

Hosted CI does not prove complete bundle uninstall behavior.
It also does not prove the strict local Colima boundary.

Local certification supplies the current strict-boundary evidence.
Follow the certification procedure.
Record the result.

Document each lower-assurance path in team instructions.
These paths include development mode, breakglass, prompt autonomy, package changes, and host transcripts.

## Related documents

- [Getting started](getting-started.md)
- [Injection policy](injection-policy.md)
- [Provider matrix](provider-matrix.md)
- [Enterprise evidence baseline](enterprise-evidence-baseline.md)
- [Requirements validation](requirements-validation.md)
- [Validation scenarios](validation-scenarios.md)
- [Roadmap](../ROADMAP.md)
