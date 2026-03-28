# Expected Controls

## Workspace and mount controls

- exactly one task workspace mount is writable
- no host home mount is present
- no host credential or agent socket mounts are present
- `/` and `$HOME` are rejected as default workspaces
- `/tmp` is mounted `noexec`
- `TMPDIR` points at `/state/tmp`
- `strict` blocks direct native ELF launches from mutable `/workspace` and
  `/state` paths
- `strict` blocks mutable shebang scripts that point at protected real
  runtimes or loaders targeting them

## Codex controls

- `strict` uses `workspace-write`
- `build` uses `workspace-write`
- `breakglass` uses `danger-full-access`
- web search stays disabled in all shipped profiles

## Network controls

- `strict` applies the base egress allowlist
- `build` applies the expanded egress allowlist
- allowlist modes program reviewed IPv4 and, when available, IPv6 destination
  allowlists; if IPv6 enforcement is unavailable, Workcell fails closed instead
  of leaving an unmanaged parallel egress path
- `breakglass` clears the allowlist and documents the loss

## Audit and operator controls

- the wrapper prints the selected Colima profile, runtime profile, and
  workspace path
- the wrapper prints the selected network policy and allowlist
- `strict` rejects cold image builds and explicit rebuild requests
- `build` prints any temporary bootstrap allowlist before rebuilding the
  runtime image
- the wrapper prints the execution path and durable host audit-log location
- each real run appends a durable host-side audit record under the managed
  Colima profile directory, including whether bootstrap egress was applied
- lower-assurance modes are named explicitly
