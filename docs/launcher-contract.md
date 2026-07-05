# Launcher contract

`scripts/workcell` is the host-side launcher. Historically it was a single
monolithic script; roadmap item D4 decomposes it into small, sourced,
behaviour-preserving modules under `scripts/lib/launcher/`. The launcher
sources each module from its `source "${ROOT_DIR}/scripts/lib/..."` block
(after `ROOT_DIR` is resolved) so every extracted helper is defined before its
first call site.

This document records the contract of each extracted module. It will grow as
further modules are pulled out of the launcher.

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
