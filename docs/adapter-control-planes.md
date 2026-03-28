# Adapter Control Planes

## Overview

Workcell keeps one shared runtime boundary (dedicated Colima VM plus hardened
inner container) and exposes thin provider adapters instead of pretending every
agent product shares one control plane. Each adapter manages a small set of
files inside the per-session ephemeral home (`/state/agent-home`) that map
Workcell's policy model into the provider's native configuration surface.

At provider launch, `runtime/container/home-control-plane.sh` re-seeds the
provider-facing home from two sources:

- the **immutable adapter baseline** under `/opt/workcell/adapters/` (a
  read-only copy of `adapters/` baked into the container image)
- the **staged operator injection bundle** mounted read-only at
  `/opt/workcell/host-injections` (from an explicit `--injection-policy`)

Mutable provider state such as auth tokens is copied into the ephemeral session
home and is never written back to the adapter baseline. On each new provider
launch within the same container session, the home is re-seeded from the same
immutable sources.

Before seeding adapter-managed files, `runtime/container/home-control-plane.sh`
verifies the relevant adapter baseline hashes against the `runtime_artifacts`
section of the committed `runtime/container/control-plane-manifest.json` that
is baked into the image. The same published manifest also carries a separate
`host_artifacts` section for reviewed host-side launcher inputs such as
`scripts/workcell`, `scripts/lib/trusted-docker-client.sh`,
`scripts/lib/extract_direct_mounts.py`, and
`scripts/lib/render_injection_bundle.py`, but those entries are
release-provenance only and are not runtime-hash-checked inside the container.
Release publication signs and publishes the full manifest so consumers can
audit the reviewed control-plane inputs separately from the broader
build-input manifest.

---

## Control file matrix

The following table covers all files seeded by `seed_codex_home()`,
`seed_claude_home()`, and `seed_gemini_home()` in
`runtime/container/home-control-plane.sh`.

| File / Path | Provider | Safe path status | Breakglass status | Description |
|---|---|---|---|---|
| `~/.codex/AGENTS.md` | Codex | Rendered (baseline + workspace + injection layers) | Same | Provider instruction doc; workspace `AGENTS.md` is imported as a layer |
| `~/.codex/config.toml` | Codex | Session-local writable copy of the baseline; reset on each launch | Same | Seeded from `adapters/codex/.codex/config.toml`; carries the baseline default profile and session-tunable defaults (analytics, history, project markers, etc.), while immutable `managed_config.toml` separately pins the reviewed policy overlay |
| `~/.codex/managed_config.toml` | Codex | Symlink → immutable baseline | Same | Linked to `adapters/codex/managed_config.toml`; admin-enforced strict default profile plus pinned profile sandbox/approval settings, web search modes, and execpolicy rules |
| `~/.codex/requirements.toml` | Codex | Symlink → immutable baseline | Same | Linked to `adapters/codex/requirements.toml`; hard requirements for the adapter including forbidden command patterns |
| `~/.codex/agents/` | Codex | Symlink → immutable baseline agents dir | Same | Linked to `adapters/codex/.codex/agents/` |
| `~/.codex/rules/` | Codex | Symlink → immutable baseline (readonly) or session-local writable copy (session) | Same | Execpolicy rules; see rules mutability section below |
| `~/.codex/mcp/config.toml` | Codex | Symlink → immutable baseline | Same | Linked to `adapters/codex/mcp/config.toml`; ships no live MCP defaults |
| `~/.codex/auth.json` | Codex | Copied from injection credential `codex_auth` if present | Same | Session-local Codex auth; not present if `credentials.codex_auth` is not configured |
| `~/.claude/settings.json` | Claude | Symlink → immutable baseline, or session-local copy with `apiKeyHelper` if `claude_api_key` is injected | Same | Linked to `adapters/claude/managed-settings.json`; contains deny-list permissions and the `PreToolUse` Bash hook. When `claude_api_key` is present, the helper reads the reviewed direct-mounted credential path instead of creating a second session-local key copy. |
| `~/.claude/CLAUDE.md` | Claude | Rendered (baseline + workspace + injection layers) | Same | Provider instruction doc; workspace `AGENTS.md` and `CLAUDE.md` are imported as layers |
| `~/.claude/workcell/` | Claude | Created only when `claude_api_key` credential is injected | Same | Holds the session-local `api-key-helper.sh` script. The helper reads the mounted key file directly, so Workcell no longer drops a second plaintext copy under this directory. |
| `~/.claude/.credentials.json` | Claude | Copied from injection credential `claude_auth` if present | Same | Session-local native Claude auth on Linux; not present if `credentials.claude_auth` is not configured |
| `~/.mcp.json` | Claude | Symlink → immutable `mcp-template.json` (empty), or copied from `claude_mcp` credential if injected | Same | MCP server registry; ships empty by default |
| `~/.gemini/settings.json` | Gemini | Copied (not symlinked) from immutable baseline; mode `0600` | Same, but `breakglass` re-enables Gemini folder trust before launch | Copied from `adapters/gemini/.gemini/settings.json` |
| `~/.gemini/trustedFolders.json` | Gemini | Seeded session-local with `/workspace` trusted; mode `0600` | Same for strict/build; omitted in `breakglass` | Prevents Gemini's restart-only trust prompt inside masked ephemeral sessions while preserving Gemini's own prompt on `breakglass` |
| `~/.gemini/GEMINI.md` | Gemini | Rendered (baseline + workspace + injection layers) | Same | Provider instruction doc; workspace `AGENTS.md` and `GEMINI.md` are imported as layers |
| `~/.gemini/.env` | Gemini | Copied from injection credential `gemini_env` if present | Same | Provider-native env-file auth (API keys, Vertex project settings); Workcell derives Gemini's selected auth type from this file when possible |
| `~/.gemini/oauth_creds.json` | Gemini | Copied from injection credential `gemini_oauth` if present | Same | Cached Gemini OAuth credential |
| `~/.gemini/projects.json` | Gemini | Copied from injection credential `gemini_projects` if present; otherwise seeded with empty `{"projects":{}}` | Same | Gemini CLI project registry |
| `~/.config/gcloud/application_default_credentials.json` | Gemini | Copied from injection credential `gcloud_adc` if present | Same | Supplemental Google ADC for Vertex flows driven by `~/.gemini/.env`; not a standalone Gemini auth mode |
| `~/.config/gh/config.yml` | All | Copied from injection credential `github_config` if present | Same | GitHub CLI config; shared across providers |
| `~/.config/gh/hosts.yml` | All | Copied from injection credential `github_hosts` if present | Same | GitHub CLI auth; shared across providers |
| `~/.ssh/` | All | Seeded from `[ssh]` injection section if configured | Same | SSH config, known_hosts, and identity files; `SSH_AUTH_SOCK` is never forwarded |

### Codex rules mutability

By default (`--codex-rules-mutability readonly`), `~/.codex/rules/` is a
symlink to the immutable adapter baseline. This means execpolicy rules cannot
be amended by the running session.

The rules become session-local writable (a copy, not a symlink) in three cases:

1. The operator explicitly passes `--codex-rules-mutability session`.
2. `--agent-autonomy prompt` is active (promoted automatically).
3. The session has already been downgraded by a package-manager mutation
   (promoted automatically).

In all three cases, the immutable adapter baseline under `adapters/` remains
unchanged. The session-local copy persists only until container exit.

---

## How Workcell flags map to provider behavior

The mapping is applied by `runtime/container/provider-wrapper.sh` before
`exec`-ing the pinned provider runtime: the native provider binary for Codex
and Claude, and the real Node runtime plus the pinned Gemini entrypoint script
for Gemini.

### `--agent-autonomy` flag

| Workcell flag | Codex flag | Claude flag | Gemini flag |
|---|---|---|---|
| `--agent-autonomy yolo` (default) | `--ask-for-approval never` | `--permission-mode bypassPermissions` | `--approval-mode yolo` |
| `--agent-autonomy prompt` | `--ask-for-approval on-request` | `--permission-mode default` | `--approval-mode default` |

Autonomy flags are injected ahead of any user-supplied `--agent-arg` values.
Attempts by the agent to override autonomy flags through provider-native argv
are blocked by `reject_unsafe_<provider>_args` in `provider-policy.sh` before
the exec.

### `--mode` flag (Workcell runtime profile)

The `--mode` flag selects the Workcell runtime profile. It affects the
container and VM posture, not directly the provider binary flags. The profile
is recorded in runtime state and surfaced in the launch audit output.

| Workcell mode | Behavior |
|---|---|
| `strict` (default) | Default developer lane. Direct native ELF execution from `/workspace` and `/state` is blocked. Mutable shebang scripts cannot target protected runtimes. Package mutations are allowed but downgrade the session assurance to `lower-assurance-package-mutation`. |
| `strict` + `--container-mutability readonly` | Strongest managed lane. Package-manager writes stay blocked; control-plane posture does not downgrade. |
| `build` | Explicit mutable preparation lane with broader egress for dependency and image creation. |
| `breakglass` | Requires `--ack-breakglass`. Explicit higher-trust path. Visibly different. The managed in-container entrypoint still does not auto-inject unsafe provider flags. |

---

## MCP configuration

Workcell ships intentionally empty MCP templates for all providers:

- **Claude**: `adapters/claude/mcp-template.json` — contains `{"mcpServers": {}}`. On the safe path this is symlinked to `~/.mcp.json`. It can be replaced by an operator-reviewed file via `credentials.claude_mcp` in the injection policy.
- **Codex**: `adapters/codex/mcp/config.toml` — contains only a comment stating that no registry-backed MCP defaults are seeded. Linked to `~/.codex/mcp/config.toml`.

**Do not add live MCP server entries to either file.** The safe path disables all project MCP servers (`enableAllProjectMcpServers: false` in Claude settings). MCP servers with network access or package-fetching behavior must be explicitly opted into in a broader runtime mode and supplied through the injection policy, not committed to the adapter baseline.

---

## Hook coverage

The Claude adapter installs a `PreToolUse` hook for the `Bash` tool via
`adapters/claude/managed-settings.json`. The hook script is
`adapters/claude/hooks/guard-bash.sh`.

### What the hook covers

The `guard-bash.sh` hook intercepts Bash tool invocations and blocks:

- `git` calls with `--no-verify`, inline hook-path overrides (`-c core.hookspath=...`), or `--git-dir`/`--work-tree` overrides
- Nested `claude` subprocess calls with unsafe override flags (`--dangerously-skip-permissions`, `--mcp-config`, `--settings`, `--system-prompt`, etc.)
- Shell expansion syntax: command substitution `$(...)`, backticks, parameter expansion `${...}`, ANSI-C quoting `$'...'`, process substitution `<(...)` / `>(...)`
- `eval` and shell variable expansion (`$VAR`)
- Nested coding-agent CLI invocations (`codex`, `claude`, `gemini` by path or name)
- `source` and `.` (script sourcing)
- Nested shell interpreter invocations (`bash script.sh`, `sh script.sh`, etc.) — `bash -c '...'` is permitted
- `rm -rf` / `rm -fr` patterns
- Direct pushes to `main` or `master`
- Reads or writes to workspace control files (`AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, `.mcp.json`) and control directories (`.claude/`, `.codex/`, `.gemini/`, `.cursor/`, `.idea/`, `.vscode/`, `.zed/`)
- Reads or writes to Workcell home control-plane paths (`~/.claude`, `~/.codex`, `~/.gemini`, `/state/agent-home/.claude`, etc.)

### What the hook does NOT cover

- **Non-Bash tool use**: the hook is a `PreToolUse` matcher scoped to `Bash`. It does not intercept `Read`, `Edit`, `Write`, `Glob`, `Grep`, or any other Claude tool.
- **Read/Edit/Write tool path restrictions**: those are handled by the `permissions.deny` list in `managed-settings.json`, not by the hook.
- **Codex**: Codex uses its own execpolicy rules (`.codex/rules/`) rather than hooks. There is no equivalent Bash hook for Codex.
- **Gemini**: Gemini CLI does not expose a hook interface equivalent to Claude's `PreToolUse`. Workcell relies on the shared runtime boundary and Gemini's native approval mode.
- **Multi-step or indirect execution**: the hook inspects the literal Bash command string. Sufficiently indirect invocations may not be caught by static pattern matching. The hook is defense in depth, not the primary boundary.

The primary security boundary is always the external runtime (dedicated Colima VM plus hardened inner container), not the hook layer.
