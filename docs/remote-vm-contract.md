# Remote VM Contract

## Authority and Status

[`policy/remote-vm-contract.json`](../policy/remote-vm-contract.json) defines
the `remote_vm` contract. Each remote VM provider must use this contract.

The contract does not create operator support. The host-support matrix gives
the support and launch status.

## Fixed Contract Fields

| Field | Required value |
|---|---|
| `target_kind` | `remote_vm` |
| `target_assurance_class` | `compat` |
| `support_boundary` | `preview-only` |
| `runtime_api` | `brokered` |
| `workspace_transport` | `remote-materialization` |
| `access_model` | `brokered` |

The policy artifact uses `target_provider=fake-remote`. A provider-specific
contract changes only this provider value. A separate access plan holds the
provider broker data.

## Deterministic Contract Evidence

The repository uses `remotevm.FakeTarget` for deterministic contract tests. The
AWS and GCP conformance fixtures also use this fake target.

The fake target copies the source workspace into its local target root. It
excludes `.git` and records each copied entry in `materialization.json`.

This process is contract evidence only. It does not copy data to AWS or GCP.
It does not start a Workcell remote session.

## Workspace Transfer Requirements

A live provider must copy the source workspace into its provider target root.
It must exclude `.git` and record each copied entry in
`materialization.json`.

Workcell must not use a live host mount for a remote workspace. The transfer
must be explicit and auditable.

## Session and Audit Requirements

The contract requires `bootstrap.json`, session records, and a target audit
log. It requires these audit events:

- `workspace_materialized`
- `bootstrap_ready`
- `session_started`
- `session_finished`

The fake target tests `status=running`, `status=exited`, and
`assurance=compat-preview-brokered`. These are deterministic contract results,
not live provider results.

## Provider Conformance Requirements

Each provider must implement `remotevm.ConformanceTarget`. Each provider must
pass `remotevm.RunConformance`.

A provider must not define a separate contract suite. A provider must limit
additions to its provider-specific broker and bootstrap data.

AWS and GCP do not implement live Workcell remote execution. Their preview
documents define tools, broker tests, certification scope, and rollback:

- [AWS EC2 SSM Preview](aws-ec2-ssm-preview.md)
- [GCP VM Preview](gcp-vm-preview.md)
