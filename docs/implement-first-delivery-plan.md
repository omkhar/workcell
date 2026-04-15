# Implement-First Delivery Plan

This document turns the current short-term roadmap into a concrete delivery
shape that keeps the boundary model intact and favors additive host-side
changes on top of the inspection and policy surfaces that already shipped.

## Principles

- keep new control paths host-owned and file-backed before introducing live
  orchestration
- build mutable workflow features on top of the shipped read-only inspection
  surface rather than replacing it
- share one policy and auth evaluation path across launcher, diagnostics, and
  operator tooling
- bias toward simple, testable, fail-closed behavior over broad feature reach

## Distinguished Engineer Checkpoints

- initial design review:
  freeze the phase boundary, confirm host-only ownership, and reject changes
  that depend on provider config or same-user local trust
- final line-by-line review:
  verify every changed code path preserves the runtime boundary, does not widen
  credential exposure, and keeps host-side git/control-plane execution explicit

## Active Tracks

### 1. Session Supervisor Phase 2

Scope for this delivery slice:

- preserve the shipped durable session inventory and inspection surface:
  `session list`, `session show`, `session diff`, and `session export`
- add durable session creation and detached execution
- add `workcell session attach`, `workcell session send`, and
  `workcell session stop`
- default the safe path to one worktree per session when the operator opts into
  orchestrated session flows
- keep the implementation host-owned and file-backed; do not add same-user
  local socket trust as a shortcut

Staffing:

- TL: session supervisor lead
- SWE A: hostutil and launcher metadata plumbing
- SWE B: shell command surface and scenario coverage

### 2. Session Observability

Scope for this delivery slice:

- surface live status, branch/worktree, assurance state, logs, transcript
  pointers, and command timeline views
- keep the first operator-facing surfaces CLI-first
- add a lightweight TUI or dashboard only after the session object and status
  model stabilize
- keep the observability path read-only with the same host-owned metadata model

Staffing:

- TL: supervisor UX lead
- SWE A: host-side session state and status rendering
- SWE B: shell integration and artifact plumbing

### 3. Deployment Reach And Auth Coverage

Scope for this delivery slice:

- keep the shipped auth helpers and explainability surface aligned with new
  launch modes
- broaden resolver coverage without turning provider-native auth into the
  security boundary
- keep browser or setup handoffs explicit and host-owned where provider
  onboarding still needs credential bootstrap help
- define the first remote and cloud deployment targets without weakening the
  Tier 1 boundary or overstating Linux and Windows support

Staffing:

- TL: auth integrations lead
- SWE A: resolver metadata and auth-state reasoning
- SWE B: deployment-target documentation and scenario coverage

## Sequence

1. build mutable orchestration on top of the shipped inventory and inspection
   surface rather than replacing it
2. add worktree-per-session defaults and status surfaces before any dashboard
   work
3. expand authenticated and lower-assurance scenario coverage as new session
   commands land
4. only then add the first remote or cloud-spawn path against the same
   host-owned policy model

## Non-Goals In This Slice

- a separate always-on daemon
- same-user local socket trust
- default-open networking
- workspace-controlled policy overrides
- secret materialization paths that bypass the reviewed host policy flow
