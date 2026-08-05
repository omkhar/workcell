# GitHub Copilot CLI Adapter

This adapter owns the Workcell GitHub Copilot CLI baseline. It runs the pinned
CLI with session-local provider state. The VM and container runtime boundary is
the primary control.

## Auth methods

- `copilot_github_token` credential key — the only Copilot auth input
  (`internal/adapters/data.go`; Copilot sets `sharedCredentialsEnabled: false`,
  so shared GitHub CLI state is deliberately excluded).
- An authenticated start does not copy the staged token to provider state. The
  entrypoint moves it to a temporary runtime file and unlinks the mounted file.
- The entrypoint uses `exec` to replace itself without the token environment.
  It stays PID 1 without Docker `--init`. Thus, `/proc/1/environ` does not
  contain the token.
- The provider wrapper unlinks the runtime file. It exports the token only to
  the managed Copilot child as `COPILOT_GITHUB_TOKEN`.
- Workcell removes the original staged token and its direct-mount copy. It does
  not copy the token to `COPILOT_HOME`.
- The adapter does not use host GitHub CLI state or host Copilot state. It does
  not use host keychains, `GH_TOKEN`, `GITHUB_TOKEN`, or ambient `gh auth token`.

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
- `reject_unsafe_copilot_args` blocks `init`, `login`, `logout`, MCP, plugin,
  skill, update, and other control-plane subcommands.
- It also blocks unsafe tool, URL, directory, instruction, remote, share,
  worktree, and autonomy flags.
- These rules apply in all modes, including `breakglass`. Breakglass changes
  the container posture. It does not change the provider unsafe-flag policy.
- The default path blocks plugins, MCP, custom agents, hooks, skills, dynamic
  retrieval, and remote sessions. A separate review unit must add each surface.
- Any Copilot telemetry, OpenTelemetry, or content-capture enablement is a
  lower-assurance, acknowledged path, not a default (`docs/invariants.md` §6).

## See also

- [../README.md](../README.md) — adapter index and common contract
- [../../docs/adapter-control-planes.md](../../docs/adapter-control-planes.md)
- [../../docs/invariants.md](../../docs/invariants.md)
- [../../docs/extending-adapters.md](../../docs/extending-adapters.md) — worked
  contributor examples
