# Managed Workstation Contract

## Contract Status

This page records the Phase 10 `managed_workstation` contract. Workcell has no
managed-workstation CLI target or host-support matrix row.

The first provider candidate is `gcp-cloud-workstations`. The candidate does
not create an operator support claim.

## Target Identifiers

A provider must use `target_kind=managed_workstation`. It must use an exact
provider-specific `target_provider` value.

A provider change must select an assurance class from
[Support Tiers and Status Terms](support-tiers.md). The same change must provide
the required evidence.

Managed workstations are not raw VMs. A provider must not use `remote_vm` only
because it offers an SSH access path.

## Lifecycle Requirements

The provider must expose its capabilities and support status before launch.
Workcell must verify the host, provider tools, and target identity.

The provider change must name the required operator acknowledgement. Workcell
must require that acknowledgement.

The provider change must name the workspace transport. Workcell must use that
transport.

Workcell must record session and audit data in Workcell-owned target state. The
provider must define stop, recovery, and removal operations.

## Security Requirements

Workcell's host control plane must control policy, diagnostics, and audit.
Provider files and workspace rules must not control the Workcell boundary.

The provider change must define the exact staged credential names. Workcell
must not use ambient provider authentication.

Workcell must not mount host credential stores, agent sockets, Docker sockets,
or Workcell control-plane state.

Audit records must identify the session, target, workspace transport, assurance
class, policy inputs, and identity source. They must record each downgrade or
recovery operation.

## Evidence Gate

The first provider row must be `preview-only` and `blocked`. A provider change
must add the implementation and fail-closed diagnostics.

The change must add a deterministic test target and shared conformance tests.
It must add exact matrix data, rollback instructions, and operator documents.

Maintainers must complete live certification before they sign a support-claim
change. This rule applies when host or provider behavior controls the claim.

The change must keep repository tests independent of live provider state.
Reviewers must reject each claim that exceeds its evidence.

## Authoritative Sources

- [Runtime Target Expansion](runtime-target-expansion-plan.md)
- [Runtime Target Phase Record](runtime-target-phase-plan.md)
- [Host-Support Matrix](../policy/host-support-matrix.tsv)
- [Repository Working Agreement](../AGENTS.md)
