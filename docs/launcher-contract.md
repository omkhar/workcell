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
