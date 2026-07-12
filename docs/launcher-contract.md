# Launcher contract

`scripts/workcell` is the host-side launcher. Historically it was a single
monolithic script; roadmap item D4 decomposes it into small, sourced,
behaviour-preserving modules under `scripts/lib/launcher/`. The launcher
sources each module from its `source "${ROOT_DIR}/scripts/lib/..."` block
(after `ROOT_DIR` is resolved) so every extracted helper is defined before its
first call site.

This document records the contract of each extracted module. It will grow as
further modules are pulled out of the launcher.

## Launcher reference

The sections below record the cross-cutting launcher contract that the module
sections then refine: the trusted host tools it requires, the environment it
pins and scrubs, the exit codes it uses, and the harness-only override flags it
recognises. Every claim here is derived from `scripts/workcell`,
`scripts/lib/launcher/*.sh`, and `scripts/lib/*.sh`.

### Required host tools

The launcher avoids bare, unpinned `PATH` lookups for host binaries: every host
tool is either absolute-resolved or run by name on the pinned `TRUSTED_HOST_PATH`
(never an inherited `PATH`). Most tools (`go`, `colima`, `docker`, and `git`'s
`HOST_GIT_BIN`) are absolute-resolved through one of two resolvers, both of which
abort the launch (printing `Missing trusted host tool: <name>` to stderr and
exiting `1`) when no trusted candidate is executable; a couple (`git` and `curl`
via `run_clean_host_command`) are instead run by name on `TRUSTED_HOST_PATH`, as
the rows and notes below detail. The two resolvers are:

- `resolve_fixed_host_tool <name> <candidate>...`
  (`scripts/lib/launcher/host-exec.sh`) returns the first caller-supplied candidate
  that is executable (`-x`) — no `PATH` fallback, but also **no canonicalisation**:
  it trusts the literal candidate path as written, so a fixed candidate that is a
  symlink is followed by the kernel without re-validating the target. Trust here
  rests on those hard-coded paths not being attacker-writable.
- `resolve_host_tool <name> <candidate>...` (`scripts/workcell`) tries the fixed
  candidates first, then falls back to `type -P <name>`, but every path **and its
  canonicalised form** must pass `is_trusted_host_tool_path` before it is accepted
  (`workcell:6660-6661`), so it cannot resolve to a target outside the trusted
  prefixes. Its `resolve_host_tool_optional` sibling is identical but returns `1`
  instead of aborting, so it is used for non-fatal capability probes.

| Tool | Resolver | Candidate paths | Purpose |
| --- | --- | --- | --- |
| `go` | `resolve_fixed_host_tool` (`go-hostutil.sh:29`) | `/opt/homebrew/bin/go`, `/usr/local/go/bin/go`, `/usr/local/bin/go`, `/usr/bin/go` | Runs the `workcell-hostutil` and `workcell-colimautil` Go programs via `go run` (see `HOST_GO_BIN`). |
| `colima` | `resolve_host_tool` (`workcell:625,670,5492`); `resolve_host_tool_optional` probe (`workcell:6713`) | `/opt/homebrew/bin/colima`, `/usr/local/bin/colima` | Manages the Colima VM profile that backs the container runtime (`HOST_COLIMA_BIN`). |
| `docker` | Fail-closed `resolve_host_tool`: a transient `HOST_DOCKER_BIN="docker"` default (`workcell:8136`) is **overridden** by `prepare_current_target_docker_client` (called in the main launch path at `workcell:8352`), which resolves it to an absolute, canonical-validated path (`resolve_host_tool docker`, `workcell:866`) before any Docker use; other sub-paths probe with `resolve_host_tool_optional` (`workcell:850,1663,6721`) | `/opt/homebrew/bin/docker`, `/usr/local/bin/docker`, `/Applications/Docker.app/Contents/Resources/bin/docker` | Drives the container runtime (`HOST_DOCKER_BIN`), run via `run_workcell_docker_client_command` (`trusted-docker-client.sh:151`). |
| `git` | `resolve_host_tool` → absolute `HOST_GIT_BIN` (`workcell:4462,8135`); **also** invoked by name via `run_clean_host_command` (`workcell:7092,7163,7190,7213,7232,7372`) | `/usr/bin/git`, `/opt/homebrew/bin/git`, `/usr/local/bin/git` | Inspects and extracts the launch workspace repository. |
| `curl` | **sanitised-PATH by name only** (no absolute resolve) via `run_clean_host_command` (`workcell:6241,6270`) | found on `TRUSTED_HOST_PATH` inside the sanitised `env -i` | Best-effort `HEAD` preflight for release URLs; **optional** — absence is swallowed (`\|\| true`), not fatal. |

A resolver-backed tool that is absent aborts the launch rather than falling back
to an attacker-controlled binary — but the resolvers differ in strength, as above:
`resolve_host_tool` (`colima`, `docker`, and `git`'s `HOST_GIT_BIN`) validates the
canonical target, while `resolve_fixed_host_tool` (`go`) trusts its literal fixed
paths. Two tools are instead run **by name on the sanitised `TRUSTED_HOST_PATH`**
rather than absolute-resolved: `git` (in its `run_clean_host_command` uses) and
`curl`. These stay confined to the trusted `PATH` but are **not**
canonical-target-validated and **not** fail-closed — e.g. a missing `curl` leaves
the preflight URL empty rather than raising `Missing trusted host tool`, so `curl`
is optional, not required.

`go` additionally has a **second, differently-scoped** resolution path: the
`--audit-transcript` PTY helper (`scripts/lib/pty_transcript`, `workcell:8778`)
sources `go-run-env.sh`, whose Go-binary resolver honours a pre-set
`WORKCELL_GO_BIN`, then `command -v go`, then the fixed candidates
(`go-run-env.sh:47-71`). Like the fixed resolver it is **fail-closed** — it exits
`1` with `Missing required tool: go` when none is found (`go-run-env.sh:66-68`) —
but it is **not confined to fixed trusted paths** (a `command -v go` hit is
accepted) and `WORKCELL_GO_BIN` is not in the scrub list, so on the
shebang-bypassed `bash` path an inherited `WORKCELL_GO_BIN` can select the `go`
used for the transcript.

This table covers the host tools a **local** launch may resolve; the exact set
depends on the target backend — `missing_launch_host_tools_csv` probes `colima`
only when `TARGET_BACKEND == "colima"`, whereas a Docker Desktop target
(`--target docker-desktop`) probes Docker/its context instead, and `curl` is used
only by the best-effort release-URL preflight. Beyond per-launch resolution, a few
prerequisites sit outside this table entirely. Remote-preview backends probe extra tools via
`missing_launch_host_tools_csv` (`scripts/workcell:6708`) — `aws` and
`session-manager-plugin` for the `aws-ec2-ssm` backend, `gcloud` for `gcp-vm`.
Image builds require a system `docker-buildx` plugin binary
(`ensure_workcell_trusted_buildx`, `scripts/lib/trusted-docker-client.sh`), which
aborts with `Missing trusted docker-buildx binary` when none is found. A
pre-set, executable `WORKCELL_TRUSTED_BUILDX_BIN` short-circuits that search and
is honored as-is; the launcher's `env -i` shebang (line 1) clears it on any
normal invocation, so this override survives only for shebang-bypassed
invocations (`/bin/bash -p scripts/workcell`) that inherit an
attacker-controlled value. Such invocations already run outside the pinned
entrypoint and are out of the launcher's trust boundary. And the
`workcell publish-pr` subcommand resolves `gh` on its non-dry-run path (required;
via the Go publish helper `internal/publishpr`, `ResolveHostTool(ctx, "gh", true,
…)`), failing with `Missing trusted host tool: gh` at publication time if it is
absent.

### Environment expectations

The launcher pins its own process environment before doing any work, then
derives a small set of host-context variables.

**Pinned `PATH`.** On direct execution the launcher re-execs itself with a
**cleared** environment: the shebang (`scripts/workcell:1`) is
`#!/usr/bin/env -S -i PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin BASH_ENV= ENV= /bin/bash`,
so `env -i` discards the inherited environment and starts `bash` with only the
fixed `PATH` and cleared `BASH_ENV`/`ENV`. **This wholesale clear only happens
when the shebang is honored.** When the launcher is instead invoked directly by an
interpreter the shebang is bypassed, `env -i` never runs, and only the explicit
`scrub_host_process_env` list (below) plus the `PATH` reset apply — and the two
such forms differ on startup-file vectors:

- `/bin/bash -p scripts/workcell` (the privileged form the repo's own
  verify-invariants and publish-pr harnesses use) ignores `BASH_ENV`/`ENV`, so
  those vectors are inert even before the scrub.
- plain `bash scripts/workcell` (no `-p`) **processes `BASH_ENV` before the script
  body runs**, so a `BASH_ENV` startup-file injection has already executed by the
  time `scrub_host_process_env` unsets the variable — the scrub is *not* a defence
  against it on that path.

Either way, security reasoning must not assume unlisted variables are absent on a
shebang-bypassed launch. In both cases the `PATH` string is frozen as
`readonly TRUSTED_HOST_PATH` and
re-exported as `PATH` (`scripts/workcell:5-6`), and it is the `PATH` handed to
every sanitised host command via `env -i PATH="${TRUSTED_HOST_PATH}"`
(`host-exec.sh:49,77`).

**Scrubbed variables.** `scrub_host_process_env` (`scripts/workcell:7-30`, called
at `scripts/workcell:32`) unsets, in order:

- `BASH_ENV`, `ENV` — startup-file injection vectors;
- `WORKCELL_TEST_FAIL_AFTER_PROFILE_REFRESH` — always unset (the fault-injection
  hook is reachable only through a CLI flag, below);
- `WORKCELL_TEST_SUPPORT_MATRIX_HOST_OS`, `_ARCH`, `_DISTRO`, `_DISTRO_VERSION` —
  unset **unless** `WORKCELL_VERIFY_INVARIANTS_SANITIZED_ENTRYPOINT` is `1`;
- `PYTHONPATH`, `PYTHONHOME`, `PYTHONSAFEPATH`, `PYTHONSTARTUP`;
- `RUBYOPT`, `RUBYLIB`, `GEM_HOME`, `GEM_PATH`;
- `PERL5OPT`, `PERL5LIB`, `PERLLIB`, `PERL_MB_OPT`, `PERL_MM_OPT`;
- `LD_PRELOAD`, `LD_LIBRARY_PATH`, `LD_AUDIT`, `LD_DEBUG`, `LD_DEBUG_OUTPUT`,
  `LD_BIND_NOW`, `LD_ASSUME_KERNEL`;
- every `DYLD_*` variable found via `compgen -v`.

**Derived / exported variables.** After scrubbing, the launcher establishes:

| Variable | Value | Mechanism |
| --- | --- | --- |
| `REAL_HOME` | resolved host home | `resolve_workcell_real_home` (`workcell:71`, from `trusted-docker-client.sh`) |
| `WORKCELL_GO_CACHE_ROOT` (exported) | `${REAL_HOME}/Library/Caches/workcell/go` on Darwin, else `${XDG_CACHE_HOME:-${REAL_HOME}/.cache}/workcell/go` | `workcell:73-79` |
| `COLIMA_STATE_ROOT` | `${REAL_HOME}/.colima` | `workcell:90`; passed to `colima` as `COLIMA_HOME` (`workcell:632`) |
| `GOPATH` / `GOMODCACHE` / `GOCACHE` (exported) | `${cache_root}/gopath`, `/mod-cache`, `/build-cache` under `WORKCELL_GO_CACHE_ROOT` | `ensure_go_run_env` in `go-run-env.sh:30-44` (honours pre-set values) |

`REAL_HOME` and `WORKCELL_GO_CACHE_ROOT` are deliberately established *before*
`go-hostutil.sh` is sourced (`workcell:80-85`), so the wrappers never run before
the host home and default Go cache root are fixed. This is an **ordering**
guarantee, not full confinement: `ensure_go_run_env` **honours a pre-set**
`GOPATH`/`GOMODCACHE`/`GOCACHE` (`go-run-env.sh:33-35`), and those three are not in
the `scrub_host_process_env` list, so on the shebang-bypassed
`bash scripts/workcell` path (above) a caller-supplied Go cache can persist — the
wrappers are guaranteed a *defined* cache root, not necessarily the default one.

### Exit codes

The launcher's **own** exit codes — those it originates for its own
skip/failure conditions — are:

| Code | Meaning | Representative sources |
| --- | --- | --- |
| `0` | Success, or an intentional clean skip (no work to do). | `workcell:3651` (no session id → skip), `workcell:8147` (logs shown) |
| `1` | General failure. In particular, the trusted-tool resolvers abort here when a required host tool is missing. | `host-exec.sh` `resolve_fixed_host_tool`, `workcell:6676` `resolve_host_tool` |
| `2` | Usage / validation / precondition guard failure — by far the most common code. Covers invalid CLI arguments, mode-requirement violations, reserved-variable rejection, a missing host working directory, and Colima profile validation failures. | `workcell:8151,8155` (reserved vars), `workcell:8614-8616` (profile validation), `run_clean_host_command_in_dir` missing-dir (`host-exec.sh`) |
| `88` | Test-only fault injection: a simulated crash immediately after a managed-profile refresh, used by harness recovery tests. Reached only through the hidden CLI flag below. | `maybe_fail_after_profile_refresh_for_tests` (`workcell:2625`) |
| `124` | A managed operation timed out — e.g. `colima start` exceeding `WORKCELL_COLIMA_START_TIMEOUT_SECONDS`. The timeout wrapper returns `124` and `set -e` propagates it, so the launcher itself exits `124` (matching `stability-contract.md`). | `run_command_with_debug_log` / `start_managed_profile` timeout path |

`0`/`1`/`2`/`88` are the *literal* `exit` codes in `scripts/workcell`
(`grep -nE 'exit [0-9]'`); `124` is originated indirectly, via `set -e`
propagating a timeout wrapper's non-zero return. Beyond the codes it originates,
a normal end-to-end invocation also **passes the supervised child's exit status
through unchanged** — a completed session exits with the container's status
(`exit "${DOCKER_STATUS}"`, `workcell:8797`), a build failure with
`${BUILD_STATUS}` (`workcell:8698`), and session subcommands propagate their
helper's status (`exit $?`, e.g. `workcell:4182,4188`). So an end-to-end run can
surface any status in `0`–`255` (including `128+N` for a signalled child);
[`stability-contract.md`](stability-contract.md) is the authoritative exit-code
reference.

### Test override flags

These variables exist for the validation harness / CI, not for operators. Each is
gated — hard-rejected, unconditionally unset, or ignored unless its harness
conditions hold — so a normal launch cannot *act* on one. Note the gate that
decides whether a value is **scrubbed** is, for the support-matrix vars, weaker
than the gate that decides whether it is **honoured** (see their rows).

| Flag | Overrides | Gating |
| --- | --- | --- |
| `WORKCELL_VERIFY_INVARIANTS_SANITIZED_ENTRYPOINT` | Two distinct effects: (a) at startup its presence (`=1`) makes `scrub_host_process_env` **skip unsetting** the four support-matrix vars; (b) it is one condition the detectors check before honouring those overrides. | Effect (a) is gated **only** on the `=1` marker (`workcell:12-17`) — *not* parent-verified. Effect (b) additionally requires `support_matrix_host_override_allowed` to confirm a recognised validation-harness parent (`host-detect.sh`). |
| `WORKCELL_TEST_SUPPORT_MATRIX_HOST_OS` / `_ARCH` / `_DISTRO` / `_DISTRO_VERSION` | Forces the detected host OS / arch / distro / distro version. | **Unset at startup only when the `SANITIZED_ENTRYPOINT` marker is absent** (`workcell:12-17`); with the marker set they survive startup even under a non-harness parent, but the detectors **honour** them only when `support_matrix_host_override_allowed` returns `0` (marker **and** recognised parent). So on the shebang-bypassed `bash scripts/workcell` path they can be *present but ignored*, not guaranteed absent. |
| `WORKCELL_TEST_CODEX_AUTH_FILE` | Reserved harness variable name — **rejected legacy input**, not a credential override: no launcher resolver consumes it (the Codex resolver no longer recognises it; see `docs/security/`). | If non-empty in the launcher's own environment the launcher **refuses to run**: `exit 2`, "reserved for the Workcell test harness" (`workcell:8150-8152`). |
| `WORKCELL_TEST_CLAUDE_KEYCHAIN_EXPORT_FILE` | **Dual role.** An *operator-supplied* value is rejected; but the launcher's own self-staging probe (`--synthetic-claude-export`, `workcell:4804`) sets this var **internally** so the Claude credential resolver materialises the synthetic export (`internal/injection/prepare_bundle.go:434`, `internal/authresolve/resolve_provider_credentials.go:16`). | Reject-on-presence guard for *inherited* operator input: `exit 2` if set in the launcher's own env (`workcell:8154-8156`). The internal staging happens later, inside bundle prep, after that guard. |
| `WORKCELL_TEST_FAIL_AFTER_PROFILE_REFRESH` | Would request the mid-refresh fault injection. | The env variable is **unconditionally unset** at startup (`workcell:11`). The fault is reachable only through the hidden CLI flag `--test-fail-after-profile-refresh`, which sets the internal `TEST_FAIL_AFTER_PROFILE_REFRESH=1` (`workcell:7883-7884`) and triggers `exit 88`. |

These gates are **harness conventions, not a security boundary against a local
operator**. The support-matrix "parent" check is a *substring match* on the parent
process command line (below), not a verified process identity, so a caller who
controls their own process tree can construct a matching command line. It deters
accidental leakage from an ordinary launch; it is not a defence against deliberate
spoofing by someone who already runs the launcher (who, being local, controls that
environment anyway). The credential-fixture variables are stricter — they force an
`exit 2` abort whenever present. The authoritative host/credential threat model
lives in the security docs under `docs/security/`, not here.

## Host detection (`host-detect.sh`)

`scripts/lib/launcher/host-detect.sh` normalises the host environment into the
lowercased values the launcher's support-matrix logic consumes. Every helper
depends only on `uname`/`ps`/`PPID`/environment variables and each other — no
other launcher function — which makes the module self-contained.

All emitted values are lowercased and newline-terminated. Values are normalised
to canonical tokens where recognised, and otherwise passed through lowercased.

### `support_matrix_host_override_allowed()`

Gate that decides whether the harness-only host overrides (below) are honoured.
Returns `0` (allowed) only when **both** hold:

- `WORKCELL_VERIFY_INVARIANTS_SANITIZED_ENTRYPOINT` is `1`; and
- the parent process command line (read from `/proc/${PPID}/cmdline`, falling
  back to `ps -p "${PPID}"`) matches one of the recognised validation harness
  entrypoints (`scripts/verify-invariants.sh` or the shared dry-run scenario
  scripts).

Returns non-zero otherwise. This keeps the overrides usable by Linux
validation runners exercising launch assembly without weakening the real,
operator-facing support boundary.

### `detected_host_os()`

Prints the normalised host operating system: `macos` (Darwin), `linux`
(Linux), or `windows` (MINGW/MSYS/CYGWIN/`Windows_NT`). Any other `uname -s`
value is passed through lowercased.

### `detected_host_arch()`

Prints the normalised host architecture: `arm64` (`arm64`/`aarch64`) or `amd64`
(`x86_64`/`amd64`). Any other `uname -m` value is passed through lowercased.

### `detected_host_distro()`

On non-Linux hosts prints `none`. On Linux, prints the lowercased `ID` from
`/etc/os-release`, or `unknown` when it cannot be determined.

### `detected_host_distro_version()`

On non-Linux hosts prints `none`. On Linux, prints the lowercased `VERSION_ID`
(falling back to `VERSION_CODENAME`) from `/etc/os-release`, or `unknown` when
it cannot be determined.

### Harness-only overrides

When `support_matrix_host_override_allowed` returns `0`, each detector first
honours a reserved override environment variable if it is non-empty, printing
its value verbatim:

- `WORKCELL_TEST_SUPPORT_MATRIX_HOST_OS`
- `WORKCELL_TEST_SUPPORT_MATRIX_HOST_ARCH`
- `WORKCELL_TEST_SUPPORT_MATRIX_HOST_DISTRO`
- `WORKCELL_TEST_SUPPORT_MATRIX_HOST_DISTRO_VERSION`

These variables are reserved for the validation harness. The launcher scrubs
them from the host process environment unless the sanitized-entrypoint marker
is set, so they cannot be smuggled in by an operator to spoof the detected host.

## Trusted host-command execution (`host-exec.sh`)

`scripts/lib/launcher/host-exec.sh` resolves fixed, trusted host tools and runs
host commands under a sanitised environment. Every helper depends only on
`env`/`cd` builtins, the readonly `TRUSTED_HOST_PATH`, `REAL_HOME` (used with a
fallback), and `resolve_workcell_real_home` (from
`scripts/lib/trusted-docker-client.sh`) — no other launcher function — which
makes the module self-contained.

### `resolve_fixed_host_tool()`

Resolves a trusted host tool from an allowlist of absolute candidate paths.
Called as `resolve_fixed_host_tool <name> <candidate>...`; prints the first
candidate that is executable (`-x`) and returns `0`. If no candidate is
executable it prints `Missing trusted host tool: <name>` to stderr and exits
`1`. Only fixed, caller-supplied absolute paths are considered, so the resolved
binary never depends on a `PATH` search.

### `run_clean_host_command()`

Runs a host command under a sanitised environment. With no arguments it is a
no-op returning `0`. Otherwise it resolves the host home (`REAL_HOME`, falling
back to `resolve_workcell_real_home`, then `/` if neither is a directory),
`cd`s into that home, and execs the command via `env -i` with only
`PATH="${TRUSTED_HOST_PATH}"`, `HOME`, and `LC_ALL=C`/`LANG=C` set. The command
runs in a subshell so the launcher's own working directory and environment are
unaffected, and the command's exit status is propagated.

### `run_clean_host_command_in_dir()`

Same sanitised `env -i` execution as `run_clean_host_command()`, but runs the
command from a caller-supplied working directory. Called as
`run_clean_host_command_in_dir <dir> <cmd>...`. If `<dir>` is not a directory it
prints `Missing host working directory: <dir>` to stderr and exits `2`. With no
command arguments it is a no-op returning `0`. `HOME` resolution and the pinned
`PATH`/locale are identical to `run_clean_host_command()`.

## Go/Colima host-utility wrappers (`go-hostutil.sh`)

`scripts/lib/launcher/go-hostutil.sh` invokes the `workcell-hostutil` and
`workcell-colimautil` Go programs on the host via `go run`, always routed
through `run_clean_host_command_in_dir` (from `host-exec.sh`) so the child
executes from `${ROOT_DIR}` under the sanitised `env -i` host environment. Every
helper depends only on `ensure_go_run_env` plus the `GOPATH`/`GOMODCACHE`/
`GOCACHE` it exports (`scripts/lib/go-run-env.sh`),
`run_clean_host_command_in_dir` (`scripts/lib/launcher/host-exec.sh`), and the
`ROOT_DIR` global set in `scripts/workcell`. `HOST_GO_BIN` is resolved by this
module itself (via `resolve_fixed_host_tool` from `host-exec.sh`, sourced
immediately before), since these wrappers are its sole consumer — so every
dependency is sourced or assigned before the first wrapper call, which makes the
module self-contained.

### `go_hostutil()`

Runs `workcell-hostutil` on the host. Calls `ensure_go_run_env`, then executes
`"${HOST_GO_BIN}" run ./cmd/workcell-hostutil "$@"` from `${ROOT_DIR}` via
`run_clean_host_command_in_dir`, forwarding only `GOPATH`/`GOMODCACHE`/`GOCACHE`
into the sanitised environment. All other host context must be passed on argv
because the `env -i` boundary strips inherited environment variables.

### `run_go_hostutil_preserve_exit()`

Wraps `go_hostutil()` and recovers the Go child's real exit code. It captures
the child's stderr to a temp file (cleaned up via a `RETURN` trap and
explicitly), and if the stderr ends in an `exit status N` trailer emitted by
`go run`, it substitutes `N` for the generic exit code `1` and strips the
trailer from the forwarded stderr. Non-trailer stderr is re-emitted unchanged
and the resolved exit code is returned.

### `go_hostutil_publish_pr()`

Same `go run ./cmd/workcell-hostutil` invocation as `go_hostutil()`, but forwards
an explicit allowlist of terminal, GnuPG, SSH, XDG, and GitHub environment
variables (for example `TERM`, `GPG_TTY`, `GNUPGHOME`, `SSH_AUTH_SOCK`,
`GIT_ASKPASS`, the `XDG_*` dirs, `GH_TOKEN`/`GITHUB_TOKEN`, `GH_HOST`,
`GH_CONFIG_DIR`) — each added only when non-empty — so host-side PR publication
can reach the operator's credentials and interactive signing agents while the
rest of the environment stays sanitised.

### `go_colimautil()`

Runs `workcell-colimautil` on the host. Identical structure to `go_hostutil()`
(`ensure_go_run_env`, forwarding `GOPATH`/`GOMODCACHE`/`GOCACHE` from
`${ROOT_DIR}` via `run_clean_host_command_in_dir`), targeting
`./cmd/workcell-colimautil` instead.

## Egress-endpoint assembly (`egress-endpoints.sh`)

`scripts/lib/launcher/egress-endpoints.sh` computes the per-session network
egress allowlist and translates it into the container runtime's `--add-host`
arguments. It maps each provider, remote-VM broker, and injected credential to
its fixed `host:port` endpoint set, deduplicates and deny-subtracts the combined
list through the `workcell-hostutil` Go helper, fails closed when a deny rule
empties the allowlist, labels whether the launch actually enforces the
allowlist, and resolves the surviving endpoints into `--add-host` runtime args.
Every helper depends only on `csv_contains_value` (defined in
`scripts/workcell`), `go_hostutil` (`scripts/lib/launcher/go-hostutil.sh`, sourced
before this module), the read-only launch-state globals `AGENT`,
`INJECTION_CREDENTIAL_KEYS`, `NETWORK_POLICY`, and `TARGET_BACKEND`, and the
`RUNTIME_NETWORK_ARGS` array `build_runtime_host_aliases` populates — all defined
before the first call site in the main launch path — which makes the module
self-contained.

### `provider_endpoints()`

Prints the space-separated `host:port` egress set a provider requires
(`codex`, `claude`, `copilot`, `gemini`). Returns `1` for any unrecognised
provider so the caller sees no endpoints.

### `target_broker_endpoints()`

Prints the space-separated `host:port` control-plane endpoints a remote-VM
broker backend requires (`aws-ec2-ssm`, `gcp-vm`). Returns `0` (empty) for every
other target, including the local `colima` and `docker-desktop` backends.

### `credential_extra_endpoints()`

Prints additional endpoints implied by the injected credential set
(`INJECTION_CREDENTIAL_KEYS`): GitHub host/config credentials add the GitHub web
and raw-content hosts, and — only when `AGENT` is `gemini` — Google OAuth/ADC
credentials add the Google identity and AI-platform hosts. Prints nothing when
no credential implies extra egress.

### `dedupe_endpoint_list()` / `subtract_endpoint_list()`

Thin wrappers over the `workcell-hostutil helper` sub-commands
`dedupe-endpoints` and `subtract-endpoints`. `dedupe_endpoint_list` collapses the
combined allow set to unique endpoints preserving order; `subtract_endpoint_list`
removes every endpoint in the deny list from the allow list, preserving allow
order. Deny always wins over allow, and the subtraction only ever tightens the
allowlist — it can never add an endpoint or change `NETWORK_POLICY`.

### `fail_empty_egress_after_deny()`

Aborts fail-closed (`exit 1`) with an actionable diagnostic when
`[network].deny_endpoints` has removed every computed endpoint on the enforced
colima allowlist path, so a zero-egress session never launches. Called for both
the `bootstrap` and `session` phases.

### `egress_enforcement_label()`

Prints `allowlist` only when the launch actually enforces the per-session
default-deny allowlist — `TARGET_BACKEND` is `colima` **and** `NETWORK_POLICY` is
`allowlist` — and `none` for every other target, making the parity gap explicit
on the launch summary.

### `build_runtime_host_aliases()`

Resets and repopulates the `RUNTIME_NETWORK_ARGS` array. On the `allowlist`
policy it resolves the supplied endpoint list to IPs via
`workcell-hostutil helper resolve-endpoints` and appends one `--add-host
host:ip` argument per resolved address (bracketing IPv6 literals); on any other
policy it leaves `RUNTIME_NETWORK_ARGS` empty and returns.
