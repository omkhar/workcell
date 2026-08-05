# Claude Adapter

The Claude adapter maps the Workcell runtime to Claude Code controls. The VM
and container runtime boundary is the primary control. Hooks and settings give
additional protection.

## Auth methods

- `claude_auth` stages the auth file at the reviewed Claude auth paths.
- `claude_api_key` uses `~/.claude/workcell/api-key-helper.sh`. The helper reads
  the mounted key file. It does not make a second plaintext key copy.
- `claude_mcp` stages reviewed Claude MCP configuration at `~/.mcp.json`.
- `claude-macos-keychain` is a fail-closed scaffold. Workcell stops until a
  supported export path exists.
- Shared GitHub CLI (`github_hosts`, `github_config`) and SSH inputs apply
  (`sharedCredentialsEnabled: true` in `internal/adapters/data.go`).

See [../../docs/injection-policy.md](../../docs/injection-policy.md).

## Managed control-plane files

Repo baselines under `adapters/claude/`:

- `.claude/settings.json`: reviewed session settings seeded to
  `~/.claude/settings.json` by `workcell_render_claude_settings` — this is the
  file to edit for session-home settings changes.
- `managed-settings.json`: the separate enterprise managed-policy file, installed
  and verified at `/etc/claude-code/managed-settings.json` (it is **not** the
  session-home `~/.claude/settings.json`).
- `CLAUDE.md`: managed baseline rendered into `~/.claude/CLAUDE.md`.
- `mcp-template.json`: MCP template seeded to `~/.mcp.json`. The default template
  is empty — no live MCP servers ship in the baseline.
- `hooks/guard-bash.sh`: the reviewed `PreToolUse` Bash hook.

In-container reserved session targets include `~/.claude`,
`~/.claude/settings.json`, `~/.claude/CLAUDE.md`, the auth mirrors
(`~/.claude/.credentials.json`, `~/.claude.json`,
`~/.config/claude-code/auth.json`), the API key helper dir `~/.claude/workcell`,
and `~/.mcp.json` (`ReservedTargets` in `internal/adapters/data.go`).

## Adapter behavior

- Each start rebuilds the provider home from the immutable baseline and explicit
  inputs. Workcell masks repo-local `.claude/` and `CLAUDE.md`. It imports them
  only as reviewed layers.
- The reviewed `PreToolUse` Bash hook blocks common trust-widening shell
  patterns. It is defense in depth: it does not replace the runtime boundary and
  does not cover non-Bash Claude tools
  (`docs/adapter-control-planes.md#claude-hook-coverage`).
- Autonomy is set host-side via `workcell --agent-autonomy` (mapped to
  `--permission-mode`); the wrapper does not honor provider-native overrides.
- `reject_unsafe_claude_args` blocks permission bypass, extra directories,
  custom tools, MCP, plugins, settings, and prompt overrides.
- It also blocks provider install and update commands. These rules apply in all
  modes, including `breakglass`.
- Breakglass changes the container posture. It does not change the provider
  unsafe-flag policy.
- The wrapper scrubs provider env such as `CLAUDE_CONFIG_DIR`, `GH_TOKEN`,
  `GITHUB_TOKEN`, and the OpenTelemetry variables before launch
  (`runtime/container/provider-wrapper.sh`).

GUI or IDE use has lower assurance. It can be Tier 2 only as a client of the
same bounded runtime.

## See also

- [../README.md](../README.md) — adapter index and common contract
- [../../docs/adapter-control-planes.md](../../docs/adapter-control-planes.md)
- [../../docs/invariants.md](../../docs/invariants.md)
- [../../docs/extending-adapters.md](../../docs/extending-adapters.md) — worked
  contributor examples
