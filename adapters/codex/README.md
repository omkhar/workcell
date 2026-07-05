# Codex Adapter

The Codex adapter maps the shared Workcell boundary into Codex-native controls.
It re-seeds a session-local Codex home from reviewed baselines on every launch;
the runtime VM-plus-container boundary stays primary and Codex config is defense
in depth, not the boundary.

## Auth methods

- `codex_auth` credential key — a direct staged `auth.json` seeded to
  `~/.codex/auth.json` (`internal/adapters/data.go`).
- `codex-home-auth-file` resolver — a reviewed host-side Codex auth-file reuse
  path; still host-side preprocessing only, not Keychain passthrough
  (`internal/authresolve/resolve_credential_sources.go`).
- Shared GitHub CLI (`github_hosts`, `github_config`) and SSH inputs apply
  because Codex opts into shared credentials
  (`sharedCredentialsEnabled: true` in `internal/adapters/data.go`).

See [../../docs/injection-policy.md](../../docs/injection-policy.md) and
[../../docs/provider-bootstrap-matrix.md](../../docs/provider-bootstrap-matrix.md).

## Managed control-plane files

Repo baselines under `adapters/codex/` (paths relative to this directory):

- `.codex/config.toml`: managed base configuration seeded into the session-local
  Codex home as `~/.codex/config.toml`. The base sets no `sandbox_mode`, so every
  sandbox decision is profile-scoped.
- `.codex/{strict,development,build,breakglass}.config.toml`: Codex 0.134+
  profile-v2 layer files. The launcher selects one with `--profile <name>`;
  Codex layers it on top of the base config. Each layer carries the per-profile
  `sandbox_mode`/`approval_policy`.
- `.codex/AGENTS.md`: managed agent guidance.
- `.codex/rules/default.rules`: managed Codex execpolicy ruleset. One ruleset
  ships today; the directory shape lets future overlays drop in alongside it.
- `.codex/agents/`: managed sub-agent guidance.
- `managed_config.toml`: workcell-side managed-mode TOML consumed by the launcher.
- `requirements.toml`: workcell-side adapter requirements contract.
- `mcp/config.toml`: MCP server config (no live MCP defaults ship in the baseline).

In-container reserved session targets: `~/.codex/{config.toml,auth.json,`
`AGENTS.md,managed_config.toml,requirements.toml,agents,rules,mcp}`
(`ReservedTargets` in `internal/adapters/data.go`).

## Adapter behavior

- The provider wrapper sets `CODEX_HOME=~/.codex`, re-seeds the baselines into the
  session-local Codex home, and imports repo-local control-plane files only as
  masked, reviewed layers (`runtime/container/provider-wrapper.sh`,
  `runtime/container/home-control-plane.sh`).
- Codex's own Linux `workspace-write` sandbox is pinned off on the managed path:
  the current Codex Linux sandbox needs unprivileged user namespaces that are
  unavailable inside the Tier 1 container.
- `~/.codex/rules/` is read-only by default; it becomes a session-local writable
  copy only in explicit lower-assurance cases (see
  [../../docs/adapter-control-planes.md](../../docs/adapter-control-planes.md#codex-rules-mutability)).
- Unsafe-argument policy (`reject_unsafe_codex_args` in
  `runtime/container/provider-policy.sh`): the wrapper blocks
  `--dangerously-bypass-approvals-and-sandbox`, `--full-auto`, `-a`/
  `--ask-for-approval`, `--add-dir`, `--search`, `--remote`, `--enable`/
  `--disable`, `--cd`, `--sandbox danger-full-access`, reserved `--config`
  overrides, off-mode `--profile` values, and `app`/`app-server`/`cloud`/`mcp`/
  `sandbox` subcommands outside the managed GUI path. `breakglass` exempts these.
- Final branch publication stays on the host through `workcell publish-pr`, not
  from inside the container session.

Codex CLI is Tier 1 when it runs fully inside the bounded runtime. App support is
only valid once it becomes a client of the same bounded executor.

## See also

- [../README.md](../README.md) — adapter index and common contract
- [../../docs/adapter-control-planes.md](../../docs/adapter-control-planes.md)
- [../../docs/invariants.md](../../docs/invariants.md)
- [../../docs/extending-adapters.md](../../docs/extending-adapters.md) — worked
  contributor examples
