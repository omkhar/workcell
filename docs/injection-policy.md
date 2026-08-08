# Injection Policy

Workcell does not pass host homes, sockets, or complete provider state to the
safe path. Use an operator-owned injection policy for each approved input.

Workcell can inject documents, credentials, SSH material, files, and endpoint
entries. Credentials, copies, and SSH material can use provider and mode
selectors.

## Security boundary

Workcell validates each secret source on the host. Workcell stages the source
in launcher-owned state.

Workcell mounts the staged source read-only. A crash can leave staged plaintext
until cleanup.

Store each credential source outside the mounted workspace. Workcell rejects a
credential source inside the workspace.

Workcell does not pass host keychains into the runtime. A successful resolver
runs on the host and writes a regular staged file.

## Policy commands

Use these commands for the entrypoint policy file:

- `workcell auth init`
- `workcell auth set`
- `workcell auth unset`
- `workcell auth status`

Use these commands for the merged policy:

- `workcell policy show`
- `workcell policy validate`
- `workcell policy diff`

Use `workcell why` to explain one credential decision. The command does not
start a runtime.

```sh
workcell why --credential CREDENTIAL --agent PROVIDER --mode MODE
```

If an included fragment declares a value, change that fragment. The auth
commands change only the entrypoint file.

## Schema reference

The tables in this section list the keys that the parsers accept. A Go test
compares each marked table with the corresponding parser key set.

### Root keys

<!-- schema:root:begin -->
| Key | Type | Required | Applies to | Default | Meaning |
|---|---|---|---|---|---|
| `version` | Integer | No | All providers | `1` | Schema version. Workcell accepts only `1`. |
| `includes` | Path array | No | All providers | None | Operator-owned policy fragments. Paths stay in the entrypoint tree. |
| `documents` | Table | No | Providers with document support | None | Instruction document sources. |
| `credentials` | Table | No | Credential owner | None | Provider and shared GitHub credential sources. |
| `ssh` | Table | No | All providers | None | SSH configuration, hosts, and identities. |
| `copies` | Table array | No | All providers | None | Files or directories for non-reserved targets. |
| `network` | Table | No | All providers | None | Endpoint additions and removals. Colima enforces the result only with `NETWORK_POLICY=allowlist`. |
<!-- schema:root:end -->

An include path is relative to the file that contains it. Workcell rejects cycles,
repeated files, and paths outside the entrypoint tree.

### Document keys

<!-- schema:documents:begin -->
| Key | Type | Required | Applies to | Default | Meaning |
|---|---|---|---|---|---|
| `common` | Path | No | Providers with document support | None | Provider-neutral instructions. |
| `codex` | Path | No | Codex | None | Codex instructions. |
| `claude` | Path | No | Claude | None | Claude instructions. |
| `gemini` | Path | No | Gemini | None | Gemini instructions. |
<!-- schema:documents:end -->

Copilot has no document key. Its managed path disables custom instructions.

### Credential keys

<!-- schema:credentials:begin -->
| Key | Type | Required | Applies to | Default | Meaning |
|---|---|---|---|---|---|
| `claude_auth` | Path or table | No | Claude | None | Auth mirrors in `~/.claude/`, `~/.claude.json`, and `~/.config/claude-code/`. |
| `claude_api_key` | Path or table | No | Claude | None | Helper-backed Claude API key. |
| `claude_mcp` | Path or table | No | Claude | None | MCP configuration at `~/.mcp.json`. This is not an auth mode. |
| `codex_auth` | Path or table | No | Codex | None | Codex auth at `~/.codex/auth.json`. |
| `copilot_github_token` | Path or table | No | Copilot | None | Export as `COPILOT_GITHUB_TOKEN` only to the managed child. |
| `gemini_env` | Path or table | No | Gemini | None | Gemini configuration at `~/.gemini/.env`. |
| `gemini_oauth` | Path or table | No | Gemini | None | OAuth state at `~/.gemini/oauth_creds.json`. |
| `gemini_projects` | Path or table | No | Gemini | None | Project registry at `~/.gemini/projects.json`. This is supplemental input. |
| `gcloud_adc` | Path or table | No | Gemini | None | ADC at `~/.config/gcloud/application_default_credentials.json`. This is supplemental input. |
| `github_hosts` | Table | No | Claude, Codex, Gemini | None | Shared auth at `~/.config/gh/hosts.yml`. Set a provider list. |
| `github_config` | Table | No | Claude, Codex, Gemini | None | Shared config at `~/.config/gh/config.yml`. Set a provider list. |
<!-- schema:credentials:end -->

Each value can be a direct path or an entry table. The shared GitHub keys must
use a table with `providers`.

Copilot does not receive shared GitHub CLI state.

### Resolver entry keys

The resolver reads these keys before it renders the policy:

<!-- schema:credentials-entry-resolver:begin -->
| Key | Type | Required | Applies to | Default | Meaning |
|---|---|---|---|---|---|
| `source` | Path | One of `source` or `resolver` | All credential keys | None | Direct host source outside the workspace. |
| `resolver` | String | One of `source` or `resolver` | Supported resolver keys | None | Built-in host resolver. It cannot occur with `source`. |
| `materialization` | `ephemeral` or `persistent` | No | Resolver entries | `ephemeral` | Resolved-file lifetime. Auth resolvers require `ephemeral`. |
| `providers` | Provider array | No | All credential keys | In-scope providers | Provider selector. Shared GitHub keys require it. |
| `modes` | Mode array | No | All credential keys | All modes | Mode selector. |
<!-- schema:credentials-entry-resolver:end -->

The supported resolvers are:

- `codex-home-auth-file` for `codex_auth`
- `claude-macos-keychain` for `claude_auth`

The Claude resolver records the intended source. It stops the launch because
Workcell does not provide a supported export path.

### Rendered credential entry keys

The resolver removes `resolver` and `materialization`. It writes a staged
`source` for the renderer.

<!-- schema:credentials-entry:begin -->
| Key | Type | Required | Applies to | Default | Meaning |
|---|---|---|---|---|---|
| `source` | Path | Yes | All credential keys | None | Staged or direct host source outside the workspace. |
| `providers` | Provider array | No | All credential keys | In-scope providers | Provider selector. Shared GitHub keys require it. |
| `modes` | Mode array | No | All credential keys | All modes | Mode selector. |
<!-- schema:credentials-entry:end -->

### SSH keys

<!-- schema:ssh:begin -->
| Key | Type | Required | Applies to | Default | Meaning |
|---|---|---|---|---|---|
| `enabled` | Boolean | No | All providers | Inferred | Explicit SSH injection switch. |
| `config` | Path | No | All providers | None | SSH configuration file. |
| `known_hosts` | Path | No | All providers | None | Known-hosts file. |
| `identities` | Path array | No | All providers | None | Private-key identity files. |
| `providers` | Provider array | No | All providers | All providers | Provider selector. |
| `modes` | Mode array | No | All providers | All modes | Mode selector. |
| `allow_unsafe_config` | Boolean | No | All providers | `false` | Accept an SSH configuration with unsafe directives. |
<!-- schema:ssh:end -->

Workcell rejects group-writable or world-writable `known_hosts` files. It
requires owner-only identity files.

`allow_unsafe_config` lowers assurance. It does not forward `SSH_AUTH_SOCK`.
It skips the SSH directive safety check. It permits `Include` to load
other configuration. It permits `LocalCommand`, `PermitLocalCommand`, and
`ProxyCommand` to run commands. It permits `PKCS11Provider` and
`SecurityKeyProvider` to load provider libraries.

The displayed assurance status for the session does not change.

### Copy keys

<!-- schema:copies:begin -->
| Key | Type | Required | Applies to | Default | Meaning |
|---|---|---|---|---|---|
| `source` | Path | Yes | All providers | None | Host file or directory. |
| `target` | Container path | Yes | All providers | None | Destination below `/state/agent-home` or `/state/injected`. |
| `classification` | `public` or `secret` | Yes | All providers | None | Security controls and file mode. |
| `providers` | Provider array | No | All providers | All providers | Provider selector. |
| `modes` | Mode array | No | All providers | All modes | Mode selector. |
<!-- schema:copies:end -->

Workcell rejects writes to reserved control-plane targets. It checks secret
sources for owner-only access and stages them as read-only.

### Network keys

<!-- schema:network:begin -->
| Key | Type | Required | Applies to | Default | Meaning |
|---|---|---|---|---|---|
| `allow_endpoints` | `host:port` or `[ipv6]:port` array | No | All providers | None | Add exact endpoint entries. Colima enforces the result only with `NETWORK_POLICY=allowlist`. |
| `deny_endpoints` | `host:port` or `[ipv6]:port` array | No | All providers | None | Remove exact entries after all additions. |
<!-- schema:network:end -->

Each port must be 1 through 65535.

Provider selectors accept `claude`, `codex`, `copilot`, and `gemini`.

Mode selectors accept `strict`, `development`, `build`, and `breakglass`.

## Credential use

Direct staged files are the primary supported credential path. See the
[provider bootstrap matrix](provider-bootstrap-matrix.md) for maturity and live
evidence.

Workcell supports `copilot_github_token` through the staged credential path.
For Copilot, Workcell does not use host GitHub CLI or Copilot state.

The managed Copilot wrapper removes the staged token from direct mounts. It
uses a temporary host mount and a transient runtime file. The wrapper deletes
the runtime file before it starts the managed child.

The wrapper exports `COPILOT_GITHUB_TOKEN` only to that child. The Workcell
entrypoint stays as PID 1 and scrubs its environment.

See the [GitHub Copilot CLI delivery record](copilot-linux-local-compat-plan.md)
for the complete token-transfer controls.

Workcell does not support Google Antigravity CLI. Do not put Antigravity keys
in an operator policy.

## Network rules

The launcher builds `ALLOW_ENDPOINTS` from these sources:

- `provider_endpoints`.
- `provider_auth_recovery_extra_endpoints` for Gemini without selected auth.
- `target_broker_endpoints`.
- `credential_extra_endpoints`.
- Versioned profile `EXTRA_ENDPOINTS`.
- `[network].allow_endpoints`.
- `snapshot-cloudflare.debian.org:443` and `snapshot.debian.org:443` for a
  non-remote ephemeral launch.

It removes every `[network].deny_endpoints` entry from the session list and the
list for bootstrap build containers.

On `colima` with `NETWORK_POLICY=allowlist`, Workcell enforces the result with
IPv4 and IPv6 `DOCKER-USER` rules. The rules allow resolved IP addresses and
ports. They do not filter TLS host names.

The enforcement scope is one Colima profile. It is not one session. The last
launch replaces the rules for all active containers in that profile.

Workcell does not isolate the profile-wide egress rules for concurrent sessions
that use different complete endpoint sets. A `breakglass` launch clears the
Workcell allowlist for the profile. That clear state remains until a later
allowlist launch applies new rules.

Do not run sessions with different complete endpoint sets at the same time in
one profile.

`allow_endpoints` broadens the allowed set. `deny_endpoints` removes exact
entries after all additions. Policy cannot change `NETWORK_POLICY` or disable
enforcement directly.

A denied host can share an IP address with an allowed host. In that case, the
denied host stays reachable at the IP layer.

Host-side rebuild downloads do not use the Colima firewall.

Other targets do not receive this allowlist. Their launch summary reports
`egress_enforcement=none`.

| Target state | `egress_enforcement` | Workcell enforcement |
|---|---|---|
| Colima with allowlist | `allowlist` | Profile-wide IPv4 and IPv6 rules |
| Colima with unrestricted network | `none` | None |
| Docker Desktop | `none` | None |
| `aws-ec2-ssm` preview | `none` | None |
| `gcp-vm` preview | `none` | None |

The remote targets are launch-blocked. Their preview plans rely on provider
firewall controls if live launch support ships later.

## Instruction precedence

Adapters with native document support use this order:

1. Adapter baseline document.
2. Repository `AGENTS.md`.
3. Repository provider overlay, such as `CLAUDE.md` or `GEMINI.md`.
4. `documents.common`.
5. Provider-specific policy document.

The Copilot adapter masks `.github/copilot-instructions.md`,
`.github/instructions`, and `.github/copilot`. Its managed wrapper disables
custom instructions.

## Examples

Keep each source outside the workspace. Use owner-only permissions for secret
sources.

### Provider credentials

```toml
version = 1

[credentials]
codex_auth = "/home/example/secrets/codex-auth.json"
claude_auth = "/home/example/secrets/claude-auth.json"
copilot_github_token = "/home/example/secrets/copilot-token.txt"
gemini_env = "/home/example/secrets/gemini.env"
```

Provider-native keys apply only to the provider that owns them.

### Shared GitHub CLI state

```toml
[credentials.github_hosts]
source = "/home/example/secrets/gh-hosts.yml"
providers = ["codex", "claude", "gemini"]

[credentials.github_config]
source = "/home/example/secrets/gh-config.yml"
providers = ["codex", "claude", "gemini"]
```

### Network changes

```toml
[network]
allow_endpoints = ["registry.internal.example:443"]
deny_endpoints = ["chatgpt.com:443"]
```

See [the complete example](examples/injection-policy.toml) for documents,
credentials, and copies.

## Explicit limits

- The safe path does not accept arbitrary environment variables that contain
  secrets.
- The safe path does not pass a complete host home.
- `[[copies]]` cannot write Workcell control-plane paths.
- Workcell does not forward `SSH_AUTH_SOCK`.
- Processes in one session do not receive isolation from each other.
- Copilot does not receive host provider homes, keychains, or ambient CLI auth.
- Strict mode does not set provider telemetry or content-capture variables.
- The Colima egress allowlist is profile-wide and uses IP addresses and ports.
