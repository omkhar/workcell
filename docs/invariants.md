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
- host provider-home state such as `~/.codex`, `~/.claude`, or `~/.gemini`

Reusable auth enters the session only through explicit injection-policy inputs.

## 2. Writes stay inside the intended workspace

The selected workspace is the durable writable mount. Provider homes are
session-local state inside the runtime. Host-side publication remains a
separate action.

## 3. Repo policy must not silently widen trust

Repo-local control-plane files such as `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`,
`.codex/`, `.claude/`, and `.gemini/` are masked on the safe path and imported
into provider homes as reviewed inputs. The workspace should not be able to
quietly take over the runtime control plane.

## 4. Network posture is explicit

`strict`, `development`, `build`, and `breakglass` are distinct runtime
profiles. Workcell
does not rely on provider prompts to describe network posture after the fact.

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
