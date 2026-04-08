# Workcell Session Supervisor Design

## Goal

Workcell needs a session-supervisor layer so operators can reason about Workcell
launches as durable session objects rather than as one-off foreground shell
commands.

This document defines the first implementation phase that is now practical in
this repository without weakening the existing boundary model.

## Phase 1 Scope

Phase 1 adds durable host-side session records and inventory commands:

- `workcell session list`
- `workcell session show --id ...`
- `workcell session export --id ...`

Each launched session writes durable metadata under the managed Colima profile
state instead of relying only on the transient `session-audit.*` directory.

## Data Model

Each session record stores:

- `session_id`
- `profile`
- `agent`
- `mode`
- `status`
- `ui`
- `execution_path`
- `workspace`
- `container_name`
- `session_audit_dir`
- `audit_log_path`
- `debug_log_path`
- `file_trace_log_path`
- `transcript_log_path`
- `started_at`
- `finished_at`
- `exit_status`
- `initial_assurance`
- `final_assurance`
- `workspace_control_plane`

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

Phase 1 also adds `session_id` to launch, assurance-change, and exit audit
records in the profile audit log.

That allows `workcell session export` to bundle:

- the durable session record
- matching audit log lines for that session

This is a clean bridge between human-readable audit history and machine-readable
session metadata.

## User Experience

`workcell session list` is optimized for a quick host-side inventory:

- session id
- status
- agent
- mode
- profile
- start time
- assurance
- workspace

`workcell session show` returns the full durable record for one session.

`workcell session export` returns the full record plus matching audit records,
either to stdout or a user-selected host file.

## Explicit Non-Goals For Phase 1

Phase 1 does not attempt to implement:

- background execution
- pause or resume
- follow-up prompts
- interactive takeover
- per-task worktree creation
- a long-running daemon
- remote worker fleets

Those remain later phases.

## Future Phases

Phase 2 should add:

- `workcell session start` as a durable session creator
- per-session worktree or branch isolation
- attach and takeover
- follow-up prompts to a running session
- artifact browsing beyond raw audit export

Phase 3 should add:

- pause and resume
- optional warm pools
- GUI and IDE clients against the same host-controlled session plane
- enterprise inventory and policy surfaces

## Peer Review

### Findings

1. Durable records must not live in `session-audit.*`.
   Reason: `--gc` already treats those directories as stale runtime scratch and
   can delete them.

2. Session export needs an explicit session key in the audit log.
   Reason: profile audit logs are cumulative and otherwise cannot be filtered
   safely to one launch.

3. Phase 1 should stay host-side and file-based.
   Reason: introducing a daemon, sockets, or background workers before the data
   model stabilizes would add complexity faster than it adds operator value.

### Residual Risks

- There is no retention policy yet for durable session records.
- Aborted launches rely on host cleanup logic to mark the record as aborted.
- The current phase gives inventory and export, not orchestration.

Those are acceptable for a first slice because the main goal here is to create
durable, auditable session objects without weakening the reviewed boundary.
