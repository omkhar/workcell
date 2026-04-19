# Implement-First Delivery Plan

This document turns the current short-term roadmap into a concrete delivery
shape that keeps the boundary model intact and favors additive host-side
changes on top of the inspection and policy surfaces that already shipped.
The longer-lived runtime-target and deployment-reach program lives in
[`docs/runtime-target-expansion-plan.md`](runtime-target-expansion-plan.md),
and the deterministic phase breakdown lives in
[`docs/runtime-target-phase-plan.md`](runtime-target-phase-plan.md).
Phases 1 and 2 of that phase plan are now implemented in the repository; this
document now defines the immediate bridge into Phase 3 and the prerequisite
work for later host-compatibility and remote-target phases.

The current repo already includes durable session records plus detached
host-side session control (`session start|attach|send|stop`) and basic
observability surfaces (`session list|show|logs|timeline|diff|export`). The
current repo also stores session, audit, and lock state under Workcell-owned
target-state roots while preserving compatibility reads for older
`~/.colima/...` records. The current repo also ships direct staged-auth flows,
`--auth-status`, and host-side auth explainability on the reviewed policy
path. The active work in this slice is to complete the shared auth/bootstrap
path. This document also records the queued validation-host and remote-VM
contract prerequisites so later phases can start cleanly without pulling
backend delivery forward into Phase 3.

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

## Phase 3 Exit Ownership

- EM:
  holds the phase boundary, prevents later validation-host or remote-VM work
  from being counted as Phase 3 delivery, and blocks support claims that
  outrun the evidence
- TL:
  owns the integrated Phase 3 landing criteria across code, deterministic
  tests, and launcher/operator behavior
- contract and docs owner:
  owns the provider/bootstrap support matrix plus the operator and rollout docs
  that bound what Phase 3 actually supports

## Delivered Foundation

The following foundation is already merged and should now be treated as input
to the next slice rather than as active delivery work:

- session inventory, detached control, logs/timeline, diff/export, and richer
  target-aware session metadata
- Workcell-owned target-state roots with compatibility reads for older
  Colima-shaped records
- direct staged-auth flows, `--auth-status`, policy explainability, and the
  current secretless and authenticated scenario baseline

## Active Phase 3 Track

### 1. Shared Auth and Bootstrap Completion

Scope for this delivery slice:

- broaden built-in resolver coverage where the current implementation is still
  intentionally narrow
- keep direct staged inputs as the primary supported auth path until broader
  resolver evidence exists
- make browser/setup handoffs explicit and host-owned where provider bootstrap
  still needs operator intervention
- keep auth selection, explainability, and launch-time diagnostics on one
  reviewed host-owned path
- publish an explicit provider/bootstrap support matrix that marks each auth
  path as repo-required, certification-only, or manual
- separate deterministic repo-required auth/bootstrap evidence from
  live-provider certification and manual provider-e2e validation

Staffing:

- TL: auth integrations lead
- SWE A: resolver metadata and auth-state reasoning
- SWE B: bootstrap handoffs, diagnostics, and scenario coverage

Phase boundary:

- this is the active implementation slice and the only track that should change
  Phase 3 status from planned to complete
- the remaining tracks below are prerequisites and follow-on planning surfaces
  for later deterministic phases, not authority to ship backend delivery in the
  current slice

## Immediate Follow-On Prerequisites

### 2. Validation Host And Host-Compatibility Matrix

Scope to define before the owning later phase starts:

- define the narrow trusted `linux/amd64` validation-host lane before broader
  non-macOS or cloud claims
- express support as `host OS x target kind x assurance class`
- add backend-aware diagnostics that fail closed on unsupported host/backend
  combinations
- keep Linux and Windows claims limited to what the validation-host evidence
  and docs actually prove

Staffing:

- TL: validation-host and rollout lead
- SWE A: validation-host tooling and diagnostics
- SWE B: support matrix docs and operator guidance

### 3. Remote VM Contract Preparation

Scope to define before the owning later phase starts:

- define the provider-neutral `remote_vm` contract before any cloud backend
  ships
- make remote workspace materialization explicit and auditable
- define the reviewed brokered-access and remote image/bootstrap model
- keep target ordering and backend selection in the longer-lived
  runtime-target expansion program rather than in this active slice

Staffing:

- TL: remote contract lead
- SWE A: remote workspace and audit model
- SWE B: contract docs, fakes, and deterministic evidence

### 4. Scenario Evidence And Operator Verification

Scope to expand as each later phase lands:

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

1. finish the shared auth/bootstrap path on the shipped host-owned policy and
   explainability surface, and freeze the provider/bootstrap support matrix for
   this phase
2. only after Phase 3 exits green, define trusted `linux/amd64` validation
   hosts plus an explicit host-compatibility matrix
3. only after that, define the remote-VM contract and deterministic evidence
   before any provider-specific backend work
4. expand authenticated, lower-assurance, and remote-contract coverage as each
   later phase lands rather than as a cleanup pass
5. leave actual `compat` and cloud-backend delivery to the later
   runtime-target program phases once these prerequisites are done

## Non-Goals In This Slice

- a separate always-on daemon
- same-user local socket trust
- default-open networking
- workspace-controlled policy overrides
- secret materialization paths that bypass the reviewed host policy flow
- automatic backend fallback
- broad Linux or Windows parity claims
- selecting or shipping `docker-desktop`, `aws-ec2-ssm`, `gcp-vm`, or
  `azure-vm` in this slice
- Kubernetes-backed execution or managed-workstation delivery in this slice
