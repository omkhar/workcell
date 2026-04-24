# Implement-First Delivery Plan

This document turns the current short-term roadmap into a concrete delivery
shape that keeps the boundary model intact and favors additive host-side
changes on top of the inspection and policy surfaces that already shipped.
The longer-lived runtime-target and deployment-reach program lives in
[`docs/runtime-target-expansion-plan.md`](runtime-target-expansion-plan.md),
and the deterministic phase breakdown lives in
[`docs/runtime-target-phase-plan.md`](runtime-target-phase-plan.md).
Phases 1 through 8 of that phase plan are now implemented in the repository;
this document now records the delivered bridge into the cloud `remote_vm`
preview backends and the prerequisite handoff pattern for later backend
phases.

The current repo already includes durable session records plus detached
host-side session control (`session start|attach|send|stop`) and basic
observability surfaces (`session list|show|logs|timeline|diff|export`). The
current repo also stores session, audit, and lock state under Workcell-owned
target-state roots while preserving compatibility reads for older
`~/.colima/...` records. The current repo also ships direct staged-auth flows,
Codex host-auth reuse on the reviewed resolver path, `--auth-status`,
credential-level `why` bootstrap summaries, and the provider/bootstrap support
matrix on the reviewed policy path. The current repo also ships the trusted
validation-host lane, canonical host-support matrix, and fail-closed
unsupported-combination diagnostics from Phase 4. The current repo now also
ships the canonical preview-only remote-VM contract, shared fake target, and
deterministic conformance harness from Phase 5. The current repo now also
ships the first cross-platform `compat` backend through the explicit
`local_compat/docker-desktop/compat` path, deterministic backend selection and
fail-closed diagnostics, and live Docker Desktop certification smoke from
Phase 6. The current repo now also ships the first two preview-only cloud
`remote_vm` backends, `aws-ec2-ssm` and `gcp-vm`, on the shared conformance
contract with deterministic broker-plan evidence and certification-only live
smoke lanes.

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

Owner-lane default:

- treat `EM`, `TL`, `contract and docs owner`, and `validation owner` as
  distinct Codex-agent lanes or threads unless a later change or runbook names
  specific humans explicitly

## Phase 7 And 8 Exit Ownership

- EM:
  holds the preview support boundary, rollout gate, and the decision that the
  cloud backend preview paths are ready to move forward
- TL:
  owns `aws-ec2-ssm` and `gcp-vm` integration, deterministic contract reuse,
  and rollback readiness on the shared target model
- contract and docs owner:
  owns preview-boundary labeling, the canonical matrices, and operator
  guidance for enable, disable, and rollback
- validation owner:
  owns repo-required remote-contract reuse evidence plus the live AWS and GCP
  certification lanes that bound support claims

## Delivered Foundation

The following foundation is already merged and should now be treated as input
to the next slice rather than as active delivery work:

- session inventory, detached control, logs/timeline, diff/export, and richer
  target-aware session metadata
- Workcell-owned target-state roots with compatibility reads for older
  Colima-shaped records
- direct staged-auth flows, Codex host-auth reuse, `--auth-status`,
  policy/bootstrap explainability, the provider/bootstrap support matrix, and
  the current secretless and authenticated scenario baseline
- the trusted validation-host lane, canonical host-support matrix, and
  fail-closed unsupported-combination diagnostics from Phase 4
- the canonical preview-only remote-VM contract, shared fake target, and
  deterministic conformance harness from Phase 5
- the explicit `local_compat/docker-desktop/compat` backend plus deterministic
  backend-selection, fail-closed diagnostics, rollback guidance, and live
  Docker Desktop certification smoke from Phase 6
- the preview-only `remote_vm/aws-ec2-ssm/compat` and
  `remote_vm/gcp-vm/compat` backends plus deterministic broker-plan
  diagnostics, shared conformance reuse, rollback guidance, and
  certification-only live smoke lanes from Phases 7 and 8

## Delivered Phase 7 And 8 Track

### 1. Cloud Remote VM Preview Backends

Scope for this delivery slice:

- ship the first two cloud `remote_vm` backends on top of the completed
  canonical remote-contract harness without redefining the contract
- keep the rollout preview-only and audited, with explicit brokered access and
  no inbound public SSH requirement on the supported path
- reuse the canonical support matrices, host-support boundaries, and provider
  bootstrap surfaces rather than introducing provider-specific forks
- require deterministic harness reuse plus live certification evidence and
  explicit enable, disable, and rollback guidance in the same slice
- keep managed-workstation delivery out of this active slice

Staffing:

- EM: preview support-boundary owner
- TL: AWS and GCP backend lead
- SWE A: remote contract integration and audited lifecycle wiring
- SWE B: preview docs, rollback guidance, and deterministic evidence
- validation owner: shared harness reuse evidence plus certification-lane lead

Phase boundary:

- this is now delivered foundation for Phase 7 and Phase 8 status
- the remaining tracks below are prerequisites and follow-on planning surfaces
  for later deterministic phases, not authority to ship demand-gated cloud or
  managed-workstation targets in the current slice

## Immediate Follow-On Prerequisites

### 2. Scenario Evidence And Operator Verification

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

## Later Phase Handoff

Before Phase 9 begins implementation, assign distinct owner lanes:

- EM:
  support-boundary owner for rollout scope, preview/GA decisions, and rollback
- TL:
  integration owner for shared harness reuse, deterministic tests, and target
  behavior
- contract and docs owner:
  owner for canonical matrices, rollout docs, and operator verification
- validation owner:
  owner for repo-required evidence and certification-lane definitions

## Sequence

1. treat the completed shared auth/bootstrap path plus the
   provider/bootstrap support matrix and completed Phase 4 host-support
   surfaces plus the completed Phase 5 remote-contract harness as fixed input
   to the next slice
2. ship cloud `remote_vm` backends before any managed-workstation delivery work
3. expand authenticated, lower-assurance, and target-contract coverage as each
   later phase lands rather than as a cleanup pass
4. leave later raw `remote_vm` and managed-workstation delivery to the later
   runtime-target program phases once these prerequisites and owner handoffs
   are done

## Non-Goals In This Slice

- a separate always-on daemon
- same-user local socket trust
- default-open networking
- workspace-controlled policy overrides
- secret materialization paths that bypass the reviewed host policy flow
- automatic backend fallback
- broad Linux or Windows parity claims
- selecting or shipping `azure-vm` in this slice
- Kubernetes-backed execution or managed-workstation delivery in this slice
