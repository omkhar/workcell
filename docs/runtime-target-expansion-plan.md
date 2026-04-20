# Runtime Target Expansion Plan

This document is the durable planning surface for Workcell's runtime-target and
deployment-reach expansion. It complements:

- [ROADMAP.md](../ROADMAP.md) for high-level direction and non-goals
- [runtime-target-phase-plan.md](runtime-target-phase-plan.md) for the
  deterministic delivery-phase breakdown
- [docs/implement-first-delivery-plan.md](implement-first-delivery-plan.md) for
  the active slice that is in flight now

It exists to keep the longer program coherent across iterations without
turning the roadmap into a checklist or overloading the active-slice plan with
later-phase decisions.

## Current Contract

Today Workcell's supported Tier 1 contract is:

- Apple Silicon macOS host
- dedicated Colima VM plus hardened container
- host-owned control plane, policy, audit, and detached session flows

This program does not relax that contract. It expands deployment reach by
introducing explicit runtime-target classes, support tiers, and review gates so
new targets can be added without overstating equivalence to the current strict
path.

## Terms

- `target kind`: the execution shape being selected, such as `local_vm`,
  `local_compat`, `remote_vm`, or `managed_workstation`
- `assurance class`: the support tier attached to a target, such as `strict` or
  `compat`
- `workspace transport`: how the workspace reaches the runtime, such as local
  mount, isolated worktree mount, or remote materialization

These are separate concepts in the control plane, docs, diagnostics, and
session records.
`strict` is reserved for targets that preserve the dedicated VM plus hardened
container boundary and pass backend-specific invariant checks. Other supported
targets must stay labeled `compat` or another explicitly lower-assurance class.

## Planning Principles

- keep one shared boundary and many thin adapters; do not hide real provider
  differences behind a fake universal abstraction
- keep the host control plane authoritative for policy, diagnostics, and audit
- preserve the active session-platform slice as state, diagnostics, and target
  metadata evolve
- require explicit scenario evidence and operator verification material before
  broadening support claims
- treat managed workstations as a separate product mode from raw remote VMs
- keep Kubernetes-backed execution out of this program

## Recommended Support Order

### Current strict target

- `colima` remains the strict macOS default

### First cross-platform compatibility target

- `docker-desktop` is the first cross-platform `compat` target on macOS, Linux,
  and Windows
- it must stay explicitly lower assurance than the current Colima path

### Remote VM targets

- `aws-ec2-ssm` is the first `remote_vm` target
- `gcp-vm` is the second `remote_vm` target on the same control-plane contract
- `azure-vm` is demand-gated and follows only after the first two remote VM
  targets stabilize

### Managed workstation targets

- `gcp-cloud-workstations` stays a later managed-workstation track
- other managed-workstation candidates are evaluated separately from the raw
  remote VM program

## Program Workstreams

### 1. Session platform parity

- preserve and extend the shipped session surface during target-model and state
  migration
- carry target, assurance, and workspace-transport rendering into session
  inspection and control flows, not only `doctor` and `inspect`

### 2. Runtime target and state model

- introduce explicit target identity and capability reporting
- generalize session, audit, and lock state away from Colima-specific paths
  while preserving compatibility reads
- keep `colima` behavior unchanged until the new model is proven

### 3. Shared auth and bootstrap

- broaden resolver coverage and bootstrap handoffs on the same host-owned auth
  path used by launcher, diagnostics, and operator tooling
- make remote-target auth dependencies explicit before any cloud preview

### 4. Trusted validation hosts and host compatibility

- define a narrow trusted `linux/amd64` validation-host lane before broad
  non-macOS claims
- express support as `host OS x target kind x assurance class`
- keep one canonical versioned capability and support-matrix artifact that
  docs, diagnostics, fixture tests, and rollout guidance derive from
- do not claim Linux or Windows `strict` parity until the same guarantees are
  proven there

### 5. Backend delivery

- keep backend selection and ordering in this program doc rather than pulling
  it forward into the active auth/bootstrap slice
- extract `colima` behind the new target model first
- add `docker-desktop` as the first `compat` target
- add `aws-ec2-ssm` as the first `remote_vm` target
- add `gcp-vm` as the second `remote_vm` target
- require later cloud adapters to pass the shared remote-VM conformance harness
  rather than redefining contract suites per provider
- keep `azure-vm` and managed workstations behind later decision gates

### 6. Scenario evidence and operator verification

- expand authenticated, lower-assurance, session-supervisor, migration, and
  remote-workspace scenario coverage as each phase lands
- treat comparison material, operator verification guidance, and rollout docs
  as exit criteria rather than post-hoc documentation

## Phase Gates

### Gate 1: target taxonomy approved

- target kind, assurance class, and workspace transport are frozen as separate
  concepts
- no support claim weakens the current strict Colima contract

### Gate 2: Colima parity complete

- Colima runs unchanged through the new target model
- session and audit state are no longer Colima-shaped at the program level
- session-surface parity is preserved

Current repo status:

- Gate 1 is implemented
- Gate 2 is implemented
- later gates remain planning targets until their code and evidence land

### Gate 3: compatibility target certified

- `docker-desktop` is feature-flagged, explicitly `compat`, and backed by
  target-aware diagnostics
- deterministic target-selection, state-routing, and fail-closed behavior are
  proven under repo-required tests
- rollback to the strict Colima path is documented and operator-verifiable
- the canonical support matrix and validation-host evidence bound the support
  claim for each published host combination
- Linux and Windows support claims remain limited to what the evidence proves

### Gate 4: first remote VM preview

- remote workspace materialization is explicit and auditable
- the remote target uses reviewed brokered access and does not require inbound
  public SSH
- the shared remote-VM conformance harness stays authoritative for the preview
- the support boundary remains preview-only and is reflected in canonical
  matrices plus rollout guidance
- shared auth/bootstrap, validation-host support matrices, and scenario
  evidence are in place

### Gate 5: second remote VM on the same contract

- the second cloud provider fits the same control-plane and audit model with
  limited provider-specific delta
- the unchanged shared conformance harness and canonical matrices still bound
  the support claim

### Gate 6: later expansion decision

- demand and support load justify whether `azure-vm` or managed workstations
  become funded follow-on work

## Program Non-Goals

- automatic backend fallback
- a flat backend abstraction that hides real provider and runtime differences
- treating `compat` targets as equivalent to the current strict Colima path
- Kubernetes-backed execution modes in this program
- folding managed workstations into the same target class as raw remote VMs
- Linux or Windows `strict` parity claims before the same guarantees are
  implemented and validated

## Maintenance Expectations

- update [ROADMAP.md](../ROADMAP.md) when direction, support tiers, or
  non-goals change
- update this document when sequencing, target order, or review gates change
- update [docs/implement-first-delivery-plan.md](implement-first-delivery-plan.md)
  when the active slice or immediate next slice changes
- treat phase-owner lanes in the planning docs as distinct Codex-agent roles
  by default unless the repo or change explicitly records named human owners
- keep docs, support claims, and automated evidence aligned in the same change
  before new target claims are treated as supported
