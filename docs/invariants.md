# Security Invariants

These invariants define the safe path. Repository priorities apply only after
Workcell satisfies these constraints.

## 1. Host secrets stay outside the default trust boundary

The managed path does not pass these host resources to the runtime:

- home directories
- keychains and browser profiles
- Git credential helpers
- `docker.sock`
- SSH, GPG, and provider agent sockets
- provider state such as `~/.codex`, `~/.claude`, `~/.copilot`,
  `~/.config/github-copilot`, `~/.cache/github-copilot`, and `~/.gemini`

Reusable authentication enters a session only through reviewed injection-policy
inputs.

The Copilot adapter accepts only `copilot_github_token`. It does not use host
Copilot state, `GH_TOKEN`, `GITHUB_TOKEN`, a host keychain, or `gh auth token`.
Workcell gives the token only to the managed Copilot child through the reviewed
temporary handoff. It does not retain the token in mounted provider state or in
the entrypoint environment. See
[Onboarding and Authentication](onboarding-and-auth.md#copilot-token-handoff).

Workcell does not support Antigravity. A future adapter must not use host Google
account state, browser profiles, keychains, home directories, or provider
caches.

## 2. Writes stay inside the intended workspace

The selected workspace is the durable writable mount. Provider homes are
session-local runtime state.

Workcell uses these host staging roots:

- `~/Library/Caches/colima/workcell-host-inputs`
- `~/Library/Caches/colima/workcell-shadow`

The managed Colima VM mounts these roots as read-only. These mounts do not give
the runtime durable write access outside the selected workspace. GitHub
publication remains a separate host action.

## 3. Repo policy must not silently widen trust

Workcell masks repository control-plane files on the safe path. It imports only
reviewed inputs into provider homes.

The mask includes these files:

- `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, and `.mcp.json`
- `.github/mcp.json` and `.github/copilot-instructions.md`

The mask includes these directories:

- `.codex/`, `.claude/`, `.copilot/`, and `.gemini/`
- `.github/instructions/`, `.github/copilot/`, `.github/hooks/`,
  `.github/agents/`, and `.github/skills/`
- `.agents/skills/`
- `.vscode/`, `.idea/`, `.cursor/`, and `.zed/`

Workcell also masks Git execution-control paths for the workspace repository and
its submodules. These paths are `hooks`, `config`, `config.worktree`, and
`worktrees`.

The Copilot adapter also masks Copilot settings, instructions, MCP files,
skills, and hooks. It disables custom instructions and blocks skill and dynamic
retrieval overrides.

A future Antigravity adapter must mask its settings, plugins, MCP files, hooks,
and instructions before it can claim support.

## 4. Network posture is explicit

`strict`, `development`, `build`, and `breakglass` are separate modes. Workcell
does not use a provider prompt as a network control.

The `strict` mode sets `NETWORK_POLICY=allowlist`. On Colima, Workcell applies a
fail-closed dual-stack firewall in the VM `DOCKER-USER` chain. It permits only
reviewed `host:port` entries and drops other egress. It stops the launch if it
cannot contain IPv6.

Only Colima applies the per-session allowlist. Other targets use their own
network controls. The launch summary states the result in
`egress_enforcement=`.

The injection policy can add or deny endpoints through `[network]`. It cannot
disable the default policy or change `NETWORK_POLICY`. See
[Network egress](injection-policy.md#network-egress-network).

The reviewed endpoint inventory is in
[outbound-endpoints.md](outbound-endpoints.md). The machine-readable source is
`policy/hardening-profile.toml`.

## 4a. Container hardening posture is captured and drift-checked

The runtime container uses these controls:

- drops all capabilities
- adds only `SETUID` and `SETGID` for the mutable-mode mapped-user step
- uses `no-new-privileges`
- uses a read-only root file system in `readonly` sessions
- uses hardened `nosuid`, `nodev`, and applicable `noexec` temporary mounts
- sets a process limit
- runs as a mapped nonroot user

`policy/hardening-profile.toml` records this posture. The
`hardening-profile-conformance` invariant checks the launcher. It also rejects
`--privileged` and `seccomp=unconfined`.

## 5. Destructive or trust-widening actions need defense in depth

The runtime boundary is the primary control. Provider controls add defense in
depth:

- Codex requirements and rules
- Claude managed settings and Bash hook
- Gemini managed settings and trusted-folder seed

These controls do not replace the runtime boundary.

## 6. Lower-assurance paths are labeled

Examples include:

- `--agent-autonomy prompt`
- `--cache-profile standard`
- `development`
- package changes in a mutable container
- `--allow-control-plane-vcs`
- `--allow-repo-mcp`
- `--allow-arbitrary-command`
- `breakglass`
- host debug logs or transcript capture
- Copilot or future Antigravity telemetry and content capture

Workcell records these choices in launch or runtime state.

## 7. Autonomous runs remain auditable

The launcher keeps durable host session metadata. Full debug logs, file traces,
and transcripts require separate operator options.

## Profile expectations

| Profile | Expected posture |
|---|---|
| `strict` | Default provider path with reviewed mounts, explicit network controls, and control-plane masks |
| `strict --container-mutability readonly` | Strongest managed path with package-manager writes blocked |
| `development` | Managed interactive path with the same boundary and masks, non-provider commands, and more dependency endpoints |
| `build` | Image preparation path with broader build endpoints |
| `breakglass` | Explicit higher-trust path with dated acknowledgement |

## Non-goals

Workcell does not claim that:

- provider hooks or rules are the primary boundary
- host-native graphical applications are equal to Tier 1
- release provenance proves the local macOS runtime boundary
