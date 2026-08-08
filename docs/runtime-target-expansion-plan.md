# Runtime Target Expansion

This page records the Workcell runtime-target program. It separates shipped
targets, evaluation work, and future targets.

The authoritative support source is
[`policy/host-support-matrix.tsv`](../policy/host-support-matrix.tsv). Read the
complete matrix row before you make a support decision.

## Shipped Target Model

Workcell records these items separately:

- `target_kind` identifies the execution shape.
- `target_provider` identifies the target implementation.
- `target_assurance_class` identifies the boundary class.
- The host-support matrix `status` and `launch` fields control operator use.
- `workspace_transport` identifies how the workspace enters the target.

An assurance class does not give support by itself. For example, a `compat`
target can be supported, preview-only, or unsupported.

## Current Status

| Target and host | Matrix status | Operator launch | Available evidence |
|---|---|---|---|
| `local_vm/colima/strict` on macOS arm64 | `supported` | allowed | live certification |
| `local_compat/docker-desktop/compat` on macOS arm64 | `supported` | allowed | live certification |
| `remote_vm/aws-ec2-ssm/compat` on macOS arm64 | `preview-only` | blocked | broker-plan evidence and a certification lane |
| `remote_vm/gcp-vm/compat` on macOS arm64 | `preview-only` | blocked | broker-plan evidence and a certification lane |
| `local_vm/apple-container/per-session-vm` on macOS 26 arm64 | `preview-only` | blocked | evaluation evidence and evidence from the certification probe |
| Linux amd64 Colima, AWS, and GCP rows | `validation-host-only` | blocked | `trusted-linux-amd64-validator` only |
| Linux amd64 Docker Desktop row | `unsupported` | blocked | none |
| Linux arm64 and Windows target rows | `unsupported` | blocked | none |

The Workcell CLI accepts `colima`, `docker-desktop`, `aws-ec2-ssm`, and
`gcp-vm` as target values. It does not accept `apple-container`, a managed
workstation, or Azure as an operator target.

Workcell defines a managed-workstation contract. Workcell has no
managed-workstation CLI target or host-support matrix row.

The AWS and GCP values expose reviewed dry-run broker plans. The operator
launch path blocks both values. They do not provide supported remote execution.

## Delivered Program Results

Workcell delivered these program results:

- a target-kind model, an assurance-class model, and Workcell-owned target state
- target-aware session and audit records
- shared host-owned authentication and bootstrap diagnostics
- a canonical host-support matrix with fail-closed diagnostics
- a provider-neutral remote-VM contract and conformance harness
- a supported Docker Desktop compatibility target on macOS arm64
- AWS SSM and GCP IAP remote-VM preview plans
- a provider-neutral managed-workstation contract
- an enterprise evidence baseline
- a host-expansion readiness gate
- an Apple `container` evaluation with a fail-closed macOS 26 guard

The detailed delivery record is in
[`runtime-target-phase-plan.md`](runtime-target-phase-plan.md).

## Active Candidate

Phase 13 is the next host-support candidate. The word `candidate` is a planning
label, not a matrix status. The phase will evaluate one exact Linux amd64
`local_compat` combination. It does not create Linux operator support.

Any promotion must include all of these items in one change:

- one exact distribution, distribution version, runtime, and architecture row
- one exact `target_provider` value
- fail-closed launch and diagnostic behavior outside that row
- install, update, rollback, uninstall, and support-bundle procedures
- deterministic repository tests
- live certification on a real operator host
- matrix, operator, and validation documents that contain the same support claim

All current Linux `local_compat` rows stay `unsupported` and `blocked` until a
promotion change meets these requirements.

Workcell must not claim Linux `strict` support until it has an equivalent
dedicated VM plus container boundary. Deterministic tests and live certification
must confirm the strict guarantees.

## Later Sequence

The roadmap records these later phases:

1. Linux arm64 and Raspberry Pi readiness
2. enterprise identity and access
3. signed policy-bundle distribution
4. fleet inventory and centralized audit ingestion
5. regulated-team proof harness and Windows investigation
6. a managed-workstation provider preview, followed by Azure VM work

The first managed-workstation discovery lane is `gcp-cloud-workstations`.
Workcell has not shipped that backend. Phase 19 evaluates `azure-vm` on the
`remote_vm` contract.

## Program Rules

- Keep the host control plane authoritative for policy, diagnostics, and audit.
- Keep one shared boundary and use a thin adapter for each provider.
- Do not use provider configuration as the security boundary.
- Do not use automatic backend fallback.
- Keep `compat` lower assurance than `local_vm/colima/strict`.
- Keep managed workstations separate from raw remote VMs.
- Keep Kubernetes-backed execution outside this program.
- Add support claims only when code, diagnostics, documents, and evidence agree.

## Evidence Sources

- [Support tiers and status terms](support-tiers.md)
- [Runtime target phase record](runtime-target-phase-plan.md)
- [Remote VM contract](remote-vm-contract.md)
- [Managed workstation contract](managed-workstation-contract.md)
- [Host expansion readiness](host-expansion-readiness.md)
- [Apple container evaluation](apple-container-evaluation.md)
- [Validation scenarios](validation-scenarios.md)
- [Roadmap](../ROADMAP.md)
