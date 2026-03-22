# Expected Controls

## Workspace and mount controls

- exactly one task workspace mount is writable
- no host home mount is present
- no host credential or agent socket mounts are present
- `/` and `$HOME` are rejected as default workspaces

## Codex controls

- `strict` uses `workspace-write`
- `build` uses `workspace-write`
- `breakglass` uses `danger-full-access`
- web search stays disabled in all shipped profiles

## Network controls

- `strict` applies the base egress allowlist
- `build` applies the expanded egress allowlist
- `breakglass` clears the allowlist and documents the loss

## Audit and operator controls

- the wrapper prints the selected Colima profile, runtime profile, and
  workspace path
- lower-assurance modes are named explicitly
