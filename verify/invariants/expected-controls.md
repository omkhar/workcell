# Expected Controls

## Workspace and mounts

- Exactly one task workspace mount is writable.
- The runtime mounts no host home, credential store, or agent socket.
- Workcell rejects `/` and the operator home as default workspaces.
- The runtime mounts `/tmp` with `noexec`.
- `TMPDIR` points to `/state/tmp`.
- Strict mode blocks native ELF files from mutable workspace and state paths.
- Strict mode blocks mutable scripts that select protected runtimes or loaders.

## Codex

- Strict, development, and build profile files set `workspace-write`.
- `breakglass` uses `danger-full-access`.
- The managed wrapper disables the native sandbox inside the Workcell boundary.
- The runtime uses the signed, checksum-pinned code-mode host.
- All shipped profiles disable web search.
- The launcher reports the Codex native sandbox as disabled. The VM, container,
  and syscall shim supply the effective boundary.

## Network

- Strict mode uses the base allowlist.
- Development and build modes add their versioned endpoint sets.
- Colima allowlist modes program IPv4 and IPv6 `DOCKER-USER` rules.
- Workcell stops the operation if it cannot apply IPv6 rules.
- `breakglass` clears the profile-wide allowlist and reports the loss.
- Docker Desktop reports `egress_enforcement=none`.

## Audit and operator output

- The launcher prints the profile, runtime mode, workspace, and target.
- The launcher prints the network policy, endpoint list, and enforcement.
- Strict mode prepares a missing or stale runtime image.
- An explicit rebuild also requires `--prepare`.
- When a rebuild uses bootstrap egress, build mode prints the temporary
  bootstrap endpoint set.
- The launcher prints the execution path and audit-log path.
- When Workcell starts a non-dry-run session, it appends a host-owned audit
  record.
- Finalization attempts to sign a chained session head.
- `session verify` fails closed when the chain, seal, or public key is invalid.
- Operator output identifies each lower-assurance mode.

The Colima egress rules apply to one profile, not one session.
