# Claude Adapter

The Claude adapter maps the shared Workcell runtime into Claude Code's native
files and guardrails. Hooks and settings are defense in depth; the runtime
VM-plus-container boundary stays primary.

## Auth methods

- `claude_auth` credential key — a direct staged auth file mirrored into the
  Claude auth locations under `~/.claude/`, `~/.claude.json`, and
  `~/.config/claude-code/` (`internal/adapters/data.go`,
  `runtime/container/home-control-plane.sh`).
- `claude_api_key` credential key — wired through an `apiKeyHelper` script at
  `~/.claude/workcell/api-key-helper.sh` that reads the mounted key file, so a
  second plaintext key copy is not seeded into the session
  (`runtime/container/home-control-plane.sh`).
- `claude_mcp` credential key — reviewed Claude MCP config seeded to `~/.mcp.json`.
- `claude-macos-keychain` resolver — a fail-closed scaffold: it records the
  intended host-side auth source in policy but Workcell still aborts launch
  until a supported export path exists
  (`internal/authresolve/resolve_credential_sources.go`,
  `docs/injection-policy.md`).
- Shared GitHub CLI (`github_hosts`, `github_config`) and SSH inputs apply
  (`sharedCredentialsEnabled: true` in `internal/adapters/data.go`).

See [../../docs/injection-policy.md](../../docs/injection-policy.md).

## Managed control-plane files

Repo baselines under `adapters/claude/`:

- `managed-settings.json`: reviewed settings seeded to `~/.claude/settings.json`.
- `CLAUDE.md`: managed baseline rendered into `~/.claude/CLAUDE.md`.
- `mcp-template.json`: MCP template seeded to `~/.mcp.json`. The default template
  is empty — no live MCP servers ship in the baseline.
- `hooks/guard-bash.sh`: the reviewed `PreToolUse` Bash hook.

In-container reserved session targets include `~/.claude`,
`~/.claude/settings.json`, `~/.claude/CLAUDE.md`, the auth mirrors
(`~/.claude/.credentials.json`, `~/.claude.json`,
`~/.config/claude-code/auth.json`), the API-key helper dir `~/.claude/workcell`,
and `~/.mcp.json` (`ReservedTargets` in `internal/adapters/data.go`).

## Adapter behavior

- Each launch rebuilds the provider home from the immutable baseline plus
  explicit injection inputs; repo-local `.claude/` and `CLAUDE.md` are masked and
  imported only as reviewed layers
  (`runtime/container/home-control-plane.sh`, `docs/invariants.md` §3).
- The reviewed `PreToolUse` Bash hook blocks common trust-widening shell
  patterns. It is defense in depth: it does not replace the runtime boundary and
  does not cover non-Bash Claude tools
  (`docs/adapter-control-planes.md#claude-hook-coverage`).
- Autonomy is set host-side via `workcell --agent-autonomy` (mapped to
  `--permission-mode`); the wrapper does not honor provider-native overrides.
- Unsafe-argument policy (`reject_unsafe_claude_args` in
  `runtime/container/provider-policy.sh`): the wrapper blocks
  `--dangerously-skip-permissions`, `--add-dir`, `--allowedTools`,
  `--mcp-config`, `--plugin-dir`, `--settings`, `--setting-sources`,
  `--system-prompt`, `--append-system-prompt`, in-session `--permission-mode`
  overrides, and the `install`/`update` lifecycle commands. `breakglass` exempts
  these.
- The wrapper scrubs provider env such as `CLAUDE_CONFIG_DIR`, `GH_TOKEN`,
  `GITHUB_TOKEN`, and the OpenTelemetry variables before launch
  (`runtime/container/provider-wrapper.sh`).

GUI or IDE use is lower assurance unless it is only a client to the same bounded
runtime.

## See also

- [../README.md](../README.md) — adapter index and common contract
- [../../docs/adapter-control-planes.md](../../docs/adapter-control-planes.md)
- [../../docs/invariants.md](../../docs/invariants.md)
- [../../docs/extending-adapters.md](../../docs/extending-adapters.md) — worked
  contributor examples
