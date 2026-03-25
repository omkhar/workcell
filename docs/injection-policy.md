# Injection Policy

Workcell's safe path does not forward host homes, `SSH_AUTH_SOCK`, keychains,
browser profiles, or provider state by default. The supported way to place
consistent material into every session is an operator-owned injection policy.

## Design

Workcell separates three planes:

- immutable adapter baseline under `adapters/`
- per-session writable runtime state under `/state/agent-home`
- explicit staged operator input mounted read-only under
  `/opt/workcell/host-injections`

At provider launch, Workcell re-seeds the provider-facing home from the
immutable baseline plus the staged operator input. Mutable provider state such
as provider auth tokens stays session-local, not a write-back into the adapter
baseline. By default, Codex rules remain linked to the immutable adapter
baseline. If you explicitly opt into
`--codex-rules-mutability session`, Workcell copies the Codex rules tree into a
session-local writable tree so provider-approved execpolicy amendments can
persist for the life of the container, while the adapter baseline remains
immutable. The staged host bundle lives in a launcher-owned state
directory under `~/.local/state/workcell/tmp`, is mounted read-only into the
runtime, and is cleaned up on exit with dead-owner stale
bundle garbage collection on later launches. Secret-bearing inputs are treated
more strictly: Workcell validates their host source files, mounts those sources
read-only into the runtime for the current session, and only then copies them
into the ephemeral session home. That avoids creating an extra plaintext
host-side bundle for provider credentials, SSH material, and
`classification = "secret"` copied files.

Provider docs are rendered in a fixed precedence order:

1. immutable baseline doc from `adapters/`
2. imported workspace common doc from repo-local `AGENTS.md` when present
3. imported workspace provider overlay such as repo-local `CLAUDE.md` or
   `GEMINI.md` when present
4. `documents.common`
5. provider-specific fragment such as `documents.claude`

That lets you keep one common `AGENTS.md`-style instruction file and have
Workcell append it to the provider-native home doc for Codex, Claude, or
Gemini without replacing the reviewed baseline, while still layering
`CLAUDE.md` or `GEMINI.md` provider-specific deltas when they exist.

## Supported inputs

- `documents.common`: common non-secret instructions rendered into every
  provider's home doc
- `documents.codex`, `documents.claude`, `documents.gemini`: provider-specific
  instruction fragments
- `[credentials]`: provider-native credential files mounted read-only from
  their original host paths and then copied into the correct per-session home
  paths for Codex, Claude, Gemini, and GitHub CLI
- `[ssh]`: optional SSH config, known hosts, and identity files mounted
  read-only from their original host paths and copied into the ephemeral
  in-container `~/.ssh`
- `[[copies]]`: explicit copied files or directories staged into either
  `/state/injected/...` or a non-reserved target under
  `/state/agent-home/...`. `classification = "secret"` entries use the same
  direct-mount model as `[credentials]`.

Selectors let you scope injected material to only some launches:

- `providers = ["codex", "claude", "gemini"]`
- `modes = ["strict", "build"]`

For `[credentials]`, simple `key = "/path/to/file"` entries still work, and
scoped entries can be expressed as nested tables:

```toml
[credentials.github_hosts]
source = "/Users/example/.config/gh/hosts.yml"
providers = ["claude", "gemini"]
modes = ["strict", "build"]
```

## Deliberate limits

- no arbitrary environment-variable secret injection on the safe path
- no whole-home passthrough
- no writes into reserved Workcell-managed control-plane files such as
  `.codex/config.toml`, `.codex/auth.json`, `.codex/rules/`,
  `.claude/settings.json`, `.gemini/settings.json`, `.gemini/.env`, or
  `.config/gh/hosts.yml`
- no generic `.ssh` target writes through `[[copies]]`; use the dedicated
  `[ssh]` section
- no expectation that injected secrets stay hidden from processes already
  running inside the same session; the boundary is host-to-session, not
  process-to-process inside the container
- no writes into Workcell-generated helper state such as
  `~/.claude/workcell/`
- no unsafe SSH config directives such as `ProxyCommand`, `LocalCommand`,
  `Include`, `IdentityAgent`, or `Match exec` unless you explicitly set
  `ssh.allow_unsafe_config = true`

## Recommended patterns

- Use `documents.common` for organization-wide instructions that should appear
  in every agent session.
- Use `documents.codex`, `documents.claude`, or `documents.gemini` only for
  provider-specific deltas.
- Use `[credentials]` for provider and GitHub auth material that should land in
  Workcell-managed session paths without a fresh login every time. This is the
  safest way to persist reusable provider auth on the host.
- Keep secret sources owner-only (`0600` for files, `0700` for directories) and
  avoid symlinks. Workcell rejects looser permissions on secret-bearing inputs.
  `ssh.known_hosts` is the exception: standard world-readable files are
  accepted, but symlinks and group/world-writable paths are rejected.
- Use `[[copies]]` with `target = "/state/injected/..."` for shared read-only
  material such as CA bundles or repo policy files. Public copies are staged
  through the launcher-owned bundle.
- Use `[[copies]]` with `target = "~/.config/..."` for per-session config files
  that are not already covered by Workcell-managed credential or control-plane
  paths. Secret copies are mounted read-only from their original host paths and
  copied in-session.
- Use `[ssh]` for SSH config and identity files. Do not forward host
  `SSH_AUTH_SOCK`.
- Do not use `[[copies]]` for long-lived API keys or provider login material
  when `[credentials]` already covers that provider.

## Credential guidance

- `credentials.codex_auth` mounts a host `auth.json` read-only and copies it into
  `~/.codex/auth.json` for session-local Codex auth reuse.
- `credentials.claude_api_key` mounts a secret file read-only and generates a
  session-local Claude `apiKeyHelper` so Claude can reuse an API key without
  mutating the reviewed baseline settings.
- `credentials.claude_auth` mounts persisted Claude CLI auth into
  `~/.config/claude-code/auth.json` when you already have reviewed host-side
  Claude login state.
- `credentials.claude_mcp` mounts an approved Claude `.mcp.json` into the
  session without widening trust to the whole workspace copy.
- `credentials.gemini_env` mounts a provider-native `~/.gemini/.env`.
  This matches Gemini CLI's documented env-file auth flow for API keys,
  Vertex project settings, and related variables.
- `credentials.gemini_oauth` mounts a cached Gemini OAuth credential file when
  you already have one on the host.
- `credentials.gemini_projects` mounts a persisted Gemini `projects.json`
  when you want Gemini CLI's project registry to survive across sessions.
- `credentials.gcloud_adc` mounts Google ADC into
  `~/.config/gcloud/application_default_credentials.json` for Gemini Vertex
  flows.
- `credentials.github_hosts` and `credentials.github_config` mount GitHub CLI
  auth/config into `~/.config/gh/`.

## Example

See [`docs/examples/injection-policy.toml`](./examples/injection-policy.toml).
