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

## Current and planned provider control planes

GitHub Copilot CLI is seeded as a Tier 1 provider adapter. The adapter owns a
session-local `COPILOT_HOME`, `COPILOT_CACHE_HOME`, token handoff, logs, and
cache/config directories. It rejects host `~/.copilot`, keychain access,
host `~/.config/github-copilot`, host `~/.cache/github-copilot`, `GH_TOKEN`,
`GITHUB_TOKEN`, ambient GitHub CLI fallback, custom instructions, repo-local
skill/dynamic-retrieval expansion, unreviewed plugin or MCP expansion,
remote-control sharing, and provider auto-update state as implicit Tier 1
inputs.

Shared cross-provider state can also seed the current supported adapters:

- `~/.config/gh/config.yml`
- `~/.config/gh/hosts.yml`
- `~/.ssh/*`

That GitHub CLI state must not become a Copilot safe-path auth input unless a
separate reviewed Copilot path explicitly allows it.

Google Antigravity CLI is also a planned fail-closed adapter. Before support is
claimed, the Antigravity adapter must pin official CLI provenance, own
session-local provider home/cache/settings state, and explicitly map or block
subagents, plugins, MCP, sandbox settings, permissions, hooks, and any shared
desktop/IDE state. Host Google account caches, browser profiles, keychains,
host homes, and provider caches must not become implicit Tier 1 inputs.

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
blocks shell access by omission from `--available-tools`, and rejects
user-supplied flags that silently widen trust. Antigravity autonomy mappings
are intentionally absent until its adapter lands.

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

Workcell pins Codex's own Linux shell sandbox off on the managed path.
Workcell's outer VM-plus-container boundary remains the primary isolation
control, and the current Codex Linux sandbox requires unprivileged user
namespaces that are not available inside the Tier 1 container.

### Claude hook coverage

The Claude adapter installs a reviewed `PreToolUse` Bash hook that blocks
common trust-widening shell patterns. It does not replace the external runtime
boundary and does not cover non-Bash Claude tools.

### Gemini folder trust

Workcell seeds Gemini's trusted-folders state for `/workspace` on the managed
path so masked ephemeral sessions do not force a restart-based trust prompt.
`breakglass` restores Gemini's own folder-trust flow.

### Provider trust and permissions

Copilot support treats permissive CLI options, saved permissions, remote
control, plugins, hooks, skills, MCP/LSP config, custom instructions,
repository-local Copilot settings, and dynamic retrieval as managed
control-plane inputs. Current releases stage only the reviewed session-local
Copilot home/cache and explicit token input mount, not host Copilot or ambient
GitHub CLI state.

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

Repo-defined MCP server-definition surfaces (`.mcp.json`, `.github/mcp.json`)
are classified `deny` in every non-breakglass mode: an untrusted workspace can
otherwise wire the agent to arbitrary MCP servers, a boundary/exfil risk. The
launcher overlays a neutralized, schema-valid empty config (`{"mcpServers": {}}`,
matching the shipped `adapters/claude/mcp-template.json` baseline) over each
surface so the repo-defined servers never reach the provider process while the
config stays parseable, and it fails closed — an unparsable or malformed
workspace config is replaced wholesale rather than honored. This deny is independent of `--allow-control-plane-vcs`: acknowledging
control-plane Git visibility does not lift it.

An operator opts a workspace's MCP configs back in with the explicit,
`ack-required` pair `--allow-repo-mcp` plus a dated `--ack-repo-mcp=YYYY-MM-DD`.
The launcher records the decision as `workspace_repo_mcp=denied` or
`workspace_repo_mcp=acknowledged` in the session record and the host audit log.
Codex's `.codex/mcp/config.toml` arrives through the whole-`.codex` directory
surface and remains governed by the existing control-plane masking; a
directory-scoped carve-out for it is tracked as follow-up work.
