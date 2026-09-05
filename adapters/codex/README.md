# Codex Adapter

The Codex adapter maps the Workcell runtime to Codex controls. At each start,
it builds a session-local Codex home from reviewed baselines. The VM and
container runtime boundary is the primary control. Codex configuration gives
additional protection.

## Auth methods

- `codex_auth` stages `auth.json` at `~/.codex/auth.json`. See
  `internal/adapters/data.go`.
- `codex-home-auth-file` reads the reviewed host Codex auth file. It does not
  pass a keychain to the runtime.
- Codex accepts the shared `github_hosts`, `github_config`, and SSH inputs.

See [../../docs/injection-policy.md](../../docs/injection-policy.md) and
[../../docs/provider-bootstrap-matrix.md](../../docs/provider-bootstrap-matrix.md).

## Managed control-plane files

Repo baselines under `adapters/codex/` (paths relative to this directory):

- `.codex/config.toml`: managed base configuration seeded into the session-local
  Codex home as `~/.codex/config.toml`.
  It keeps the coordinator model and effort selected by the operator or Codex.
  Delegated work defaults to `gpt-5.6-terra` with `medium` effort.
  Explicit user model and effort choices still take precedence.
  It limits each session to three concurrent child threads.
  Its Codex-only instructions select available child models and effort by task risk.
  The base sets no `sandbox_mode`, so every sandbox decision is profile-scoped.
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
- Workcell turns off the Codex Linux `workspace-write` sandbox on the managed
  path. That sandbox needs unprivileged user namespaces. The Tier 1 container
  does not provide them.
- Workcell installs the matching signed code-mode host for shell tool calls.
- `~/.codex/rules/` is read-only by default; it becomes a session-local writable
  copy only in explicit lower-assurance cases (see
  [../../docs/adapter-control-planes.md](../../docs/adapter-control-planes.md#codex-rules-mutability)).
- `reject_unsafe_codex_args` denies unapproved subcommands by default. See
  `runtime/container/provider-policy.sh`.
- The allowed set contains the reviewed read-only and session commands. The
  fixture `tests/fixtures/codex-subcommands.txt` binds this set to the Codex
  version.
- The wrapper blocks `agents`, `queue`, `migrate-rollouts`, plugins, cloud,
  remote-control, `exec-server`, update, sandbox, debug, and unclassified
  subcommands.
- It also blocks MCP servers, `responses-api-proxy`, and `stdio-to-uds`.
- The wrapper permits `app-server` only as a bare start without arguments.
- The wrapper also blocks unsafe autonomy, network, profile, and configuration
  flags. This includes changes to guarded configuration namespaces.
- These rules apply in all modes, including `breakglass`. Breakglass changes
  the container posture. It does not change the provider unsafe-flag policy.
- Final branch publication stays on the host through `workcell publish-pr`, not
  from inside the container session.

Codex CLI is Tier 1 when it runs fully in the bounded runtime. An app can be
Tier 2 only when it is a client of the same bounded runtime.

## See also

- [../README.md](../README.md) — adapter index and common contract
- [../../docs/adapter-control-planes.md](../../docs/adapter-control-planes.md)
- [../../docs/invariants.md](../../docs/invariants.md)
- [../../docs/extending-adapters.md](../../docs/extending-adapters.md) — worked
  contributor examples
