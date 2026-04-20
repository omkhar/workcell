# Implement-First Delivery Plan

This document turns the current short-term roadmap into a concrete delivery
shape that keeps the boundary model intact and favors additive host-side
changes on top of the inspection and policy surfaces that already shipped.
The longer-lived runtime-target and deployment-reach program lives in
[`docs/runtime-target-expansion-plan.md`](runtime-target-expansion-plan.md),
and the deterministic phase breakdown lives in
[`docs/runtime-target-phase-plan.md`](runtime-target-phase-plan.md).
Phases 1 through 4 of that phase plan are now implemented in the repository;
this document now defines the immediate bridge into Phase 5 and the
prerequisite work for later remote-target phases.

The current repo already includes durable session records plus detached
host-side session control (`session start|attach|send|stop`) and basic
observability surfaces (`session list|show|logs|timeline|diff|export`). The
current repo also stores session, audit, and lock state under Workcell-owned
target-state roots while preserving compatibility reads for older
`~/.colima/...` records. The current repo also ships direct staged-auth flows,
Codex host-auth reuse on the reviewed resolver path, `--auth-status`,
credential-level `why` bootstrap summaries, and the provider/bootstrap support
matrix on the reviewed policy path. The current repo also ships the
validation-host bridge, canonical host-support matrix artifact, generated host
support docs, target-aware support diagnostics, and fail-closed launch gating
for unsupported host and target combinations. The active work in this slice is
now the remote-VM contract preparation. This document also records the queued
later backend prerequisites so later phases can start cleanly without pulling
provider-specific delivery forward into Phase 5.

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

## Phase 5 Exit Ownership

In this repo's current planning mode, each ownership lane below is intended to
be a distinct Codex-agent role or thread unless a change explicitly records a
human owner assignment. The goal is independent review and handoff, not one
blended owner wearing every hat at once.

- EM:
  holds the remote-VM phase boundary, prevents provider-specific backend work
  from being counted as Phase 5 delivery, and blocks preview claims that
  outrun the contract evidence
- TL:
  owns the integrated fake-target, conformance-harness, and deterministic
  contract landing criteria across code and launcher/operator behavior
- contract and docs owner:
  owns the canonical remote-contract docs, support matrices, and operator
  guidance that later provider phases must reuse
- validation owner:
  owns the repo-required remote-contract evidence, certification-lane
  boundaries, and harness reuse rules that later provider phases must satisfy

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
- the trusted `linux/amd64` validation-host bridge, canonical
  `policy/host-support-matrix.tsv` artifact, generated
  `docs/host-support-matrix.md` rollout doc, and fail-closed host diagnostics
  on `--inspect`, `--doctor`, and launch

## Active Phase 5 Track

### 1. Remote VM Contract Preparation

Scope for this delivery slice:

- define the provider-neutral `remote_vm` contract before any cloud backend
  ships
- make remote workspace materialization explicit and auditable
- define the reviewed brokered-access and remote image/bootstrap model
- add one canonical shared fake remote target, conformance harness, and
  fixtures that later cloud adapters must reuse unchanged
- keep target ordering and backend selection in the longer-lived
  runtime-target expansion program rather than in this active slice

Staffing:

- TL: remote contract and conformance-harness lead
- SWE A: remote workspace and audit model
- SWE B: contract docs, fake target, and deterministic evidence
- validation owner: repo-required evidence and certification-lane lead

Phase boundary:

- this is the active implementation slice and the only track that should change
  Phase 5 status from planned to complete
- the remaining tracks below are prerequisites and follow-on planning surfaces
  for later deterministic phases, not authority to ship backend delivery in the
  current slice

## Immediate Follow-On Prerequisites

### 2. Remote VM Contract Preparation

Scope to define before the owning later phase starts:

- define the provider-neutral `remote_vm` contract before any cloud backend
  ships
- make remote workspace materialization explicit and auditable
- define the reviewed brokered-access and remote image/bootstrap model
- add one canonical shared fake remote target, conformance harness, and
  fixtures that later cloud adapters must reuse unchanged
- keep target ordering and backend selection in the longer-lived
  runtime-target expansion program rather than in this active slice

Staffing:

- EM: remote-contract boundary owner
- TL: remote contract lead
- SWE A: remote workspace and audit model
- SWE B: contract docs, fakes, and deterministic evidence
- validation owner: shared conformance evidence and certification-lane lead

### 3. Scenario Evidence And Operator Verification

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

Before Phase 6, 7, 8, or 9 begins implementation, assign:

- one distinct Codex agent to each lane below by default
- only replace that with named human owners if the change or runbook records
  the human assignment explicitly

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
   provider/bootstrap support matrix as fixed input to the next slice
2. treat the completed validation-host bridge and host-support matrix as the
   fixed support boundary for later host claims
3. only after Phase 5 exits green, let provider-specific backend work reuse the
   shared remote-VM contract and deterministic evidence
4. expand authenticated, lower-assurance, and remote-contract coverage as each
   later phase lands rather than as a cleanup pass
5. leave actual `compat` and cloud-backend delivery to the later
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
- selecting or shipping `docker-desktop`, `aws-ec2-ssm`, `gcp-vm`, or
  `azure-vm` in this slice
- Kubernetes-backed execution or managed-workstation delivery in this slice
