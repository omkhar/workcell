# Stability contract

This page defines the public compatibility contract for Workcell v1.
Workcell published `v1.0.2` on 2026-08-05 as its first 1.0 release.

These files contain the machine-readable contract:

- [`policy/public-contract.toml`](../policy/public-contract.toml) lists the public primitives.
- [`policy/operator-contract.toml`](../policy/operator-contract.toml) lists the stable operator workflows.
- [`policy/v1-contract-freeze.toml`](../policy/v1-contract-freeze.toml) contains the v1 compatibility floor.

`workcell-citools validate-public-contract` checks the public primitives.
`verify-operator-contract.sh` checks the operator workflows.
Release preflight compares the v1 floor with each earlier version in Git history.

The contract version is `1`.

`Stable` means that this deprecation policy applies.
`Experimental` interfaces have no compatibility guarantee.
Workcell makes no compatibility promise for an unlisted public interface.

## Deprecation policy

Workcell follows semantic versioning for stable v1 interfaces.
These rules apply:

- A patch or minor release must not break a stable interface.
- Workcell permits compatible additions to fields, prefixes, flags, and commands.
- Workcell must announce a deprecation in [`CHANGELOG.md`](../CHANGELOG.md).
- A deprecated runtime interface must show a documented deprecation notice.
- The interface must work for at least one later minor release.
- Workcell removes the interface only in a new major release.

Workcell permits an exception only when a stable interface causes a security vulnerability.
Workcell must record that exception in `CHANGELOG.md` and [`SECURITY.md`](../SECURITY.md).

Workcell uses a single-maintainer release process.
Only the latest release gets security fixes.
Read `CHANGELOG.md` before you upgrade.

## Exit-code contract

| Code | Meaning |
| ---: | --- |
| `0` | The operation succeeded or made a documented clean skip. |
| `1` | The operation started and failed at runtime. |
| `2` | The command, option, argument, or precondition is not valid. |
| `3` | `workcell-hostutil colima-status` did not find the requested profile. |
| `124` | A Colima operation timed out. |
| `126` | The launcher blocked execution or the target is not executable. |
| `127` | The launcher did not find the target. |
| `128+N` | Signal `N` terminated the supervised child process. |

The launcher returns the supervised child exit status without a change.
That child status can equal a launcher-owned code.
Use the diagnostic and command context to identify the source.

The Go command tools use exit code `2` for usage and precondition errors.
They use exit code `1` for runtime errors.
`internal/cliexit.ExitCodeError` carries the code through the error chain.

The `scripts/workcell` command uses exit code `2` for most precondition errors.
It runs with `set -euo pipefail`.

### Recorded intentional exceptions

Exit code `126` has two documented meanings.
It can report a policy block or an `EACCES` execution error.
Read the launcher diagnostic to identify the cause.

The shell uses two codes for host preconditions.
If Workcell cannot find a trusted host tool, it returns exit code `1`.
If Workcell cannot find the specified host working directory, it returns exit code `2`.

This split preserves shell and Go parity.
The parity tests in `internal/testkit` enforce it.

## CLI stability

`policy/operator-contract.toml` is the exact stable workflow inventory.
`policy/v1-contract-freeze.toml` stores each frozen canonical command.

The stable inventory includes these command groups:

- managed Codex, Claude, Copilot, and Gemini launch commands
- strict, development, and build modes
- autonomy, target, workspace, prepare, repair, dry-run, and cache options
- `publish-pr` and `support-bundle`
- `auth`, `policy`, and `why`
- `session` start, control, inspection, export, verification, and deletion commands
- doctor, inspect, log, authentication-status, cleanup, and version options

Use the policy file for the exact syntax.
Do not infer stable syntax from this summary.

Stable selector syntax does not change a target support tier.
The host support matrix controls target support.
Workcell supports strict Colima only on macOS arm64.
Workcell supports Docker Desktop compatibility only on macOS arm64.
The `aws-ec2-ssm` and `gcp-vm` selector names are stable.
These remote targets remain preview-only and launch-blocked.

The `--version` syntax is stable.
The `v1.0.2` bundle reports `workcell v1.0.1`.
The command reads the first changelog entry in that bundle.
Do not use this output to authenticate the release tag.
Verify the signed release tag, signed checksum, and bundle digest.

Workcell does not guarantee compatibility for these preview interfaces:

- Workcell does not implement `--ui gui`. The value fails closed.
- Workcell does not guarantee backend behavior for `aws-ec2-ssm` or `gcp-vm`.

`--agent antigravity` is a recognized but unsupported value.
It fails closed.

These higher-trust options require explicit acknowledgement:

- `--mode breakglass` requires a dated `--ack-breakglass` value.
- `--allow-arbitrary-command` requires a dated `--ack-arbitrary-command` value.
- `--allow-control-plane-vcs` requires `--ack-control-plane-vcs`.
- `--allow-repo-mcp` requires a dated `--ack-repo-mcp` value.

Workcell rejects each option without its acknowledgement.
These options reduce assurance or expose an additional control surface.
Use an option only for its documented exception.

## Stable machine-readable output

The public contract freezes these output prefixes:

| Prefix | Source |
| --- | --- |
| `host_os=` | Host support matrix and `scripts/workcell` |
| `host_arch=` | Host support matrix and `scripts/workcell` |
| `support_matrix_status=` | Host support matrix and `scripts/workcell` |
| `provider_bootstrap_` | Authentication policy and `scripts/workcell` |
| `target_kind=` | Session records and `scripts/workcell` |
| `target_provider=` | Session records and `scripts/workcell` |
| `workspace=` | Session records and `scripts/workcell` |
| `assurance=` | Session records and `scripts/workcell` |
| `publish_pr_url=` | PR publication output |
| `mutation score:` | Mutation result output |
| `surviving mutants:` | Mutation result output |
| `record_digest=` | Audit output from `scripts/workcell` |
| `prev_digest=` | Audit output from `scripts/workcell` |
| `egress_enforcement=` | Egress output from `scripts/workcell` |
| `session_verify=` | `session verify` output |
| `key_id=` | `session verify` output |
| `head_digest=` | `session verify` output |

The `scenario-manifest` TSV schema also has a stable column order:

1. `id`
2. `test_file`
3. `requires_credentials`
4. `lane`
5. `platform`
6. `validation_tier`
7. `manual`

`policy/public-contract.toml` also freezes the complete `session show --text` prefix set.
The validator compares that set with `SessionShowLines`.

## Injection-policy supported tables

An injection policy accepts these five top-level tables:

- `documents`
- `ssh`
- `credentials`
- `copies`
- `network`

`copies` is the only supported array-of-table.
The other four names use single tables.

The policy root also accepts these scalar keys:

- `version`
- `includes`

The `[network]` table accepts `allow_endpoints` and `deny_endpoints`.
It cannot change the network-policy mode.
See [Injection policy](injection-policy.md) for each table schema.

## Durable session-record fields

`SessionRecord` uses these stable JSON fields:

`version`, `session_id`, `profile`, `target_kind`, `target_provider`,
`target_id`, `target_assurance_class`, `runtime_api`, `workspace_transport`,
`agent`, `mode`, `status`, `ui`, `execution_path`, `workspace`,
`workspace_origin`, `workspace_root`, `worktree_path`, `git_branch`,
`git_head`, `git_base`, `container_name`, `monitor_pid`, `live_status`,
`session_audit_dir`, `audit_log_path`, `debug_log_path`,
`file_trace_log_path`, `transcript_log_path`, `started_at`, `observed_at`,
`finished_at`, `exit_status`, `initial_assurance`, `current_assurance`,
`final_assurance`, `workspace_control_plane`, `workspace_repo_mcp`,
`bootstrap_id`, `image_ref`.

`SessionExport` contains these stable fields:

- `session`
- `audit_records`

## Internal Go APIs

Internal Go APIs are not part of the public v1 contract.
Code outside this module cannot import packages under `internal/`.
Workcell does not guarantee compatibility for those APIs.
