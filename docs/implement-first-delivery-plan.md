# Implement-First Delivery Plan

This document turns the current short-term roadmap into a concrete delivery
shape that keeps the boundary model intact and favors additive host-side
changes on top of the inspection and policy surfaces that already shipped.
The longer-lived runtime-target and deployment-reach program lives in
[`docs/runtime-target-expansion-plan.md`](runtime-target-expansion-plan.md),
and the deterministic phase breakdown lives in
[`docs/runtime-target-phase-plan.md`](runtime-target-phase-plan.md).
Phase 1 of that phase plan is now implemented in the repository; this document
remains as the delivered-scope reference for that slice and the immediate
bridge to later runtime-target work.

The current repo already includes durable session records plus detached
host-side session control (`session start|attach|send|stop`) and basic
observability surfaces (`session list|show|logs|timeline|diff|export`). The
remaining work in this slice is to make those commands feel like one coherent
session platform with safer default workspace isolation, richer status
rendering, and deeper validation coverage.

## Principles

- keep new control paths host-owned and file-backed before introducing live
  orchestration
- build mutable workflow features on top of the shipped read-only inspection
  surface rather than replacing it
- preserve session-platform parity as target identity, assurance, and workspace
  transport metadata evolve
- share one policy and auth evaluation path across launcher, diagnostics, and
  operator tooling
- require explicit scenario evidence and operator verification material before
  broadening support claims
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

- preserve the shipped durable session inventory and detached control surface:
  `session list`, `session show`, `session logs`, `session timeline`,
  `session diff`, `session export`, `session start`, `session attach`,
  `session send`, and `session stop`
- default the safe path to one worktree per session when the operator opts into
  orchestrated session flows
- carry target identity, assurance, workspace mode, and workspace transport
  through the session surface as the underlying state model evolves
- keep the detached session path file-backed and host-owned rather than adding
  same-user local socket trust as a shortcut
- keep the implementation host-owned and file-backed; do not add same-user
  local socket trust as a shortcut

Staffing:

- TL: session supervisor lead
- SWE A: hostutil and launcher metadata plumbing
- SWE B: shell command surface and scenario coverage

### 2. Session Observability

Scope for this delivery slice:

- extend the shipped logs, transcript pointers, and command timeline views
  with clearer live status, branch/worktree, and assurance rendering
- make the same rendering target-aware so operators can understand which
  execution shape they are inspecting without guessing from Colima-specific
  names
- keep the first operator-facing surfaces CLI-first
- add a lightweight TUI or dashboard only after the session object and status
  model stabilize
- keep the observability path read-only with the same host-owned metadata model

Staffing:

- TL: supervisor UX lead
- SWE A: host-side session state and status rendering
- SWE B: shell integration and artifact plumbing

### 3. Runtime Target Foundations, Auth, and Validation Reach

Scope for this delivery slice:

- define the runtime-target model explicitly:
  separate target kind, assurance class, and workspace transport in launcher
  metadata, diagnostics, and session records
- generalize host-owned state, audit metadata, and detached-session records
  away from Colima-specific `profile` and `~/.colima/...` assumptions while
  preserving compatibility reads for existing records
- keep the shipped auth helpers and explainability surface aligned with new
  launch modes
- broaden resolver coverage without turning provider-native auth into the
  security boundary
- keep browser or setup handoffs explicit and host-owned where provider
  onboarding still needs credential bootstrap help
- define a narrow trusted `linux/amd64` validation-host lane plus phase gates
  for non-macOS and remote-target evidence
- define the first cross-platform `compat` target and the first `remote_vm`
  target without weakening the Tier 1 boundary or overstating Linux and
  Windows support

Staffing:

- TL: auth integrations lead
- SWE A: resolver metadata and auth-state reasoning
- SWE B: deployment-target documentation and scenario coverage

### 4. Scenario Evidence And Operator Verification

Scope for this delivery slice:

- expand authenticated, lower-assurance, session-supervisor, migration, and
  remote-workspace scenario coverage as each target-facing phase lands
- treat comparison material and operator verification flows as delivery
  criteria, not post-hoc documentation cleanup
- keep the first operator-facing rollout guidance CLI-first and host-owned

Staffing:

- TL: validation and contract lead
- SWE A: scenario and migration evidence
- SWE B: operator verification docs and comparison material

## Sequence

1. build mutable orchestration on top of the shipped inventory and inspection
   surface rather than replacing it
2. add worktree-per-session defaults and status surfaces before any dashboard
   work
3. refactor state, diagnostics, and auth/bootstrap onto the runtime-target
   model while preserving session-surface parity
4. define trusted `linux/amd64` validation hosts and explicit evidence gates
   before broad non-macOS or remote-target claims
5. expand authenticated and lower-assurance scenario coverage as each phase
   lands rather than as a cleanup pass
6. only then define and prepare the first cross-platform `compat` and
   `remote_vm` paths against the same host-owned policy model, leaving their
   actual delivery to the runtime-target expansion program

## Non-Goals In This Slice

- a separate always-on daemon
- same-user local socket trust
- default-open networking
- workspace-controlled policy overrides
- secret materialization paths that bypass the reviewed host policy flow
- automatic backend fallback
- broad Linux or Windows parity claims
- Kubernetes-backed execution or managed-workstation delivery in this slice
