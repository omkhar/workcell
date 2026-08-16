# Launcher Contract

[`scripts/workcell`](../scripts/workcell) is the host launcher. It sources small
modules from `scripts/lib/launcher/`. This page records the current contract of
those modules.

## Launcher Reference

Direct execution uses the launcher shebang. The shebang starts Bash with an
empty environment, a fixed `PATH`, and empty `BASH_ENV` and `ENV` values. The
launcher also scrubs language startup and dynamic-loader variables.

Do not use `bash scripts/workcell` as a trusted entry point. That command skips
the shebang. Bash can process `BASH_ENV` before the script can scrub it.
Repository test harnesses can use `/bin/bash -p scripts/workcell`. Privileged
Bash does not process those startup files, but it still skips the empty shebang
environment.

The launcher gives clean host commands these values:

- The fixed trusted `PATH`.
- The resolved host home, or `/` if no valid home exists.
- `LC_ALL=C` and `LANG=C`.
- Only the additional variables that the applicable helper permits.

### Required host tools

The launcher does not use an inherited `PATH`. It uses fixed candidates or a
fixed trusted path.

| Tool | Resolution and use |
|---|---|
| Go | `resolve_fixed_host_tool` selects the first executable fixed candidate. Go runs the host utility commands. |
| Colima | `resolve_host_tool` selects and checks a trusted candidate for the Colima target. |
| Docker | Target preparation selects and checks the Docker client before Docker use. |
| Git | The launcher can use a checked absolute Git path. Clean host commands also call Git by name on the fixed path. |
| curl | A best-effort release URL check calls curl by name on the fixed path. curl is optional for that check. |
| AWS tools | The AWS preview probe checks `aws` and `session-manager-plugin`. |
| gcloud | The GCP preview probe checks `gcloud`. |
| gh | The host publication helper requires a trusted GitHub CLI path for a real publication. |

`resolve_fixed_host_tool` tests only whether a literal candidate path is
executable. It does not resolve and check a symlink target. Trust requires fixed
directories that an attacker cannot write to.

The main `resolve_host_tool` path also checks the canonical path against trusted
prefixes. Clean host commands that call a tool by name use the fixed path but do
not check the canonical target.

Image builds require a trusted Docker Buildx plug-in. A direct launch
clears a caller-supplied `WORKCELL_TRUSTED_BUILDX_BIN` value. A shebang-bypass
test process can keep caller-supplied environment values and is outside the
direct entry point contract.

The PTY transcript helper has its own Go resolver. It accepts
`WORKCELL_GO_BIN`, then a Go binary on its current path, then fixed candidates.
This resolver stops if it finds no Go binary. A direct launcher execution clears
the inherited environment. A shebang-bypass process can supply a different
value.

### Environment expectations

The launcher removes these environment values at startup:

- `BASH_ENV` and `ENV`.
- `WORKCELL_TEST_FAIL_AFTER_PROFILE_REFRESH` in all cases.
- The four support-matrix test values unless the sanitized-entry marker is `1`.
- Python, Ruby, and Perl startup or library values.
- Linux loader values such as `LD_PRELOAD` and `LD_LIBRARY_PATH`.
- Each `DYLD_*` value.

The launcher resolves the real host home before it sources Go host wrappers. It
sets a default cache root for Workcell Go tools. The Go environment helper supplies
`GOPATH`, `GOMODCACHE`, and `GOCACHE` when they have no value.

On a shebang-bypass path, a caller can preserve unlisted variables and existing
Go cache values. Thus, the direct shebang is part of the trusted launcher
contract.

### Exit codes

The launcher uses these status values:

| Status | Meaning |
|---|---|
| `0` | Success or an intentional no-work result |
| `1` | General failure, such as a missing required trusted tool |
| `2` | Usage, validation, or precondition failure |
| `88` | Hidden CLI test fault after a managed profile refresh |
| `124` | Managed operation timeout |

The launcher can also return the provider, container, build, or session-helper
status without change. Use [Stability Contract](stability-contract.md) for the
authoritative public exit behavior.

### Test override flags

The test inputs for host support can replace detected host fields only when both of
these conditions are true:

1. `WORKCELL_VERIFY_INVARIANTS_SANITIZED_ENTRYPOINT=1` is present.
2. The parent command line matches one of the listed repository validation
   harnesses.

This parent check searches for a substring in the command line. It prevents
accidental operator use. It is not a security boundary against a local operator
who controls the process tree.

The launcher rejects inherited `WORKCELL_TEST_CODEX_AUTH_FILE` and
`WORKCELL_TEST_CLAUDE_KEYCHAIN_EXPORT_FILE` values. The synthetic Claude probe
can set its internal fixture value only after the inherited-value check.

The launcher always removes an inherited
`WORKCELL_TEST_FAIL_AFTER_PROFILE_REFRESH` value. The hidden CLI test option has
no parent-process gate. It exits with status `88` only after a managed profile
refresh.

## Host detection (`host-detect.sh`)

[`host-detect.sh`](../scripts/lib/launcher/host-detect.sh) returns normalized,
lowercase host fields during detection without a test override. The module uses
`uname`, `ps`, `tr`, `PPID`, environment values, `/etc/os-release`, and its own
functions.

### `support_matrix_host_override_allowed()`

This function applies the two-part test gate described above.

### `detected_host_os()`

This function returns `macos`, `linux`, `windows`, or a lowercase unknown value.

### `detected_host_arch()`

This function returns `arm64`, `amd64`, or a lowercase unknown value.

### `detected_host_distro()`

This function returns the Linux `ID`, `unknown`, or `none` on a non-Linux host.

### `detected_host_distro_version()`

This function returns Linux `VERSION_ID`, then `VERSION_CODENAME`, `unknown`, or
`none`.

### Harness-only overrides

An allowed test override passes through without a case change.

## Trusted host-command execution (`host-exec.sh`)

[`host-exec.sh`](../scripts/lib/launcher/host-exec.sh) supplies three functions.

### `resolve_fixed_host_tool`

This function receives a tool name and fixed absolute candidates. It prints the
first executable candidate. If it finds none, it prints
`Missing trusted host tool: <name>` and exits with status `1`.

### `run_clean_host_command`

This function changes to the resolved host home and starts a command with an
empty environment. It supplies only the fixed path, home, and C locale. With no
command, it returns success.

### `run_clean_host_command_in_dir`

This function uses the same clean environment in a caller-selected directory.
If the directory does not exist, it prints a diagnostic and exits with status
`2`. With no command after the directory, it returns success.

## Go/Colima host-utility wrappers (`go-hostutil.sh`)

[`go-hostutil.sh`](../scripts/lib/launcher/go-hostutil.sh) resolves the fixed Go
binary and runs Go commands from the repository root in the clean host
environment.

### `go_hostutil()`

This function runs `cmd/workcell-hostutil` with the selected arguments.

### `run_go_hostutil_preserve_exit()`

This function recovers and returns the child status from Go's final
`exit status N` diagnostic.

### `go_hostutil_publish_pr()`

This function runs host publication with an explicit allowlist of terminal,
GPG, SSH, XDG, and GitHub variables.

### `go_colimautil()`

This function runs `cmd/workcell-colimautil` with the selected arguments.

### Colima stream and timeout controls

The launcher streams Colima inventory and status output directly to the host utility.
The container inventory, Colima inventory, and Colima status inputs each have a 4 MiB limit.

Managed Colima start uses the checked absolute Colima path. A positive timeout cannot exceed 24 hours.
For a positive timeout, the Go helper starts Colima in a dedicated process group.

On timeout, the helper sends `SIGTERM` and polls for group absence.
It sends `SIGKILL` if the group remains. It polls again and fails if the group remains.

After cancellation, the helper converts only expected termination outcomes to status `124` when cleanup succeeds.
It preserves ordinary exit statuses and unrelated signal statuses. It reports start, input/output, and cleanup errors.

Runtime DNS resolution uses a five-second deadline. The resolver cancels the lookup context when resolution finishes.

The publication function is a deliberate host-side exception. It can receive
the ambient operator publication and signing environment. The Tier 1 runtime
does not receive this ambient state. Reviewed injection can stage selected
GitHub CLI files and SSH identities. The supported pull-request publication
workflow stays on the host.

## Egress-endpoint assembly (`egress-endpoints.sh`)

[`egress-endpoints.sh`](../scripts/lib/launcher/egress-endpoints.sh) supplies
helpers for fixed provider, target broker, and credential endpoint values. It
also supplies list and host-alias helpers.

### `provider_endpoints()`

This function returns the fixed service endpoints for Codex, Claude, Copilot,
or Gemini.

### `target_broker_endpoints()`

This function adds the fixed AWS SSM or GCP IAP broker endpoints.

### `credential_extra_endpoints()`

This function adds fixed GitHub or Google authentication endpoints when the
selected staged inputs need them.

The main launcher combines these values with authentication-recovery,
injection-policy, profile-extra, and conditional Debian endpoints.

Use [Outbound Endpoints](outbound-endpoints.md) for the full endpoint inventory.

### `dedupe_endpoint_list()` / `subtract_endpoint_list()`

`dedupe_endpoint_list` preserves the first occurrence of each endpoint.
`subtract_endpoint_list` removes each denied endpoint and preserves the allow
order. A deny value cannot add an endpoint.

### `fail_empty_egress_after_deny()`

If deny values remove every session endpoint, the check stops an enforced
Colima allowlist session with status `1`. This session check does not apply to
`--prepare-only`. If deny values remove every bootstrap endpoint, the check
stops only when a rebuild must use that bootstrap set. A prepared image can
still launch when the session endpoint set is not empty.

### `egress_enforcement_label()`

`egress_enforcement_label` returns `allowlist` only for a Colima target with the
allowlist policy. It returns `none` for Docker Desktop and the blocked remote
preview targets.

### `build_runtime_host_aliases()`

`build_runtime_host_aliases` resolves the effective endpoint hosts and builds
Docker `--add-host` arguments. It does nothing when the network policy is not
`allowlist`. These aliases support deterministic resolution. The rule set in
the Colima VM supplies the actual managed egress enforcement.

## Change Rule

Keep each extracted module behavior-compatible with its call sites. When a
module contract changes, update its tests, this page, and the applicable
operator or security document. Make the updates in the same change.
