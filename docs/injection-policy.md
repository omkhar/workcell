# Injection Policy

The safe path does not forward host homes, sockets, or provider state by
default. The supported way to place stable material into Workcell sessions is
an explicit operator-owned injection policy.

## How it works

Each launch rebuilds the provider-facing home from:

1. immutable adapter baselines
2. workspace instruction imports
3. explicit injected documents, credentials, copies, and SSH material

Secret-bearing inputs are handled more carefully than public inputs:

- provider credentials, SSH identities, and `classification = "secret"` copies
  are validated on the host
- they are mounted read-only into the runtime from launcher-owned staging
  state, then copied into the ephemeral session home
- a crash can leave short-lived staged plaintext behind until later cleanup

## Supported sections

| Section | Purpose |
|---|---|
| `[documents]` | common or provider-specific instruction fragments |
| `[credentials]` | provider-native auth, MCP, and shared GitHub CLI state |
| `[ssh]` | SSH config, known hosts, and identity files |
| `[[copies]]` | explicit copied files or directories for non-reserved targets |
| `includes = [...]` | compose a policy from smaller operator-owned fragments |

Selectors let you scope entries to only some launches:

- `providers = ["codex", "claude", "gemini"]`
- `modes = ["strict", "build"]`

Credential entries can be either direct file sources or built-in host
resolvers:

- `credentials.codex_auth = "/abs/path"`
- `[credentials.claude_auth] resolver = "claude-macos-keychain"`

Resolver-backed credentials are still host-side preprocessing only. Workcell
materializes them into ordinary files under the per-launch injection bundle; it
does not pass Keychain access into the runtime.

Today, `claude-macos-keychain` is a fail-closed resolver scaffold: it lets you
record the intended host-side auth source in policy, but Workcell still aborts
launch unless a supported export path exists.

## Credential keys

| Key | Session target | Notes |
|---|---|---|
| `codex_auth` | `~/.codex/auth.json` | persisted Codex auth |
| `claude_auth` | Claude auth mirrors under `~/.claude/`, `~/.claude.json`, and `~/.config/claude-code/` | on macOS, prefer the built-in `claude-macos-keychain` host resolver |
| `claude_api_key` | helper-backed Claude API key access | avoids seeding a second plaintext key copy into the session |
| `claude_mcp` | `~/.mcp.json` | reviewed Claude MCP config |
| `gemini_env` | `~/.gemini/.env` | API key, GCA, or Vertex configuration |
| `gemini_oauth` | `~/.gemini/oauth_creds.json` | cached Gemini OAuth state |
| `gemini_projects` | `~/.gemini/projects.json` | persisted Gemini project registry |
| `gcloud_adc` | `~/.config/gcloud/application_default_credentials.json` | supplemental Vertex credential, not a standalone Gemini auth mode |
| `github_hosts` | `~/.config/gh/hosts.yml` | shared GitHub CLI auth; prefer scoped nested tables |
| `github_config` | `~/.config/gh/config.yml` | shared GitHub CLI config; prefer scoped nested tables |

## Instruction precedence

Provider docs are rendered in this order:

1. adapter baseline doc
2. repo-local `AGENTS.md`
3. repo-local provider overlay such as `CLAUDE.md` or `GEMINI.md`
4. `documents.common`
5. provider-specific document fragment

## Deliberate limits

- no arbitrary environment-variable secret injection on the safe path
- no whole-home passthrough
- no writes into Workcell-managed control-plane paths through `[[copies]]`
- no `SSH_AUTH_SOCK` forwarding
- no assumption that one process inside the session is isolated from another
  process in the same session

## Recommended usage

- put org-wide guidance in `documents.common`
- keep provider deltas in `documents.codex`, `documents.claude`, or
  `documents.gemini`
- use `[credentials]` for reusable auth, not `[[copies]]`
- scope shared GitHub credentials with `providers = [...]`
- keep secret inputs owner-only and avoid symlinks

## Example

See [docs/examples/injection-policy.toml](./examples/injection-policy.toml).
