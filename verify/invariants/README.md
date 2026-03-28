# Invariant Test Plan

The verification harness should answer one question: does the current runtime
still satisfy the documented invariants?

## Minimum checks

1. `strict` starts the selected agent inside the container and not on the host.
2. The container does not receive host auth or control-plane mounts.
3. The Codex profile inside the container defaults to `strict`.
4. The wrapper mounts only the selected workspace.
5. The VM egress policy is applied for `strict` and `build`.
6. `breakglass` is visibly different and must be explicitly selected.
7. Policy files exist and are loadable by the local Codex binary.
8. Broad workspaces such as `/` and `$HOME` are rejected by default.

## Negative checks

The harness should fail if it detects:

- `docker.sock` passthrough
- host `~/.codex` passthrough
- SSH or GPG agent socket passthrough
- host home directory passthrough
- missing egress policy when `strict` is requested
- `breakglass` defaults
