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
  state, then copied into provider home only when provider-native files are
  required, or consumed directly by the wrapper for Copilot
- a crash can leave short-lived staged plaintext behind until later cleanup

## Supported sections

| Section | Purpose |
|---|---|
| `[documents]` | common or provider-specific instruction fragments |
| `[credentials]` | provider-native auth, MCP, and shared GitHub CLI state |
| `[ssh]` | SSH config, known hosts, and identity files |
| `[[copies]]` | explicit copied files or directories for non-reserved targets |
| `[network]` | extend or tighten the per-session egress allowlist |
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

- `providers = ["codex", "claude", "copilot", "gemini"]`
- `modes = ["strict", "development", "build"]`

Credential entries can be either direct file sources or built-in host
resolvers:

- `credentials.codex_auth = "/abs/path"`
- `[credentials.claude_auth] resolver = "claude-macos-keychain"`

Direct credential source files must live outside the mounted workspace. Workcell
rejects credential sources under the workspace because the workspace itself is
mounted into the runtime and would expose the original secret path in addition
to the reviewed credential handoff.

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
| GitHub Copilot CLI | `copilot_github_token` | SSH inputs via policy as needed | staged through reviewed host-side inputs, removed from direct runtime mounts, passed through a temporary handoff mount outside provider state and a transient runtime handoff file, and exported as `COPILOT_GITHUB_TOKEN` only to the managed child process; shared GitHub CLI state, host keychains, `GH_TOKEN`, `GITHUB_TOKEN`, and host Copilot provider state (`~/.copilot`, `~/.config/github-copilot`, `~/.cache/github-copilot`) are not Copilot auth inputs |
| Gemini | `gemini_env`, `gemini_oauth` | `gemini_projects` as a supplemental project registry input, `gcloud_adc` as a supplemental Vertex input, plus shared GitHub CLI and SSH inputs via policy as needed | `gemini_projects` and `gcloud_adc` are not standalone Gemini auth modes |

GitHub Copilot CLI is supported only through explicit staged token material:
`copilot_github_token`, with session-local `COPILOT_HOME` and
`COPILOT_CACHE_HOME` state. It does not pass through host Copilot provider
state (`~/.copilot`, `~/.config/github-copilot`,
`~/.cache/github-copilot`), host keychains, `GH_TOKEN`, `GITHUB_TOKEN`,
ambient `gh auth token`, arbitrary BYOK provider env, or whole-home state.
For auth-required launches, Workcell
removes the reviewed staged token file from direct runtime mounts, deletes the
staged direct-mount copy from the mounted injection bundle, converts the token
into a temporary host-mounted token handoff outside mounted provider state,
stages the value into a transient runtime handoff file, unlinks the mounted
handoff file, re-execs the entrypoint without the token in its environment, keeps that
entrypoint as PID 1 instead of Docker `--init` so `/proc/1/environ` is
scrubbed, and exports its value as
`COPILOT_GITHUB_TOKEN` only to the managed child after the wrapper unlinks the
handoff file. Copilot development-shell or debug-command launches with a staged
token also remove the token file and staged copy from direct runtime mounts,
but do not create the handoff mount because the provider is not being
authenticated.

Google Antigravity CLI is also planned, but not launch-ready. Its future path
must first pin official install and auth provenance, then stage only reviewed
Google auth material into session-local provider state. Host Google account
caches, browser profiles, keychains, host homes, and provider caches are not
acceptable implicit safe-path inputs.

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
| `copilot_github_token` | session-local Copilot token handoff | converted to a temporary host-mounted token handoff outside mounted provider state, moved through a transient runtime handoff file with the Workcell entrypoint as PID 1, and exported as `COPILOT_GITHUB_TOKEN` only to the managed Copilot child process; the original token file and staged direct-mount copy are removed from direct runtime mounts and are not copied into provider state |
| `github_hosts` | `~/.config/gh/hosts.yml` | shared GitHub CLI auth; prefer scoped nested tables |
| `github_config` | `~/.config/gh/config.yml` | shared GitHub CLI config; prefer scoped nested tables |

Future Antigravity credential keys are planned, not supported keys in current
releases. They must not appear in operator policy until the matching adapter,
validation, and docs land.

## Network egress (`[network]`)

`strict` mode ships a default-deny, per-session egress allowlist — the reviewed
policy artifact for this control. On the `colima` target the launcher enforces it
as a fail-closed, dual-stack firewall: `scripts/workcell` computes
`ALLOW_ENDPOINTS` (the union of reviewed sources — provider/auth-recovery/broker
endpoints, credential-derived endpoints, `[network].allow_endpoints`, profile
`EXTRA_ENDPOINTS`, and the Debian snapshot mirrors
`snapshot-cloudflare.debian.org:443` and `snapshot.debian.org:443`), de-dupes it, subtracts
`[network].deny_endpoints`, and hands it to `scripts/colima-egress-allowlist.sh`,
which programs `iptables`/`ip6tables` `DOCKER-USER` rules that ACCEPT each allowed
`host:port` (IPv4 and IPv6) and `DROP` the rest. If `ip6tables` is unavailable the
helper aborts rather than leave IPv6 unfiltered. Only the colima target applies
these rules; the dispatch never changes based on policy content.

Operators extend or tighten the allowlist only through the `[network]` table,
never by disabling the default:

```toml
[network]
allow_endpoints = ["registry.internal.example:443"]  # add to the allowlist
deny_endpoints  = ["chatgpt.com:443"]                 # remove from the allowlist
```

- `allow_endpoints` are unioned into the allowlist (they extend, never replace
  the reviewed provider/credential endpoints).
- `deny_endpoints` are subtracted by endpoint entry after every allow source, so
  a denied `host:port` is removed even when a provider needs it (deny wins).
  Because the match is exact, blocking the automatic Debian snapshot egress
  requires denying **both** mirror endpoints —
  `snapshot-cloudflare.debian.org:443` and `snapshot.debian.org:443` — since
  denying only one still leaves the other reachable.
- Each endpoint must be `host:port` or `[ipv6]:port` (port 1-65535, host
  `^[A-Za-z0-9.-]+$`, no leading dot or `..`, IP-shaped hosts must be real IPs),
  validated with the same grammar `scripts/colima-egress-allowlist.sh` applies.
- Fail-closed: a malformed endpoint, empty string, unknown `[network]` key, or
  non-array value aborts with an error naming the offending value.

**No-weakening invariant:** `[network]` can only contribute endpoint lists; it
cannot set `NETWORK_POLICY`, disable the allowlist, or switch to an unrestricted
posture, so an injection policy never weakens the shipped default-deny allowlist.

**Scope:** `deny_endpoints` tighten IP-level VM/container egress (the session and
the bootstrap build container). Two boundaries follow — a denied host sharing an
IP with an allowed endpoint (e.g. a shared CDN) stays reachable at the IP layer,
and the launcher's host-side rebuild fetches (release-URL resolution) are not
gated. To fully block a host, drop overlapping allow_endpoints and prebuild the
image.

Per-session enforcement is a `colima`-only property; other targets rely on their
own network controls and do not receive the `DOCKER-USER` allowlist. The launch
summary prints an `egress_enforcement=` line next to `network_policy=...
endpoints=...`:

| Target | `egress_enforcement` | Per-session allowlist enforced |
|---|---|---|
| `colima` (allowlist) | `allowlist` | yes — `iptables`/`ip6tables` default-deny in `DOCKER-USER` |
| `colima` (unrestricted, e.g. breakglass) | `none` | no — allowlist not applied |
| `docker-desktop` | `none` | no — relies on Docker Desktop / host controls |
| `aws-ec2-ssm` (preview) | `none` | no — relies on the VM's security groups |
| `gcp-vm` (preview) | `none` | no — relies on the VM's own firewall |

`egress_enforcement=allowlist` prints only for `colima` + allowlist, else `none`.

## Instruction precedence

Provider docs are rendered in this order for adapters that enable native
instruction files:

1. adapter baseline doc
2. repo-local `AGENTS.md`
3. repo-local provider overlay such as `CLAUDE.md` or `GEMINI.md`
4. `documents.common`
5. provider-specific document fragment

The Copilot adapter masks repo-local Copilot control-plane paths such as
`.github/copilot-instructions.md`, `.github/instructions/**`, and
`.github/copilot/settings*.json` instead of trusting them directly, and the
managed wrapper launches with custom instructions disabled. The planned
Antigravity adapter must define its instruction, settings, plugin, MCP, and
hook files before any provider-specific instruction layering is supported.
Current releases do not provide provider-specific instruction layering for
Antigravity.

## Deliberate limits

- no arbitrary environment-variable secret injection on the safe path
- no whole-home passthrough
- no writes into Workcell-managed control-plane paths through `[[copies]]`
- no `SSH_AUTH_SOCK` forwarding
- no assumption that one process inside the session is isolated from another
  process in the same session
- no host provider-home, keychain, browser-profile, ambient CLI auth, or broad
  provider token state passthrough on Copilot or future Antigravity paths
- no provider telemetry, OpenTelemetry, or content-capture environment
  variables in `strict` mode unless a lower-assurance acknowledged path and
  deterministic tests are added

## Recommended usage

- put org-wide guidance in `documents.common`
- keep provider deltas in `documents.codex`, `documents.claude`, or
  `documents.gemini`; Copilot custom instructions are disabled on the managed
  path
- use `[credentials]` for reusable auth, not `[[copies]]`
- scope shared GitHub credentials with `providers = [...]`
- keep secret inputs owner-only and avoid symlinks

## Example

See [docs/examples/injection-policy.toml](./examples/injection-policy.toml).
