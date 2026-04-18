# Workcell Session Supervisor Design

## Goal

Workcell needs a session-supervisor layer so operators can reason about
launches as durable session objects rather than as one-off foreground shell
commands.

This document describes the current shipped session-supervisor slice in the
repository and the gaps that remain without weakening the existing boundary
model.

For the supported operator inventory, treat
[`policy/operator-contract.toml`](../policy/operator-contract.toml) plus
`workcell --help` as authoritative. This design note is explanatory.

## Current Scope

The current implementation provides durable host-side session records plus
host-side inventory, observability, and detached-control commands:

- `workcell session list`
- `workcell session show --id ...`
- `workcell session logs --id ... --kind ...`
- `workcell session timeline --id ...`
- `workcell session diff --id ...`
- `workcell session export --id ...`
- `workcell session start`
- `workcell session attach`
- `workcell session send`
- `workcell session stop`
- `workcell session delete`

Each launched session writes durable metadata under the managed Colima profile
state instead of relying only on the transient `session-audit.*` directory.

Detached sessions default to `--session-workspace isolated`, so the detached
path already points toward worktree-per-session operation on the safe path. The
public CLI also supports `--session-workspace direct` when operators
intentionally want a detached session to reuse the live workspace path.

## Data Model

Each session record stores the durable host-side view of one launch, including:

- session identity and profile metadata
- agent, mode, and UI
- workspace root, workspace origin, and worktree path
- git branch, head, and clean launch base when available
- runtime container name, monitor PID, and live status
- retained audit, debug, file-trace, and transcript paths
- start, observe, and finish timestamps
- initial, current, and final assurance state
- workspace control-plane mode

The durable record lives under:

- `~/.colima/<profile>/sessions/<session_id>.json`

The transient `session-audit.*` directory remains separate and disposable.

## Why This Shape

This design is intentionally host-owned.

That matters because:

- the host launcher already owns the trusted control plane
- the transient container should stay disposable
- `--gc` already cleans transient audit dirs
- durable session inventory should survive the transient session cleanup path

Storing records under `sessions/` instead of inside `session-audit.*` avoids
confusing retention with ephemeral runtime scratch state.

## Audit Relationship

The current implementation adds `session_id` to launch, control, assurance, and
exit audit records in the profile audit log.

That allows:

- the durable session record to stay machine-readable
- `workcell session export` to bundle matching audit records
- `workcell session timeline` to print the session-specific audit trail
- `workcell session logs` to resolve one retained log for a recorded session

This is the bridge between human-readable audit history and machine-readable
session metadata.

## User Experience

`workcell session list` is optimized for a quick host-side inventory:

- session id
- status and live status
- agent and mode
- profile
- start time
- assurance
- workspace

`workcell session show` returns the full durable record for one session.

`workcell session logs` prints one retained audit, debug, file-trace, or
transcript log for a recorded session.

`workcell session timeline` prints the audit-log records that match one
session.

`workcell session diff` renders the current workspace status and diff against
the clean git base recorded when the session started. It fails closed if the
workspace was already dirty at launch, if no git base was recorded, or if the
workspace is no longer a self-contained git worktree on the host.

`workcell session export` returns the full record plus matching audit records,
either to stdout or a user-selected host file.

Detached sessions can be started, attached to, steered, and stopped from the
host without introducing a separate always-on daemon or same-user local socket
trust.

## Current Non-Goals

The current slice does not yet attempt to implement:

- a session queue or warm-pool system
- pause or resume
- checkpoints or forks
- centralized multi-host inventory or analytics
- preserved-boundary GUI or IDE clients
- remote worker fleets

## Remaining Work

The remaining near-term work is to:

- harden and normalize worktree-per-session defaults
- expose branch/worktree and assurance state more clearly in operator-facing
  status views
- deepen validation coverage for detached-session transitions and
  lower-assurance paths
- add richer artifact browsing without weakening the host-owned model

Longer-term product-direction items such as pause/resume, checkpoints, GUI
clients, and enterprise inventory belong in [ROADMAP.md](../ROADMAP.md).

## Peer Review

### Findings

1. Durable records must not live in `session-audit.*`.
   Reason: `--gc` already treats those directories as stale runtime scratch and
   can delete them.

2. Session export needs an explicit session key in the audit log.
   Reason: profile audit logs are cumulative and otherwise cannot be filtered
   safely to one launch.

3. The session plane should stay host-side and file-based first.
   Reason: introducing a daemon, sockets, or background workers before the data
   model stabilizes would add complexity faster than it adds operator value.

## Residual Risks

- There is no retention policy yet for durable session records.
- Aborted launches rely on host cleanup logic to mark the record as aborted.
- Detached-session control exists, but there is still no queue, pause/resume,
  or centralized session administration plane.

Those are acceptable for the current slice because the main goal here is to
create durable, auditable session objects and bounded detached-session control
without weakening the reviewed boundary.
