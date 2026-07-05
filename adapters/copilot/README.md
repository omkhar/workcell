# GitHub Copilot CLI Adapter

This adapter owns the Workcell-managed GitHub Copilot CLI baseline. It runs a
pinned Copilot CLI binary against session-local provider state; the runtime
VM-plus-container boundary stays primary.

## Auth methods

- `copilot_github_token` credential key — the only Copilot auth input
  (`internal/adapters/data.go`; Copilot sets `sharedCredentialsEnabled: false`,
  so shared GitHub CLI state is deliberately excluded).
- For auth-required launches the staged token is not seeded into provider state.
  Instead it flows through a scrubbing handoff
  (`runtime/container/entrypoint.sh`, `runtime/container/provider-wrapper.sh`):
  the entrypoint moves the token into a transient runtime handoff file, unlinks
  the mounted file, and re-execs without the token in its environment while
  staying PID 1 (no Docker `--init`) so `/proc/1/environ` is scrubbed; the
  provider wrapper then unlinks the handoff file and exports the value as
  `COPILOT_GITHUB_TOKEN` only for the managed Copilot child process. The original
  staged token file and its direct-mount copy are removed from direct runtime
  mounts and are not copied into `COPILOT_HOME`.
- The adapter does not use host GitHub CLI state, host Copilot provider state
  (`~/.copilot`, `~/.config/github-copilot`, `~/.cache/github-copilot`), host
  keychains, `GH_TOKEN`, `GITHUB_TOKEN`, or ambient `gh auth token` as auth or
  readiness inputs (`docs/invariants.md` §1).

See [../../docs/injection-policy.md](../../docs/injection-policy.md).

## Managed control-plane files

Copilot ships no repo baseline files under `adapters/copilot/`; its control
plane is entirely session-local. The adapter owns a session-local `COPILOT_HOME`
and `COPILOT_CACHE_HOME`, the token handoff, logs, and the cache/config
directories. In-container reserved session targets: `~/.copilot`,
`~/.copilot/AGENTS.md`, `~/.copilot/logs`, `~/.cache/github-copilot`, and
`~/.config/github-copilot` (`ReservedTargets` in `internal/adapters/data.go`).

## Adapter behavior

- The wrapper sets `COPILOT_HOME=~/.copilot` and
  `COPILOT_CACHE_HOME=~/.cache/github-copilot` and launches with custom
  instructions disabled (`runtime/container/provider-wrapper.sh`).
- Autonomy maps host-side through the reviewed wrapper; shell access is withheld
  by omission from `--available-tools`
  (`docs/adapter-control-planes.md#autonomy-mapping`).
- Repo-local Copilot control-plane files (`.github/copilot-instructions.md`,
  `.github/instructions/**`, `.github/mcp.json`, `.github/copilot/settings*.json`,
  repo-local skill/hook dirs) are masked on the safe path (`docs/invariants.md`
  §3).
- Unsafe-argument policy (`reject_unsafe_copilot_args` in
  `runtime/container/provider-policy.sh`): the wrapper blocks the
  `init`/`login`/`mcp`/`plugin`/`skill`/`update` lifecycle and control-plane
  subcommands and a broad set of trust-widening flags (MCP toolset/config,
  `--allow-all*`, `--allow-tool`/`--allow-url`, `--add-dir`, `--dynamic-retrieval`,
  `--no-custom-instructions`, `--remote`/`--share*`/`--worktree`, `--yolo`,
  bundled short options, and more). These are rejected in **every** mode
  including `breakglass`: `provider-wrapper.sh` re-checks arguments
  (`WORKCELL_WRAPPER_CONTEXT=1`), so `container-smoke.sh` confirms breakglass
  overrides still fail. Breakglass raises the sandbox floor, not the unsafe-flag
  policy.
- Plugin, MCP, custom-agent, hook, skill, dynamic-retrieval, and remote-session
  expansion each stay blocked on the default path until a separate Workcell
  review unit and validation evidence land.
- Any Copilot telemetry, OpenTelemetry, or content-capture enablement is a
  lower-assurance, acknowledged path, not a default (`docs/invariants.md` §6).

## See also

- [../README.md](../README.md) — adapter index and common contract
- [../../docs/adapter-control-planes.md](../../docs/adapter-control-planes.md)
- [../../docs/invariants.md](../../docs/invariants.md)
- [../../docs/extending-adapters.md](../../docs/extending-adapters.md) — worked
  contributor examples
