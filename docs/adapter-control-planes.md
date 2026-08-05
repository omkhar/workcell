# Adapter Control Planes

Workcell keeps one shared boundary and several thin adapters. Each adapter
seeds a session-local provider home from reviewed baselines under `adapters/`
plus explicit injection inputs.

## How adapter seeding works

At launch, `runtime/container/home-control-plane.sh` rebuilds the provider home
from:

1. immutable adapter baselines baked into the image
2. explicit injection-policy inputs staged read-only for the session
3. selected workspace instruction imports such as repo-local `AGENTS.md`,
   where the provider adapter enables native instructions

Mutable provider state stays session-local. It is not written back into the
adapter baseline.

## Per-provider mapping

| Provider | Main managed files | Notable behavior |
|---|---|---|
| Codex | `config.toml`, `managed_config.toml`, `requirements.toml`, `rules/`, `mcp/config.toml`, rendered `AGENTS.md`, optional `auth.json` | rules are immutable by default and can become session-local writable only in explicit lower-assurance cases |
| Claude | `settings.json`, rendered `CLAUDE.md`, `.mcp.json`, auth mirrors, optional API key helper | the reviewed Bash hook is defense in depth; MCP defaults are empty |
| GitHub Copilot CLI | session-local `COPILOT_HOME`, `COPILOT_CACHE_HOME`, host-mounted token handoff plus transient runtime handoff, logs, and `~/.config/github-copilot` | supported Copilot token credential: `copilot_github_token`, staged through reviewed host-side inputs, removed from direct runtime mounts including the staged direct-mount copy, passed through a temporary handoff mount outside provider state, scrubbed from PID 1 by running the Workcell entrypoint without Docker `--init` for token handoff launches, and exported as `COPILOT_GITHUB_TOKEN` only to the managed child after unlinking the runtime handoff file; host `gh` auth, host keychains, host Copilot provider state (`~/.copilot`, `~/.config/github-copilot`, `~/.cache/github-copilot`), custom instructions, and skill/dynamic-retrieval surfaces are not safe-path inputs |
| Gemini | `settings.json`, rendered `GEMINI.md`, `.env`, `oauth_creds.json`, `projects.json`, `trustedFolders.json` | `breakglass` restores Gemini's own folder-trust prompt; `gcloud_adc` is supplemental to Vertex config in `gemini_env` |

## Current and unsupported provider control planes

GitHub Copilot CLI is seeded as a Tier 1 provider adapter. The adapter owns a
session-local `COPILOT_HOME`, `COPILOT_CACHE_HOME`, token handoff, logs, and
cache/config directories. It rejects host Copilot state, keychain access, and
ambient GitHub CLI auth. It also rejects custom instructions and unreviewed
control-plane expansion. It does not permit remote control or provider
auto-update state.

Shared cross-provider state can also seed the current supported adapters:

- `~/.config/gh/config.yml`
- `~/.config/gh/hosts.yml`
- `~/.ssh/*`

That GitHub CLI state must not become a Copilot safe-path auth input unless a
separate reviewed Copilot path explicitly allows it.

Google Antigravity CLI is an unsupported fail-closed scaffold. Before support,
the adapter must pin official CLI provenance. It must own session-local provider
state. It must map or block each provider control surface. Host account state,
browser profiles, keychains, home directories, and provider caches must stay
excluded.

## Instruction layering

Provider docs are rendered in this order for adapters that enable native
instruction files:

1. adapter baseline doc
2. workspace `AGENTS.md` when present
3. workspace provider overlay such as `CLAUDE.md` or `GEMINI.md`
4. `documents.common`
5. provider-specific document fragment such as `documents.claude`

That gives the provider a native home document while keeping the workspace
control plane masked on the safe path.

## Autonomy mapping

| Workcell setting | Codex | Claude | Copilot | Gemini |
|---|---|---|---|---|
| `--agent-autonomy yolo` | `--ask-for-approval never` | `--permission-mode bypassPermissions` | `--available-tools=view,create,edit,apply_patch,grep,glob --allow-tool=read --allow-tool=write --no-ask-user` | `--approval-mode yolo` |
| `--agent-autonomy prompt` | `--ask-for-approval on-request` | `--permission-mode default` | `--available-tools=view,create,edit,apply_patch,grep,glob` | `--approval-mode default` |

Copilot maps prompt and yolo modes through the reviewed provider wrapper,
blocks shell access by omission from `--available-tools`, and rejects unsafe
user flags. Antigravity has no autonomy mapping because it is unsupported.

Unsafe provider-native attempts to override those managed flags are blocked on
the managed path.

## Runtime-profile effect

`--mode` changes the runtime posture, not just provider argv:

- `strict`: default managed lane
- `strict --container-mutability readonly`: strongest managed lane
- `development`: managed interactive lane with broader dependency egress
- `build`: broader egress for preparation and rebuild work
- `breakglass`: explicit higher-trust path requiring acknowledgement

## Special cases

### Codex rules mutability

`~/.codex/rules/` is read-only by default. It becomes a session-local writable
copy only when:

- `--codex-rules-mutability session` is selected
- prompt autonomy is active
- the session has already been downgraded by package mutation

### Codex native sandbox

Workcell turns off the Codex Linux shell sandbox on the managed path. The
Workcell VM and container boundary remains the primary isolation control. The
Codex sandbox needs unprivileged user namespaces. The Tier 1 container does not
provide them.

### Claude hook coverage

The Claude adapter installs a reviewed `PreToolUse` Bash hook that blocks
common trust-widening shell patterns. It does not replace the external runtime
boundary and does not cover non-Bash Claude tools.

### Gemini folder trust

Workcell seeds Gemini's trusted-folders state for `/workspace` on the managed
path so masked ephemeral sessions do not force a restart-based trust prompt.
`breakglass` restores Gemini's own folder-trust flow.

### Provider trust and permissions

Workcell controls permissive CLI options, saved permissions, and remote control.
It also controls plugins, hooks, skills, MCP, LSP, instructions, and dynamic
retrieval. The current release stages only reviewed session-local state and the
explicit token input.

Antigravity support must do the same once official CLI provenance identifies
its settings, permission, plugin, MCP, hook, and instruction surfaces. Current
releases do not import or trust those paths because `--agent antigravity` is
not implemented.

Strict mode scrubs provider telemetry, OpenTelemetry, and content-capture
environment variables by default; any opt-in path must be lower assurance,
acknowledged, audited, and tested.

## MCP posture

Workcell ships no live MCP defaults in the adapter baselines. Reviewed MCP
state must arrive through explicit operator inputs, not ambient workspace
content.

Workcell classifies repo MCP files as `deny` outside breakglass. An untrusted
workspace can otherwise configure an arbitrary MCP server.

The launcher masks each file with a valid empty configuration. Thus, repo MCP
servers do not reach the provider. Workcell replaces an invalid configuration
and fails closed. The `--allow-control-plane-vcs` option does not lift this
deny.

An operator opts a workspace's MCP configs back in with the explicit,
`ack-required` pair `--allow-repo-mcp` plus a dated `--ack-repo-mcp=YYYY-MM-DD`.
The launcher records the decision as `workspace_repo_mcp=denied` or
`workspace_repo_mcp=acknowledged` in the session record and the host audit log.
Codex's `.codex/mcp/config.toml` arrives through the whole-`.codex` directory
surface and remains governed by the existing control-plane masking; a
directory-scoped carve-out for it is tracked as follow-up work.
