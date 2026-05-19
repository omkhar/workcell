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

`workcell auth init|set|unset|status` manages the entrypoint policy file only.
`workcell policy show|validate|diff` inspects the merged policy, and
`workcell why --credential ... --agent ... --mode ...` explains one credential
decision, including out-of-scope cases, without launching the runtime. If a
credential is declared by an
included fragment, update that fragment directly.

`workcell auth status` and `workcell --auth-status` report
`provider_bootstrap_*` lines for the selected agent. `workcell why` reports the
matching `bootstrap_*` lines for the selected credential so the operator can
see whether the path is repo-required, certification-only, or manual.

Selectors let you scope entries to only some launches:

- `providers = ["codex", "claude", "gemini"]`
- `modes = ["strict", "development", "build"]`

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

## Provider auth maturity

Direct staged credential files are the primary supported auth path today.
Built-in host resolvers are still intentionally narrow:

- `codex-home-auth-file` is a reviewed Codex host-auth reuse path
- `claude-macos-keychain` remains the fail-closed Claude macOS scaffold

See [provider-bootstrap-matrix.md](provider-bootstrap-matrix.md) for the
current bootstrap tiers, handoffs, and evidence.

| Provider | Launch-ready inputs today | Additional reviewed inputs | Current caveats |
|---|---|---|---|
| Codex | direct staged `codex_auth` or the `codex-home-auth-file` resolver | shared GitHub CLI and SSH inputs via policy as needed | direct staged auth is still the default recommendation; host resolver reuse remains host-side preprocessing only |
| Claude | `claude_auth`, `claude_api_key`, `claude_mcp` | shared GitHub CLI and SSH inputs via policy as needed | the built-in `claude-macos-keychain` resolver can record intent but remains fail-closed until a supported export path exists |
| Gemini | `gemini_env`, `gemini_oauth` | `gemini_projects` as a supplemental project registry input, `gcloud_adc` as a supplemental Vertex input, plus shared GitHub CLI and SSH inputs via policy as needed | `gemini_projects` and `gcloud_adc` are not standalone Gemini auth modes |

GitHub Copilot CLI is planned for Tier 1 parity but is not a launch-ready
provider today. The planned path must use explicit staged token material, likely
`copilot_github_token`, and session-local `COPILOT_HOME` /
`COPILOT_CACHE_HOME` state. It must not pass through host `~/.copilot`, host
keychains, `GH_TOKEN`, `GITHUB_TOKEN`, ambient `gh auth token`, arbitrary BYOK
provider env, or whole-home state. The managed child should see only the
Workcell-staged `COPILOT_GITHUB_TOKEN` on the supported path.

## Credential keys

| Key | Session target | Notes |
|---|---|---|
| `codex_auth` | `~/.codex/auth.json` | persisted Codex auth |
| `claude_auth` | Claude auth mirrors under `~/.claude/`, `~/.claude.json`, and `~/.config/claude-code/` | direct staged auth file is launch-ready; the built-in `claude-macos-keychain` resolver remains fail-closed scaffold only |
| `claude_api_key` | helper-backed Claude API key access | avoids seeding a second plaintext key copy into the session |
| `claude_mcp` | `~/.mcp.json` | reviewed Claude MCP config |
| `gemini_env` | `~/.gemini/.env` | API key, GCA, or Vertex configuration |
| `gemini_oauth` | `~/.gemini/oauth_creds.json` | cached Gemini OAuth state |
| `gemini_projects` | `~/.gemini/projects.json` | persisted Gemini project registry |
| `gcloud_adc` | `~/.config/gcloud/application_default_credentials.json` | supplemental Vertex credential, not a standalone Gemini auth mode |
| `github_hosts` | `~/.config/gh/hosts.yml` | shared GitHub CLI auth; prefer scoped nested tables |
| `github_config` | `~/.config/gh/config.yml` | shared GitHub CLI config; prefer scoped nested tables |

`copilot_github_token` is a planned credential key, not a supported key in
current releases. It must not appear in operator policy until the Copilot
adapter, validation, and docs land.

## Instruction precedence

Provider docs are rendered in this order:

1. adapter baseline doc
2. repo-local `AGENTS.md`
3. repo-local provider overlay such as `CLAUDE.md` or `GEMINI.md`
4. `documents.common`
5. provider-specific document fragment

The planned Copilot adapter must separately define how `AGENTS.md`,
`.github/copilot-instructions.md`, `.github/instructions/**`, and
`.github/copilot/settings*.json` are imported, masked, or rejected. Current
releases do not provide Copilot-specific instruction layering.

## Deliberate limits

- no arbitrary environment-variable secret injection on the safe path
- no whole-home passthrough
- no writes into Workcell-managed control-plane paths through `[[copies]]`
- no `SSH_AUTH_SOCK` forwarding
- no assumption that one process inside the session is isolated from another
  process in the same session
- no host `~/.copilot`, keychain, `GH_TOKEN`, `GITHUB_TOKEN`, ambient
  `gh auth token`, or broad Copilot token state passthrough on the future
  Copilot path
- no Copilot telemetry, OpenTelemetry, or content-capture environment variables
  in future `strict` mode unless a lower-assurance acknowledged path and
  deterministic tests are added

## Recommended usage

- put org-wide guidance in `documents.common`
- keep provider deltas in `documents.codex`, `documents.claude`, or
  `documents.gemini`
- use `[credentials]` for reusable auth, not `[[copies]]`
- scope shared GitHub credentials with `providers = [...]`
- keep secret inputs owner-only and avoid symlinks

## Example

See [docs/examples/injection-policy.toml](./examples/injection-policy.toml).
