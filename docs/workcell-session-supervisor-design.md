# Workcell Session Supervisor Design

## Goal

The session supervisor makes a launch a durable host-owned session. Use the
[operator contract](../policy/operator-contract.toml) and `workcell --help` for
the current command inventory. This page explains the design.

## Current Scope

The session command group supplies these functions:

- Start, attach, send input, stop, and delete a detached session.
- List sessions and show one durable record.
- Read retained logs and a session audit timeline.
- Compare a session workspace with its recorded clean Git base.
- Export a session and the audit records for that session.
- Verify the session audit chain and seal.

Detached sessions use `--session-workspace isolated` by default. This mode
creates a host clone and branch for a supported Git workspace. The operator can
select `direct` to reuse the live workspace. This selection is explicit.

## Why This Shape

The host launcher owns session policy, durable state, and orchestration. The
container is disposable. A transient `session-audit.*` directory does not store
the session record.

For local Colima, a new record has this default shape:

```text
~/.local/state/workcell/targets/local_vm/colima/<profile>/sessions/<session-id>.json
```

Compatibility reads also accept the legacy path under
`~/.colima/<profile>/sessions/`. Workcell-owned garbage collection preserves
durable session records. The operator uses `workcell session delete` to remove
a stopped session record and selected recorded artifacts. It does not remove the
recorded isolated clone.

## Data Model

A durable record can include:

- Session, profile, agent, mode, and interface identity.
- Target kind, provider, identifier, assurance class, and runtime API.
- Workspace source, transport, root, path, and worktree.
- Git branch, head, and clean launch base.
- Container name, monitor process, recorded status, and live status.
- Audit, debug, file-trace, and transcript paths.
- Start, observation, and finish times.
- Initial, current, and final assurance.
- Control-plane state for the workspace.

Use the Go session schema for the exact machine-readable fields.

## Audit Relationship

Applicable audit records contain the session identifier. This key lets the host
select records from a cumulative target audit log.

The command group uses the key as follows:

- `session timeline` prints audit records for the session.
- `session export` combines the durable record with audit records for the
  session.
- `session logs` resolves one retained file from the durable record.
- `session verify` checks the authoritative audit chain and host seal.

The durable JSON record is machine-readable state. The audit log is the event
history. Neither record replaces the other.

## User Experience

`session list` prints a compact host inventory. Verbose output adds target,
workspace transport, Git branch, and worktree fields.

`session show` prints the full record. Its text form prints stable `key=value`
lines.

`session diff` compares the current workspace with the clean Git base. It stops
if the launch workspace is dirty, the record has no clean base, or the host
path is not a self-contained Git worktree.

`session start`, `session send`, and `session stop` print stable text summaries
for host automation.

The control plane uses host files and processes. It does not add a Workcell
daemon or a same-user trusted local socket.

## Runtime Language Boundary

The host session plane is a Go and Bash system. Go owns the session-record
schema and validates the command route. Bash dispatches the session subcommands.
Go parses and validates inputs for attach, send, stop, delete, monitor,
timeline, logs, and verify. `scripts/workcell` still owns live container checks,
terminal state, artifact-path resolution, deletion, monitor lifecycle, and
controls for send and stop operations.

The process-local shell adapter is
[`detached-stdin-wrapper.sh`](../runtime/container/detached-stdin-wrapper.sh).
The entry point starts the adapter after any required user change. Without file
tracing, the adapter runs as container PID 1.

The adapter can:

- Keep the provider on a pseudo-terminal.
- Relay the Workcell-owned input FIFO in key-at-a-time mode.
- Synchronize terminal dimensions.
- Forward container signals to the provider.
- Reap the process tree.
- Restore terminal state.
- Return the provider exit status.

It does not select policy, write durable host state, or do host
orchestration. Move this adapter to a Go runtime tool if its responsibility
expands beyond this list.

## Current Non-Goals

The shipped session plane does not provide:

- A queue or warm pool.
- Pause, resume, checkpoint, or fork operations.
- Central multi-host inventory or analytics.
- A GUI or IDE client with the Tier 1 boundary claim.
- A remote worker fleet.

## Remaining Work

This page describes shipped behavior. See [ROADMAP.md](../ROADMAP.md) for future
work.

## Peer Review

The shipped design retains these review decisions.

### Findings

- Durable records do not use `session-audit.*` directories.
- Audit records contain a session identifier.
- The host uses a file-based control plane.

## Residual Risks

- Durable records stay until the operator deletes them.
- An aborted launch depends on host cleanup to record the aborted state.
- The control plane has no central session administrator.
- The process-local adapter remains shell code inside the container.

These limits do not change the host-owned boundary. Add new session functions
only when their state, audit, cleanup, and assurance behavior are explicit.
