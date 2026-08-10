# Workcell System Design

## Purpose

This page describes the system that is in this repository. Use the
[Roadmap](../ROADMAP.md) for planned work. Use the
[host-support matrix](../policy/host-support-matrix.tsv) for current support
decisions.

## Design Rules

Workcell uses these rules:

- Keep the trusted control plane on the host.
- Use a dedicated VM and a container for the strict local boundary.
- Keep each provider adapter small and explicit.
- Do not use provider configuration or instructions as the security boundary.
- Require explicit operator input for a lower-assurance path.
- Record the selected target and assurance class.
- Stop a live launch if the selected host and target do not have an allowed
  matrix row.

Workcell is not a generic container sandbox. It is not a cloud agent service.
It does not provide a central enterprise policy or analytics service.

## System Boundary

The strict local target has these parts:

1. The host launcher validates and prepares the session.
2. A dedicated Colima VM supplies the main machine boundary.
3. A hardened container runs the provider in the VM.
4. A provider adapter creates a managed provider home.
5. Host-owned records preserve session and audit data.

The macOS arm64 Docker Desktop target uses the same container launch path and
core hardening flags. It does not have a dedicated Workcell VM. It does not have
Workcell egress enforcement. It requires seccomp, but it does not require
AppArmor or SELinux. For these reasons, its assurance class is `compat`, not
`strict`.

The Colima VM belongs to one workspace-bound Colima profile. It does not belong
to one runtime mode or session. Concurrent sessions in one profile share the VM
and kernel.

The command line interface also accepts the AWS EC2 SSM and GCP VM target names.
Those targets supply reviewed dry-run broker plans. Their operator launch rows
are blocked. They do not start a Workcell-managed remote session.

The Apple `container` evaluation and the managed-workstation contract do not
have operator target values.

## Host Control Plane

[`scripts/workcell`](../scripts/workcell) is the main host control plane. It
does these tasks before it starts a provider:

- Scrub the host environment.
- Resolve trusted host tools.
- Validate the workspace, host, target, and support-matrix row.
- Load authentication and injection policy.
- Resolve approved credential sources on the host.
- Create the staged injection bundle.
- Mask mutable workspace control files on non-`breakglass` paths.
- Prepare the runtime image when it is missing or stale, or when the operator
  selects an explicit prepared rebuild.
- Apply the selected Colima egress policy.
- Start the container with controlled mounts and environment variables.
- Write session, audit, and assurance records.

The launcher uses Go packages in [`internal/`](../internal) for structured
policy, state, and metadata work. The shell file stays as orchestration glue.
Go code owns authentication policy, credential resolution, injection, host
state, sessions, target state, release helpers, and support-matrix evaluation.

## Target and Support Model

Durable session records store `target_kind`, `target_provider`,
`target_assurance_class`, and `workspace_transport`. The launcher reports all
fields in this table through diagnostics.

| Field | Meaning |
|---|---|
| `target_kind` | Execution shape, such as `local_vm` or `local_compat` |
| `target_provider` | Target implementation, such as `colima` |
| `target_assurance_class` | Boundary class, such as `strict` or `compat` |
| `support_matrix_status` | Support status for one host and target row |
| `support_matrix_launch` | Whether operator launch is allowed |
| `workspace_transport` | Method that moves the workspace into the target |

An assurance class does not give support by itself. The host-support matrix is
the only support decision source. Workcell does not select another target when
the selected target is blocked.

## Runtime Profiles

The profiles under [`runtime/profiles/`](../runtime/profiles) define different
runtime postures:

| Profile | Purpose |
|---|---|
| `strict` | Default managed provider session |
| `development` | Managed interactive development |
| `build` | Image preparation and build work |
| `breakglass` | Explicit higher-trust work with lower assurance |

The launcher records an assurance reduction. It does not present all profiles
as equivalent. `breakglass` requires operator acknowledgment.

## Workspace and Mounts

The launcher mounts the selected workspace at `/workspace`. It rejects broad
default workspaces such as `/` and the operator home. Approved persistent-cache
paths and narrow credential handoffs are separate mount classes.

The safe path does not pass these host resources into the runtime:

- The host home.
- Docker or agent sockets.
- Host keychains or credential stores.
- Provider-state directories from the host.
- Broad SSH, GPG, cloud, or GitHub authentication state.

The safe path masks repository files that can take control of a provider or
Git. Examples include provider settings, MCP settings, provider-state
directories, Git hooks, and mutable Git configuration.

Regular repository instruction files can enter a managed provider home as
imported workspace input. Workcell does not require Git tracking for these
imports. Codex imports `AGENTS.md`. Claude and Gemini import `AGENTS.md` and
their native instruction file. Copilot imports no repository instruction file.

## Authentication and Injection

Workcell does not mount a live host credential store. It uses this host-owned
sequence:

1. Load the injection policy.
2. Select the provider and runtime-mode scope.
3. Resolve an approved source on the host, when a supported resolver exists.
4. Render a staged bundle and its manifest.
5. Mount the staged bundle at enumerated runtime paths.
6. Build the managed provider home from approved material.

Directly staged credentials are the primary supported path. Workcell also
supports the resolver for Codex host-auth files. The Claude macOS keychain
resolver is a fail-closed scaffold. It does not supply a launch credential.

The injection schema controls input names, selectors, file modes, source types,
and target paths. It blocks arbitrary writes to reserved provider-home paths.

## Network Policy

Managed Colima allowlist modes install rules in the VM. These rules apply to the
whole Colima profile, not to one session. The most recent launch sets the
profile policy. A `breakglass` launch clears the managed allowlist rules.

The allowlist can include provider, target-broker, credential-derived,
auth-recovery, validated policy, and profile endpoints. Ephemeral local launches
can also include build and snapshot endpoints. It does not list all hosts that a
session can reach. A deny rule can remove an endpoint from the effective session
or bootstrap set.

Rule replacement is not atomic. A failed replacement can leave the profile
without the default-deny rule. Existing connections can continue after a more
restrictive policy replaces the rule set. These are residual risks of the
current profile-wide control.

Docker Desktop reports `egress_enforcement=none`. Workcell does not claim VM
egress enforcement for that target.

## Container Start Sequence

The runtime uses this control chain:

1. [`entrypoint.sh`](../runtime/container/entrypoint.sh) validates the managed
   environment and prepares runtime state.
2. [`runtime-user.sh`](../runtime/container/runtime-user.sh) manages the mapped
   host UID and GID.
3. [`home-control-plane.sh`](../runtime/container/home-control-plane.sh) builds
   the provider home under `/state/agent-home`.
4. [`provider-wrapper.sh`](../runtime/container/provider-wrapper.sh) scrubs the
   provider environment.
5. [`provider-policy.sh`](../runtime/container/provider-policy.sh) rejects
   prohibited provider flags and configuration changes.
6. The selected provider binary starts.

Mutable sessions start the entry point as root so that it can prepare runtime
state. The entry point then changes to the mapped UID and GID. Read-only
sessions start with the mapped UID and GID.

The container launch uses capability removal, `no-new-privileges`, a process
limit, controlled temporary filesystems, and a managed environment. The
[Invariant Test Plan](../verify/invariants/README.md) and
[Expected Controls](../verify/invariants/expected-controls.md) are the detailed
control sources.

## Provider Adapters

Workcell supports Tier 1 command-line adapters for Codex, Claude Code, GitHub
Copilot CLI, and Gemini CLI. Each adapter maps the provider's native home,
settings, authentication inputs, and prohibited options.

Workcell does not hide provider differences behind one common provider control
plane. Use the [Provider Matrix](provider-matrix.md) for the exact mapping and
current provider notes.

## Exec Guard

The Rust exec guard under [`runtime/container/rust/`](../runtime/container/rust)
adds checks below the shell wrappers. It uses a libc preload interposer. On the
strict path, it blocks direct native ELF files from mutable paths. It also
blocks mutable shebang scripts that select a protected runtime. Workcell
supplies guarded wrappers for tools such as Git and Node.

The guard is defense in depth. A fully static process does not load the
interposer for its own later `exec` call. `/workspace` does not have a kernel
`noexec` mount. Thus, a fully static caller can make a direct `exec` call that
this layer does not inspect. The container and VM boundaries stay in force. Use
[Expected Controls](../verify/invariants/expected-controls.md) for the exact
enforcement scope.

## Detached Sessions

The host owns detached session state. The operator can start, attach, send
input, stop, list, show, delete, inspect logs, inspect a timeline, inspect a
diff, export, and verify a session. The
[operator contract](../policy/operator-contract.toml) is the authoritative
command inventory.

Detached sessions use a host-owned JSON record. The record includes identity,
target, workspace, Git, process, log, time, and assurance data. Workcell stores
new local Colima records in the Workcell-owned target-state tree. Compatibility
reads accept the old Colima session path.

The default workspace mode for a detached session creates an isolated host
clone and branch for a supported Git workspace. The session plane uses files
and host processes.
It does not require a Workcell daemon or a trusted local socket.

## Audit and Publication

A real launch appends a host-owned audit record. Session finalization tries to
sign a chained session head. Verification stops when the chain, seal, or public
key is invalid. The [Signed Session Audit Records](signed-session-audit-records.md)
page defines the exact guarantees and legacy limits.

Repository publication is a host-side action. This repository uses
`./scripts/repo-publish-pr.sh` after fresh local `pr-parity` evidence exists.
The Tier 1 container does not receive ambient maintainer signing or GitHub
publication state. Injection policy can stage reviewed GitHub authentication
and configuration for runtime use, but pull-request publication stays a
host-side workflow.

## Verification Sources

Use these sources for detailed claims:

- [Threat Model](threat-model.md) for assets, attacks, controls, and residual
  risks.
- [Injection Policy](injection-policy.md) for the machine-checked injection
  schema.
- [Diagnostics and the Support Matrix](diagnostics-and-support-matrix.md) for
  target decisions and output fields.
- [Validation Scenarios](validation-scenarios.md) for test coverage.
- [Provenance](provenance.md) and [Release Process](releasing.md) for release
  controls.

## Current Limits

- Operator launch is supported only for the allowed macOS arm64 matrix rows.
- Linux amd64 is a validation host, not an operator host.
- Linux arm64 and Windows targets are unsupported.
- AWS and GCP remote targets have preview rows on macOS arm64.
- The Colima, AWS, and GCP Linux amd64 rows are `validation-host-only`.
- Operator launch remains blocked for all these rows.
- Apple `container` is preview-only and has no operator target value.
- Workcell has no managed-workstation backend.
- The session plane has no queue, pause, resume, checkpoint, or fork model.
- The supported user interface is the CLI. GUI and IDE paths do not have the
  Tier 1 claim.
- The built-in resolvers support Codex host-auth reuse. The Claude macOS
  keychain resolver stops without a credential.
- Claims for the strict macOS boundary require live host certification.
