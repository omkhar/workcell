# Adapter Control Planes

Workcell keeps one shared boundary and several thin adapters. Each adapter
seeds a session-local provider home from reviewed baselines under `adapters/`
plus explicit injection inputs.

## How adapter seeding works

At launch, `runtime/container/home-control-plane.sh` rebuilds the provider home
from:

1. immutable adapter baselines baked into the image
2. explicit injection-policy inputs staged read-only for the session
3. selected workspace instruction imports such as repo-local `AGENTS.md`

Mutable provider state stays session-local. It is not written back into the
adapter baseline.

## Per-provider mapping

| Provider | Main managed files | Notable behavior |
|---|---|---|
| Codex | `config.toml`, `managed_config.toml`, `requirements.toml`, `rules/`, `mcp/config.toml`, rendered `AGENTS.md`, optional `auth.json` | rules are immutable by default and can become session-local writable only in explicit lower-assurance cases |
| Claude | `settings.json`, rendered `CLAUDE.md`, `.mcp.json`, auth mirrors, optional API key helper | the reviewed Bash hook is defense in depth; MCP defaults are empty |
| Gemini | `settings.json`, rendered `GEMINI.md`, `.env`, `oauth_creds.json`, `projects.json`, `trustedFolders.json` | `breakglass` restores Gemini's own folder-trust prompt; `gcloud_adc` is supplemental to Vertex config in `gemini_env` |

Shared cross-provider state can also seed:

- `~/.config/gh/config.yml`
- `~/.config/gh/hosts.yml`
- `~/.ssh/*`

## Instruction layering

Provider docs are rendered in this order:

1. adapter baseline doc
2. workspace `AGENTS.md` when present
3. workspace provider overlay such as `CLAUDE.md` or `GEMINI.md`
4. `documents.common`
5. provider-specific document fragment such as `documents.claude`

That gives the provider a native home document while keeping the workspace
control plane masked on the safe path.

## Autonomy mapping

| Workcell setting | Codex | Claude | Gemini |
|---|---|---|---|
| `--agent-autonomy yolo` | `--ask-for-approval never` | `--permission-mode bypassPermissions` | `--approval-mode yolo` |
| `--agent-autonomy prompt` | `--ask-for-approval on-request` | `--permission-mode default` | `--approval-mode default` |

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

### Claude hook coverage

The Claude adapter installs a reviewed `PreToolUse` Bash hook that blocks
common trust-widening shell patterns. It does not replace the external runtime
boundary and does not cover non-Bash Claude tools.

### Gemini folder trust

Workcell seeds Gemini's trusted-folders state for `/workspace` on the managed
path so masked ephemeral sessions do not force a restart-based trust prompt.
`breakglass` restores Gemini's own folder-trust flow.

## MCP posture

Workcell ships no live MCP defaults in the adapter baselines. Reviewed MCP
state must arrive through explicit operator inputs, not ambient workspace
content.
