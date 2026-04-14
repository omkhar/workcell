# Implement-First Delivery Plan

This document turns the near-term roadmap into a concrete delivery shape that
keeps the boundary model intact and favors additive host-side changes.

## Principles

- keep new control paths host-owned and file-backed before introducing live
  orchestration
- prefer read-only inspection commands before mutable workflow features
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

### 1. Session Supervisor Phase 2 Foundation

Scope for this delivery slice:

- extend durable session records with git/workspace metadata needed for review
- add `workcell session diff` as a host-only inspection surface
- keep the implementation file-backed; do not add a daemon or socket protocol
- defer `attach`, `send`, `stop`, and detached orchestration until the session
  object and review surfaces stabilize

Staffing:

- TL: session supervisor lead
- SWE A: hostutil and launcher metadata plumbing
- SWE B: shell command surface and scenario coverage

### 2. Policy Explainability

Scope for this delivery slice:

- add `workcell policy show`
- add `workcell policy validate`
- add `workcell policy diff`
- add a scoped `workcell why` for credential selection and status reasons
- keep outputs read-only and redact-by-default

Staffing:

- TL: policy explainability lead
- SWE A: policy evaluator and CLI command implementation
- SWE B: shell integration and scenario coverage

### 3. Auth Diagnostics

Scope for this delivery slice:

- improve shared auth posture metadata without expanding resolver trust
- keep `auth status` and launcher-facing auth posture aligned
- provide clearer provider-ready versus configured-only distinctions
- keep browser or setup handoffs explicit and host-owned

Staffing:

- TL: auth integrations lead
- SWE A: resolver metadata and auth-state reasoning
- SWE B: shell output, recovery guidance, and scenario coverage

## Sequence

1. land read-only inspection primitives first:
   policy commands, `why`, session metadata, session diff
2. unify auth posture reason codes across offline and launch-time paths
3. expand scenario coverage before adding any mutable orchestration commands
4. revisit detached sessions and live takeover only after the durable session
   object proves stable in tests and operator use

## Non-Goals In This Slice

- a background daemon
- same-user local socket trust
- default-open networking
- workspace-controlled policy overrides
- secret materialization paths that bypass the reviewed host policy flow
