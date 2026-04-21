# Remote VM Contract

Workcell now carries one canonical preview-only `remote_vm` contract in
[`policy/remote-vm-contract.json`](../policy/remote-vm-contract.json). This is
the provider-neutral control-plane contract that later cloud adapters must
reuse; it does not mean a production cloud backend ships today.

The contract is implemented and exercised through:

- [`internal/remotevm`](../internal/remotevm) for the typed contract,
  canonical fake target, and reusable conformance harness
- [`internal/remotevm/contract_test.go`](../internal/remotevm/contract_test.go)
- [`internal/remotevm/fake_target_test.go`](../internal/remotevm/fake_target_test.go)
- [`internal/remotevm/conformance_test.go`](../internal/remotevm/conformance_test.go)

## Canonical Values

The canonical preview-only contract is:

- `target_kind = remote_vm`
- `target_provider = fake-remote`
- `target_assurance_class = compat`
- `support_boundary = preview-only`
- `runtime_api = brokered`
- `workspace_transport = remote-materialization`
- `access_model = brokered`

Later provider adapters must not fork those control-plane meanings. Provider
work can add provider-specific bootstrap details, but the shared session,
audit, workspace-materialization, and conformance semantics stay the same.

## Workspace Materialization

Remote workspaces are explicit and host-auditable. The canonical fake target
materializes a reviewed source workspace into:

`targets/remote_vm/fake-remote/<target-id>/materializations/<materialization-id>/`

That root contains:

- `workspace/` with the materialized remote workspace contents
- `materialization.json` with the explicit entry manifest

The canonical contract excludes `.git` from the materialized workspace and
records the materialized tree in the manifest instead of treating a live host
mount as the remote target.

## Bootstrap And Session Lifecycle

Target bootstrap is explicit and file-backed:

- `targets/remote_vm/fake-remote/<target-id>/bootstrap/bootstrap.json`

Session records stay on the same Workcell-owned target-state tree:

- `targets/remote_vm/fake-remote/<target-id>/sessions/<session-id>.json`

The shared audit log for that target is:

- `targets/remote_vm/fake-remote/<target-id>/workcell.audit.log`

Required audit events are:

- `workspace_materialized`
- `bootstrap_ready`
- `session_started`
- `session_finished`

The canonical session contract uses:

- `workspace_control_plane = host-brokered`
- `status = running` at session start
- `status = exited` at session finish
- `assurance = compat-preview-brokered`

## Reuse Rule For Later Providers

Later `remote_vm` providers such as `aws-ec2-ssm` and `gcp-vm` must implement
the shared [`remotevm.ConformanceTarget`](../internal/remotevm/fake_target.go)
interface and pass the shared
[`remotevm.RunConformance`](../internal/remotevm/conformance.go) harness
without redefining a provider-specific contract suite.

That reuse rule is the main Phase 5 boundary: provider work starts only after
this provider-neutral contract is fixed, documented, and proven
deterministically in-repo.
