# Stability Contract

This document records which parts of Workcell's surface are stable ahead of
1.0, and the exit-code contract shared across the Rust launcher, the Go
binaries, and the shell entrypoint. It is the pre-1.0 compatibility reference;
G1 extends it into the versioned 1.0 contract.

"Stable" means the shape is not expected to change without a deprecation note.
"Experimental" means it may change or be removed. Absence from this document is
not a stability promise.

## Exit-code contract

The canonical convention across every entrypoint:

| Code | Meaning |
|------|---------|
| 0 | success |
| 2 | usage / precondition error (missing or unknown command, wrong arity, bad option, unmet precondition) |
| 1 | runtime error (an operation attempted and failed) |
| 3 | colima profile status: no matching profile (`workcell-hostutil colima-status`) |
| 124 | colima operation timed out (mirrors GNU `timeout`) |
| 126 | launcher: fail-closed policy block, or target not executable (`EACCES`) |
| 127 | launcher: target not found (`ENOENT`) |
| 128+N | launcher: supervised child terminated by signal N |
| 0–255 | launcher: passthrough of the supervised child's own exit status |

Per surface:

- **Go binaries** (`workcell-citools`, `workcell-hostutil`, `workcell-colimautil`,
  `workcell-runtimeutil`): usage/precondition errors exit **2**, runtime errors
  exit **1**. The exit code is carried through the error chain by
  `internal/cliexit.ExitCodeError{Code}`. Deterministic exit-code tests live in
  each binary's `main_test.go`.
- **Shell entrypoint** (`scripts/workcell`): usage/precondition failures exit
  **2** (the dominant convention); a small set of host-precondition failures
  exit **1** (see the recorded exception below). Runs under `set -euo pipefail`.
- **Rust launcher** (`workcell-launcher`): fail-closed authorization refusals and
  non-executable targets exit **126**; missing targets exit **127**; a supervised
  child's exit status or signal (`128+N`) is passed through unchanged.

### Recorded intentional exceptions

These are deliberate and are documented rather than "fixed", because changing
them would break existing shell↔Go parity or the launcher's fail-closed
posture:

- **Launcher 126 is overloaded** — it covers both a fail-closed policy block and
  an exec `EACCES` (not executable). Both match the shell convention that 126
  means "command found but not runnable". A caller distinguishes them by the
  launcher's stderr diagnostic, not the code.
- **Shell 1-vs-2 split for host preconditions** — "missing trusted host tool"
  exits **1** while "missing host working directory" exits **2**. This split is
  frozen for byte-for-byte parity with the Go translations
  (`internal/publishpr/host_exec.go`) and is guarded by `internal/testkit`'s
  bash↔Go parity harness.

## CLI stability

The user-facing CLI is `scripts/workcell`. Stable surface:

- Core flags: `--agent`, `--target`, `--mode`, `--workspace`, `--agent-autonomy`,
  `--dry-run`, `--prepare` / `--prepare-only`, and the introspection flags
  `--doctor` / `--inspect` / `--logs` / `--auth-status` / `--gc`.
- Subcommands: `publish-pr`; `session <start|attach|send|stop|list|show|delete|logs|timeline|diff|export>`;
  `auth <init|set|unset|status>`; `policy <show|validate|diff>`; `why`.

Experimental or explicitly gated (may change; already marked in `--help`):

- `--agent antigravity` (recognized as planned but unsupported).
- `--ui gui` (not implemented; fails closed).
- preview targets `aws-ec2-ssm` and `gcp-vm`.
- `--mode breakglass` (gated behind a dated `--ack-breakglass`),
  `--allow-arbitrary-command` (behind `--ack-arbitrary-command`), and
  `--allow-control-plane-vcs` (behind `--ack-control-plane-vcs`).

## Stable machine-readable output

These lines are consumed by tests, CI, or other tooling and are treated as
contract-like `key=value` / structured output:

- session `show` metadata: `target_kind=`, `target_provider=`, `workspace=`,
  `assurance=`, and peers.
- support matrix: `host_os=`, `host_arch=`, `support_matrix_status=`.
- `publish_pr_url=` (from `publish-pr`).
- `mutation score: NN.NN% (k/t killed)` and `surviving mutants: …`.
- audit digest lines: `record_digest=`, `prev_digest=`.
- `scenario-manifest` TSV rows (tab-delimited).

## Internal Go APIs

All Go code lives under `internal/`, so it is not importable outside the module;
"stable" here means a stable intra-module contract. The most depended-upon is
`internal/cliexit` (the exit-code carrier used by every CLI). Treat other
`internal/` package APIs as implementation detail that may change with their
callers.
