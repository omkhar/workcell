# Invariant Test Plan

The invariant suite checks the documented runtime and control-plane contract.

## Positive checks

The suite verifies these conditions:

1. The provider starts in the container.
2. The runtime receives only approved host mounts.
3. The safe path rejects direct mounts of host authentication roots and all
   host sockets. It permits only approved staged credential files.
4. The selected workspace is the only general writable host mount. An approved
   temporary credential handoff is one narrow exception. An approved non-secret
   persistent-cache plane is another exception.
5. Managed Colima modes generate the network-policy plan for the selected profile.
6. The launch output labels `breakglass` as lower assurance.
7. Provider control-plane files are present and usable.
8. Workcell rejects unsafe broad workspaces.

The dry-run mount checks cover the fixed forbidden-path subset in
`policy/forbidden-host-paths.toml`. Checks for provider-state sources also require a
host-side validation lane.

## Negative checks

The suite fails when it detects these conditions:

- A Docker socket passes into the runtime.
- A host home passes into the runtime.
- A direct host provider-state mount passes into the runtime.
- An SSH or GPG agent socket passes into the runtime.
- A generated allowlist plan for a managed Colima mode has no egress rules.
- A launch selects `breakglass` without explicit operator input.
