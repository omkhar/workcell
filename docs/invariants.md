# Security Invariants

These invariants define the safe path. The priority order in the repository
only applies after these constraints are satisfied.

## 1. Host secrets stay outside the default trust boundary

On the managed path, Workcell does not pass through:

- host home directories
- keychains or browser profiles
- git credential helpers
- `docker.sock`
- SSH, GPG, or provider agent sockets
- host provider-home state such as `~/.codex`, `~/.claude`, `~/.copilot`,
  `~/.config/github-copilot`, `~/.cache/github-copilot`, or `~/.gemini`

Reusable auth enters the session only through explicit injection-policy inputs.
The GitHub Copilot CLI adapter preserves the same invariant for host Copilot
provider state (`~/.copilot`, `~/.config/github-copilot`,
`~/.cache/github-copilot`), host keychains, `GH_TOKEN`, `GITHUB_TOKEN`, and
ambient `gh auth token` fallback by accepting only `copilot_github_token`
through the reviewed host-side staging path, removing the original token file
from direct runtime mounts, moving it through a temporary handoff mount outside
provider state and a transient runtime handoff file, re-execing the entrypoint
without the token in its environment, and exporting it as
`COPILOT_GITHUB_TOKEN` only for the managed child after the wrapper unlinks the
handoff file. The
planned Google Antigravity CLI adapter must preserve it for host Google account
caches, browser profiles, keychains, host homes, and provider caches. Current
releases do not support `--agent antigravity`.

## 2. Writes stay inside the intended workspace

The selected workspace is the durable writable mount. Provider homes are
session-local state inside the runtime. Workcell's host-side staging roots under
`~/Library/Caches/colima/workcell-host-inputs` and
`~/Library/Caches/colima/workcell-shadow` are mounted into the managed Colima VM
read-only so the runtime can consume reviewed injection bundles and masked
workspace control-plane snapshots without widening durable write access.
Host-side publication remains a separate action.

## 3. Repo policy must not silently widen trust

Repo-local control-plane files are masked on the safe path and imported into
provider homes as reviewed inputs. The masked set is the provider files
`AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, `.mcp.json`,
`.github/mcp.json`, and `.github/copilot-instructions.md`; the provider,
Copilot, and IDE directories `.codex/`, `.claude/`, `.copilot/`,
`.gemini/`, `.github/instructions/`, `.github/copilot/`,
`.github/hooks/`, `.github/agents/`, `.github/skills/`, `.agents/skills/`,
`.vscode/`, `.idea/`, `.cursor/`, and `.zed/`; and git execution-control paths (`hooks`, `config`,
`config.worktree`, and `worktrees` for the workspace repo and its
submodules). The workspace should not be able to quietly take over the
runtime control plane.

The Copilot adapter explicitly accounts for Copilot-specific repo control-plane
files such as `.github/copilot-instructions.md`, `.github/instructions/**`,
`.github/mcp.json`, `.github/copilot/settings*.json`, and repo-local skill
and hook directories by masking them on the safe path and launching Copilot with custom
instructions disabled while blocking skill/dynamic-retrieval overrides. The
planned Antigravity adapter must do the same for Antigravity-specific settings,
plugin, MCP, hook, and instruction files before it can claim support.

## 4. Network posture is explicit

`strict`, `development`, `build`, and `breakglass` are distinct runtime
profiles. Workcell
does not rely on provider prompts to describe network posture after the fact.

`strict` sets `NETWORK_POLICY=allowlist`, and on the `colima` target the
launcher enforces that as a fail-closed, dual-stack, default-deny egress
firewall: `iptables`/`ip6tables` rules in the VM's `DOCKER-USER` chain ACCEPT
only the reviewed `host:port` allowlist and then `DROP` everything else, and the
helper aborts rather than run without IPv6 (`ip6tables`) containment. Only the
colima target applies this per-session allowlist; other targets rely on their
own network controls, and the launch summary states which with an
`egress_enforcement=` label. Operators may extend or tighten the allowlist only
through the reviewed injection-policy `[network]` surface
(`allow_endpoints`/`deny_endpoints`), which can never disable the default or
change `NETWORK_POLICY`. See [docs/egress-policy.md](egress-policy.md).

## 5. Destructive or trust-widening actions need defense in depth

The runtime boundary is primary, but Workcell also uses provider-side defenses
where they help:

- Codex requirements and rules
- Claude reviewed settings and Bash hook
- Gemini managed settings and trusted-folder seeding

These are guardrails, not substitutes for the runtime boundary.

## 6. Lower-assurance paths are labeled

Examples:

- `--agent-autonomy prompt`
- `--cache-profile standard`
- `development`
- package mutation inside a mutable container
- `--allow-control-plane-vcs`
- `--allow-arbitrary-command`
- `breakglass`
- host-side debug or transcript capture
- any Copilot or future Antigravity telemetry, OpenTelemetry, or content-capture
  enablement

Workcell records those choices in launch or runtime state instead of implying
they are equivalent to the default path.

## 7. Autonomous runs remain auditable

The launcher keeps durable host-side audit metadata for real sessions. Full
debug logs, file traces, and transcripts are separate explicit choices rather
than ambient defaults.

## Profile expectations

| Profile | Expected posture |
|---|---|
| `strict` | default provider lane; reviewed mounts, explicit network posture, repo control-plane masking |
| `strict --container-mutability readonly` | strongest managed lane; package-manager writes blocked |
| `development` | managed interactive lane; same boundary and masking as `strict` with managed non-provider command execution and broader dependency egress |
| `build` | broader egress for image preparation and dependency refresh |
| `breakglass` | explicit higher-trust lane requiring acknowledgement |

## Non-goals

Workcell does not claim:

- that provider hooks or rules are the primary boundary
- that host-native GUIs are equivalent to Tier 1
- that release provenance proves the full local macOS boundary on its own
