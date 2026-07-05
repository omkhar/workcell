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

The launcher never trusts a bare `PATH` lookup for a host binary. It resolves
each tool to an absolute path through one of two resolvers, both of which abort
the launch (printing `Missing trusted host tool: <name>` to stderr and exiting
`1`) when no trusted candidate is executable:

- `resolve_fixed_host_tool <name> <candidate>...`
  (`scripts/lib/launcher/host-exec.sh`) considers only the fixed, caller-supplied
  absolute candidate paths — there is no `PATH` fallback.
- `resolve_host_tool <name> <candidate>...` (`scripts/workcell`) tries the fixed
  candidates first, then falls back to `type -P <name>`, but every path (and its
  canonicalised form) must pass `is_trusted_host_tool_path` before it is
  accepted. Its `resolve_host_tool_optional` sibling is identical but returns `1`
  instead of aborting, so it is used for non-fatal capability probes.

| Tool | Resolver | Candidate paths | Purpose |
| --- | --- | --- | --- |
| `go` | `resolve_fixed_host_tool` (`go-hostutil.sh:29`) | `/opt/homebrew/bin/go`, `/usr/local/go/bin/go`, `/usr/local/bin/go`, `/usr/bin/go` | Runs the `workcell-hostutil` and `workcell-colimautil` Go programs via `go run` (see `HOST_GO_BIN`). |
| `colima` | `resolve_host_tool` (`workcell:625,670,5492`); `resolve_host_tool_optional` probe (`workcell:6713`) | `/opt/homebrew/bin/colima`, `/usr/local/bin/colima` | Manages the Colima VM profile that backs the container runtime (`HOST_COLIMA_BIN`). |
| `docker` | `resolve_host_tool` (`workcell:866,3654,3722,3786,4028,4215`); `resolve_host_tool_optional` probe (`workcell:850,1663,6721`) | `/opt/homebrew/bin/docker`, `/usr/local/bin/docker`, `/Applications/Docker.app/Contents/Resources/bin/docker` | Drives the container runtime (`HOST_DOCKER_BIN`). |
| `git` | `resolve_host_tool` (`workcell:4462,8135`); also invoked by name through `run_clean_host_command` (`workcell:7092,7163,7190,7213,7232,7372`) | `/usr/bin/git`, `/opt/homebrew/bin/git`, `/usr/local/bin/git` | Inspects and extracts the launch workspace repository (`HOST_GIT_BIN`). |
| `curl` | invoked by name through `run_clean_host_command` (`workcell:6241,6270`) | resolved from `TRUSTED_HOST_PATH` inside the sanitised `env -i` | Performs `HEAD` preflight reachability checks. |

All resolutions restrict the binary to the trusted host tool directories, so a
required tool that is absent from those locations aborts the launch rather than
falling back to an attacker-controlled binary.

### Environment expectations

The launcher pins its own process environment before doing any work, then
derives a small set of host-context variables.

**Pinned `PATH`.** The launcher re-execs itself with a cleared environment. The
shebang (`scripts/workcell:1`) is
`#!/usr/bin/env -S -i PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin BASH_ENV= ENV= /bin/bash`,
so `env -i` discards the inherited environment and starts `bash` with only the
fixed `PATH` and cleared `BASH_ENV`/`ENV`. That same string is then frozen as
`readonly TRUSTED_HOST_PATH` and re-exported as `PATH` (`scripts/workcell:5-6`),
and it is the `PATH` handed to every sanitised host command via
`env -i PATH="${TRUSTED_HOST_PATH}"` (`host-exec.sh:49,77`).

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
`go-hostutil.sh` is sourced, so the Go host-utility wrappers can never run with
an unsanitised host home or Go cache root (`workcell:80-85`).

### Exit codes

The launcher uses four numeric exit codes:

| Code | Meaning | Representative sources |
| --- | --- | --- |
| `0` | Success, or an intentional clean skip (no work to do). | `workcell:3651` (no session id → skip), `workcell:8147` (logs shown) |
| `1` | General failure. In particular, the trusted-tool resolvers abort here when a required host tool is missing. | `host-exec.sh` `resolve_fixed_host_tool`, `workcell:6676` `resolve_host_tool` |
| `2` | Usage / validation / precondition guard failure — by far the most common code. Covers invalid CLI arguments, mode-requirement violations, reserved-variable rejection, a missing host working directory, and Colima profile validation failures. | `workcell:8151,8155` (reserved vars), `workcell:8614-8616` (profile validation), `run_clean_host_command_in_dir` missing-dir (`host-exec.sh`) |
| `88` | Test-only fault injection: a simulated crash immediately after a managed-profile refresh, used by harness recovery tests. Reached only through the hidden CLI flag below. | `maybe_fail_after_profile_refresh_for_tests` (`workcell:2625`) |

There are no other codes: `grep -nE 'exit [0-9]' scripts/workcell` yields only
`0`, `1`, `2`, and the single `88`.

### Test override flags

These variables exist for the validation harness / CI, not for operators. They
are gated so they cannot be smuggled in through a normal launch.

| Flag | Overrides | Gating |
| --- | --- | --- |
| `WORKCELL_VERIFY_INVARIANTS_SANITIZED_ENTRYPOINT` | Enables the host-detection overrides below (and suppresses their startup scrub). | Honoured only when set to `1` **and** `support_matrix_host_override_allowed` also confirms the parent process is a recognised validation-harness entrypoint (`host-detect.sh`). |
| `WORKCELL_TEST_SUPPORT_MATRIX_HOST_OS` / `_ARCH` / `_DISTRO` / `_DISTRO_VERSION` | Forces the detected host OS / arch / distro / distro version. | Honoured only when `support_matrix_host_override_allowed` returns `0`; otherwise unset by `scrub_host_process_env` at startup (`workcell:12-17`). |
| `WORKCELL_TEST_CODEX_AUTH_FILE` | Points the harness at a fixture Codex auth file (consumed by test subprocesses, never by the launcher). | If non-empty in the launcher's own environment the launcher **refuses to run**: `exit 2`, "reserved for the Workcell test harness" (`workcell:8150-8152`). |
| `WORKCELL_TEST_CLAUDE_KEYCHAIN_EXPORT_FILE` | Points the harness at a fixture Claude keychain export (consumed by test subprocesses, never by the launcher). | Same reject-on-presence guard: `exit 2` (`workcell:8153-8156`). |
| `WORKCELL_TEST_FAIL_AFTER_PROFILE_REFRESH` | Would request the mid-refresh fault injection. | The env variable is **unconditionally unset** at startup (`workcell:11`). The fault is reachable only through the hidden CLI flag `--test-fail-after-profile-refresh`, which sets the internal `TEST_FAIL_AFTER_PROFILE_REFRESH=1` (`workcell:7883-7884`) and triggers `exit 88`. |

Because the support-matrix overrides depend on both an explicit marker variable
and a verified parent entrypoint, and the credential-fixture variables cause an
outright abort when present, an operator cannot use any of these to spoof the
detected host or redirect credential handling.

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
