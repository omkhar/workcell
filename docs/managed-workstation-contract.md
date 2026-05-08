# Managed Workstation Contract

This page records the Phase 10 provider-neutral `managed_workstation` contract.
It is a contract and discovery gate, not a shipped backend or support claim.

Current status:

- no `--target` value selects a managed workstation today
- no host-support matrix row is promoted for managed workstations today
- no Workcell-managed cloud resource, workstation pool, or provider credential
  path ships in this phase
- the first provider discovery lane is `gcp-cloud-workstations`
- `azure-vm` remains queued as the next raw `remote_vm` provider lane

## Target Model

Managed workstations are a separate target kind:

- `target_kind = managed_workstation`
- `target_provider` is provider-specific, with `gcp-cloud-workstations` as the
  first discovery lane
- `target_assurance_class` must be `compat` or lower until stronger evidence is
  proven in the same change as a later provider preview
- target state belongs under Workcell-owned target roots, for example
  `targets/managed_workstation/<provider>/<target-id>/`
- the host Workcell control plane remains authoritative for policy,
  diagnostics, audit records, and operator acknowledgements

A managed workstation is not a raw VM. Provider implementations must not reuse
the `remote_vm` target kind just because a provider exposes an SSH-like access
path.

## Lifecycle Contract

Later provider implementations must make these lifecycle stages explicit and
auditable:

- discover provider capabilities and the support boundary before launch
- verify host support, provider tools, target identity, and operator
  acknowledgement without silent fallback
- materialize or bind the workspace through a reviewed provider-specific
  transport instead of a live host mount
- prepare provider bootstrap state without mounting host credential stores,
  keychains, SSH agents, GPG agents, Docker sockets, or Workcell control-plane
  state
- start the managed workstation session through a brokered or provider-managed
  access path
- write session and audit metadata under Workcell-owned target state
- stop, recover, and remove provider state through documented operator actions

The provider may own workstation infrastructure. Workcell owns the local
decision record, policy evaluation, operator-facing support status, and audit
trail.

## Identity, Policy, And Audit

Managed-workstation providers must model identity separately from raw VM
providers:

- user identity, machine identity, provider project/account identity, and
  breakglass identity must be visible in the contract before support promotion
- provider config, prompt files, workspace rules, or ambient cloud CLIs are not
  the security boundary
- organization policy must enter through reviewed Workcell policy or a future
  signed policy bundle, not through workspace-controlled state
- audit records must connect session id, target kind, provider, target id,
  workspace transport, assurance class, policy inputs, identity source, and any
  downgrade or recovery path

No provider preview may claim support until its docs and tests show how these
fields are produced and retained.

## Comparison

| Target kind | Boundary shape | Workspace path | Support posture |
|---|---|---|---|
| `local_vm` | dedicated local VM plus hardened container | local reviewed mount | strict on supported macOS arm64 Colima |
| `local_compat` | lower-assurance local runtime compatibility path | local reviewed mount | compat where matrix rows allow it |
| `remote_vm` | raw VM reached through reviewed brokered access | explicit remote materialization | preview-only for AWS SSM and GCP IAP |
| `managed_workstation` | provider-managed workstation lifecycle | provider-specific materialization or binding | contract only until a provider preview lands |

## Evidence Gate

Before any managed-workstation backend ships, the implementation change must add:

- a deterministic fake target or fixture strategy when provider state is not
  needed
- shared conformance expectations for lifecycle, workspace, identity, policy,
  audit, support boundary, and recovery behavior
- support-matrix rows, docs, diagnostics, rollback guidance, and operator
  verification material in the same change as any support promotion
- certification-only live smoke when provider state or credentials are required
- a quality review pass that removes speculative abstraction, duplicate contract
  language, unsupported claims, and dead code before validation is considered
  complete

If those items cannot be proven, the provider lane remains discovery work rather
than a supported target.
