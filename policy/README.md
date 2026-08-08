# Policy Core

The `policy/` directory contains the shared Workcell contracts.

Each adapter and workflow must preserve these rules:

- The runtime boundary is the primary control.
- The safe path excludes control sockets, unapproved secrets, ambient
  authentication state, and direct provider-state mounts. It permits only an
  approved staged credential handoff.
- Each network mode is explicit.
- `breakglass` is narrow and has lower assurance.
- Hosted controls outside Git need an explicit policy record.

Provider-native configuration belongs in `adapters/`, not in this directory.

`hardening-profile.toml` records container controls and fixed endpoint literals.
The controls include dropped capabilities, `no-new-privileges`, a read-only root
file system, hardened temporary mounts, a PID limit, and a mapped non-root user.

The `hardening-profile-conformance` check compares the policy artifact with
launcher source functions. See the [outbound endpoint inventory](../docs/outbound-endpoints.md)
for the human-readable fixed sets and their limits.
