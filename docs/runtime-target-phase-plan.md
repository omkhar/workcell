# Runtime Target Phase Record

This page records the result of each runtime-target phase. It is not a support
matrix. Use
[`policy/host-support-matrix.tsv`](../policy/host-support-matrix.tsv) for the
current support decision.

## Completion Rule

A phase can change a support claim only when the same change includes:

- the implementation and fail-closed diagnostics
- the matrix and operator documents
- deterministic repository tests
- live certification when host or provider behavior controls the claim
- an explicit rollback procedure

Repository validation must not require live cloud state or real provider
credentials. Live evidence belongs in a certification lane.

## Delivered Phases

### Phase 0: Validation substrate

Workcell separates repository-required scenarios from certification-only
scenarios. `./scripts/validate-repo.sh` does not require live Colima or cloud
state.

### Phase 1: Session platform and target model

Workcell ships detached session control, inspection, logs, timeline, diff, and
export. Session records include `target_kind`, `target_provider`,
`target_assurance_class`, and `workspace_transport`.

### Phase 2: Target state and Colima driver

Session, audit, and lock state use Workcell-owned target roots. Compatibility
reads preserve records that use the old Colima fields. Colima remains the
default target.

### Phase 3: Shared authentication and bootstrap

The launcher and operator tools use the reviewed host-owned authentication
path. Workcell ships authentication status, credential explanations, and the
provider bootstrap matrix.

### Phase 4: Host-support matrix

Workcell ships the canonical host-support matrix and target-aware diagnostics.
Unsupported combinations fail closed. Linux amd64 remains a validation host,
not an operator host.

### Phase 5: Remote-VM contract

Workcell ships a provider-neutral `remote_vm` contract, a fake target, and a
shared conformance harness. The contract requires explicit workspace
materialization and brokered access.

### Phase 6: Docker Desktop compatibility target

Workcell supports `local_compat/docker-desktop/compat` on macOS arm64. The
target has deterministic tests and live certification. It has lower assurance
than `local_vm/colima/strict`. Workcell does not use silent fallback.

### Phase 7: AWS EC2 SSM preview

Workcell ships deterministic selection, diagnostics, state routing, and a
reviewed AWS SSM broker plan. The macOS arm64 matrix row is `preview-only` and
`blocked`. Live AWS use stays in its certification lane. The reviewed access
model does not permit inbound public SSH.
The certification lane validates broker access to the target. It does not
start or certify a Workcell-managed remote session.

### Phase 8: GCP VM preview

Workcell ships deterministic selection, diagnostics, state routing, and a
reviewed GCP IAP broker plan. The macOS arm64 matrix row is `preview-only` and
`blocked`. Live GCP use stays in its certification lane. The certification
lane requires a target that has no external NAT IP.
The certification lane validates broker access to the target. It does not
start or certify a Workcell-managed remote session.

### Phase 9: Expansion decision

Workcell selected managed-workstation contract work before a third raw VM
provider. Phase 19 puts Azure work after the first managed-workstation provider
preview.

### Phase 10: Managed-workstation contract

Workcell defines `managed_workstation` as a separate target kind. The first
discovery lane is `gcp-cloud-workstations`. No provider backend or operator
target shipped in this phase.

### Phase 11: Enterprise evidence baseline

Workcell ships an evidence map for architecture, threats, support boundaries,
provenance, release controls, audit, and validation. Its control mappings are
evaluation aids. They are not certification claims.

### Phase 12: Host-expansion readiness

Workcell defines the promotion gates for Linux and Windows. No Linux or Windows
operator support shipped in this phase.

## Other Delivered Target Work

### GitHub Copilot CLI parity

Workcell supports GitHub Copilot CLI as a Tier 1 provider adapter. This work
completed before Phase 13. The provider support source is
[`provider-matrix.md`](provider-matrix.md).

### Apple `container` evaluation

Workcell completed the C1 evaluation on Apple Silicon macOS 26. The result was
GO for the technical evaluation. Workcell deferred operator promotion. The target
remains `preview-only` and `blocked`. The CLI does not expose it as a target.
Colima remains the default.

## Planned Phases

### Phase 13: Linux amd64 compatibility candidate

The word `candidate` is a planning label, not a matrix status. Select one
distribution, runtime, and exact `target_provider`. Keep each Phase 13 row
`unsupported` and `blocked`. Do not add a support claim until one change includes
the exact matrix row, host procedures, diagnostics, and rollback. The change
must include repository tests and live host certification.

### Phase 14: Linux arm64 and Raspberry Pi readiness

Evaluate Linux arm64 separately from Linux amd64. Keep Raspberry Pi
`unsupported` and `blocked`. A promotion change must document hardware limits
and rollback. It must also include live certification on a real operator host.

### Phase 15: Identity and access

Define user, machine, service-account, group, and breakglass identity. Connect
the identity record to session and audit events.

### Phase 16: Signed policy bundles

Define a signed and versioned organization policy bundle. Specify precedence,
expiry, rollback, drift detection, and local override rules.

### Phase 17: Fleet inventory and audit ingestion

Add fleet inventory and centralized ingestion for the shipped OCSF audit
records. Preserve the documented privacy and redaction rules.

### Phase 18: Regulated-team proof harness and Windows investigation

Add deterministic negative tests for forbidden mounts, sockets, credential
stores, and workspace policy takeover. Investigate WSL2 and native Windows as
separate targets. Keep Windows unsupported until the Windows promotion change
includes all required evidence.

### Phase 19: Managed workstation preview and Azure return

Add the first managed-workstation provider preview only after its contract,
diagnostics, rollback, and evidence exist. Then evaluate `azure-vm` on the
shared remote-VM contract.

## Status Sources

- [Runtime target expansion](runtime-target-expansion-plan.md)
- [Host expansion readiness](host-expansion-readiness.md)
- [Provider matrix](provider-matrix.md)
- [Requirements and validation](requirements-validation.md)
- [Roadmap](../ROADMAP.md)
