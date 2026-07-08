#!/usr/bin/env -S BASH_ENV= ENV= bash
# Day-two install-lifecycle mechanics as repeatable evidence for G3:
# install -> upgrade-in-place -> rollback, all inside a sandboxed HOME so this
# repo-required scenario touches no real host state.
#
# SAFETY: the launcher's `--gc` and scripts/uninstall.sh both reap the
# hard-coded `/tmp` scratch root and passwd-derived real-home cache roots that a
# HOME/XDG/TMPDIR sandbox cannot contain (resolve_workcell_real_home prefers the
# passwd identity over $HOME, and `/tmp` is hard-coded), so invoking them live
# here could delete a developer's or CI runner's real Workcell temp/cache state.
# They are therefore NOT invoked live in this scenario:
#   - uninstall is proven end to end by the macOS install-verification CI lane;
#   - `--gc`'s cleanup contract is asserted below at the FUNCTION level against
#     an INJECTED sandbox root (never `/tmp`, never real home);
#   - the live end-to-end `--gc` exercise is local-operator-certification
#     remainder (see docs/install-lifecycle.md).
# Sentinels seeded in the real `/tmp` (and the real state root, when present)
# prove the whole scenario leaves real Workcell state untouched.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-install-lifecycle.XXXXXX")"
TMP_DIR="$(cd "${TMP_DIR}" && pwd -P)"
# The extracted cleanup-function snippet (see the --gc contract section) contains
# only self-contained function definitions, so it can live in the sandbox and
# needs no relative-source resolution.
FUNCTIONS_COPY="${TMP_DIR}/gc-cleanup-fns.sh"

cleanup() {
  rm -rf "${TMP_DIR}"
  rm -f "${REAL_TMP_SENTINEL:-}"
  if [[ -n "${REAL_STATE_SENTINEL:-}" ]]; then
    rm -f "${REAL_STATE_SENTINEL}"
  fi
}
trap cleanup EXIT

fail() {
  echo "install-lifecycle: $*" >&2
  exit 1
}

# ---------------------------------------------------------------------------
# Real-state sentinels: seeded BEFORE anything runs, asserted untouched AFTER.
# The /tmp sentinel uses a name matching a Workcell `--gc`/uninstall reap
# pattern and is owned by us, so it would be deleted if either reaper ran
# against the real host — its survival proves neither did.
# ---------------------------------------------------------------------------
REAL_TMP_SENTINEL="/tmp/workcell-docker.g3-install-lifecycle-real-sentinel.$$"
printf 'do not touch\n' >"${REAL_TMP_SENTINEL}"
REAL_STATE_ROOT="${XDG_STATE_HOME:-${HOME:-}/.local/state}/workcell"
REAL_STATE_SENTINEL=""
if [[ -n "${HOME:-}" && -d "${REAL_STATE_ROOT}" ]]; then
  REAL_STATE_SENTINEL="${REAL_STATE_ROOT}/g3-install-lifecycle-real-sentinel.$$"
  printf 'do not touch\n' >"${REAL_STATE_SENTINEL}"
fi

SANDBOX_HOME="${TMP_DIR}/home"
mkdir -p "${SANDBOX_HOME}"

LAUNCHER_LINK="${SANDBOX_HOME}/.local/bin/workcell"
MAN_LINK="${SANDBOX_HOME}/.local/share/man/man1/workcell.1"

# Build a minimal install tree that scripts/install.sh can link: it copies the
# real installer scripts but ships a stub launcher/man page that identifies its
# version, so an upgrade or rollback is observable as the installed launcher
# switching which tree it resolves to.
build_install_tree() {
  local tree="$1"
  local token="$2"

  mkdir -p "${tree}/scripts" "${tree}/man"
  cp "${ROOT_DIR}/scripts/install.sh" "${tree}/scripts/install.sh"
  cp "${ROOT_DIR}/scripts/install-workcell.sh" "${tree}/scripts/install-workcell.sh"
  chmod 0755 "${tree}/scripts/install.sh" "${tree}/scripts/install-workcell.sh"
  cat >"${tree}/scripts/workcell" <<EOF
#!/bin/bash
echo "workcell fixture ${token}"
EOF
  chmod 0755 "${tree}/scripts/workcell"
  printf '.TH WORKCELL 1 "fixture %s"\n' "${token}" >"${tree}/man/workcell.1"
}

# install.sh only writes under \$HOME (~/.local/bin, ~/.local/share/man) and, with
# --no-install-deps, runs no Homebrew; its re-exec re-passes HOME, so the sandbox
# HOME fully contains it. It performs no /tmp or real-home cache reaping.
install_from_tree() {
  local tree="$1"

  HOME="${SANDBOX_HOME}" \
    "${tree}/scripts/install.sh" --no-install-deps >/dev/null
}

launcher_target_tree() {
  local target=""
  target="$(readlink "${LAUNCHER_LINK}")" || fail "installed launcher is not a symlink"
  # strip the trailing /scripts/workcell to recover the tree root
  printf '%s\n' "${target%/scripts/workcell}"
}

# ---------------------------------------------------------------------------
# Install -> upgrade-in-place -> rollback mechanics (sandbox HOME only).
# ---------------------------------------------------------------------------
TREE_A="${TMP_DIR}/release-a"
TREE_B="${TMP_DIR}/release-b"
build_install_tree "${TREE_A}" "A"
build_install_tree "${TREE_B}" "B"

install_from_tree "${TREE_A}"
[[ -L "${LAUNCHER_LINK}" ]] || fail "install did not create the launcher symlink"
[[ -L "${MAN_LINK}" ]] || fail "install did not create the man symlink"
[[ "$(launcher_target_tree)" == "${TREE_A}" ]] || fail "launcher not pointed at the installed tree A"
grep -q 'workcell fixture A' <<<"$("${LAUNCHER_LINK}" --help)" || fail "installed launcher A did not run"

# Upgrade in place: re-running the installer from a second tree repoints the
# single launcher entry with no leftover duplicate, no orphaned old link.
install_from_tree "${TREE_B}"
[[ "$(launcher_target_tree)" == "${TREE_B}" ]] || fail "upgrade did not repoint launcher to tree B"
help_b="$("${LAUNCHER_LINK}" --help)"
grep -q 'workcell fixture B' <<<"${help_b}" || fail "upgraded launcher B did not run"
if grep -q 'workcell fixture A' <<<"${help_b}"; then
  fail "upgrade left the old launcher A resolvable"
fi
bin_entries="$(find "$(dirname "${LAUNCHER_LINK}")" -maxdepth 1 -name workcell | wc -l | tr -d ' ')"
[[ "${bin_entries}" == "1" ]] || fail "upgrade left ${bin_entries} launcher entries, want exactly 1"

# Rollback: re-installing the prior tree repoints back, proving the operation is
# symmetric and leaves no state pinning it forward.
install_from_tree "${TREE_A}"
[[ "$(launcher_target_tree)" == "${TREE_A}" ]] || fail "rollback did not repoint launcher to tree A"
grep -q 'workcell fixture A' <<<"$("${LAUNCHER_LINK}" --help)" || fail "rolled-back launcher A did not run"

# ---------------------------------------------------------------------------
# `workcell --gc` cleanup CONTRACT at the function level with an INJECTED root.
# We extract ONLY cleanup_workcell_temp_root and the self-contained helpers it
# calls, and source just those — NOT the whole launcher. Sourcing the full
# launcher would run its top-level init, which resolves `go` from fixed trusted
# paths (scripts/lib/launcher/go-hostutil.sh) and aborts with "Missing trusted
# host tool: go" on runners where Go is on PATH but outside those fixed paths
# (hosted toolcache, mise). The extracted snippet has no such dependency, so it
# runs the real reap logic against a sandbox directory (NOT the hard-coded /tmp,
# NOT real home) while being provably incapable of touching real host state. It
# must reap Workcell-owned scratch matching a cleanup pattern and preserve
# unrelated files. The live end-to-end `--gc` is local-operator certification
# (docs/install-lifecycle.md).
# ---------------------------------------------------------------------------
{
  sed -n '/^workcell_nonnegative_integer_or_default() {/,/^}/p' "${ROOT_DIR}/scripts/workcell"
  sed -n '/^workcell_path_mtime_epoch() {/,/^}/p' "${ROOT_DIR}/scripts/workcell"
  sed -n '/^workcell_path_is_stale_minutes() {/,/^}/p' "${ROOT_DIR}/scripts/workcell"
  sed -n '/^make_workcell_tree_user_writable() {/,/^}/p' "${ROOT_DIR}/scripts/workcell"
  sed -n '/^cleanup_workcell_temp_root() {/,/^}/p' "${ROOT_DIR}/scripts/workcell"
} >"${FUNCTIONS_COPY}"
grep -q '^cleanup_workcell_temp_root() {' "${FUNCTIONS_COPY}" ||
  fail "could not extract cleanup_workcell_temp_root from the launcher (renamed?)"

GC_FN_ROOT="${TMP_DIR}/gc-fn-root"
mkdir -p "${GC_FN_ROOT}/workcell-docker.stale-fixture"
printf 'keep me\n' >"${GC_FN_ROOT}/unrelated-keep.txt"

gc_fn_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    # stale threshold 0 => any owned, pattern-matching entry is eligible.
    cleanup_workcell_temp_root "$2" 0
    echo "gc-cleanup-done"
  ' _ "${FUNCTIONS_COPY}" "${GC_FN_ROOT}"
)"
grep -q '^gc-cleanup-done$' <<<"${gc_fn_output}" || fail "--gc cleanup contract did not run"
[[ ! -e "${GC_FN_ROOT}/workcell-docker.stale-fixture" ]] || fail "gc cleanup did not reap injected Workcell-owned stale scratch"
[[ -f "${GC_FN_ROOT}/unrelated-keep.txt" ]] || fail "gc cleanup reaped an unrelated (non-Workcell) file"

# ---------------------------------------------------------------------------
# Real-state containment proof: nothing above touched the real host.
# ---------------------------------------------------------------------------
[[ -e "${REAL_TMP_SENTINEL}" ]] || fail "scenario deleted a real /tmp Workcell sentinel outside the sandbox"
if [[ -n "${REAL_STATE_SENTINEL}" ]]; then
  [[ -f "${REAL_STATE_SENTINEL}" ]] || fail "scenario deleted a real ~/.local/state/workcell sentinel outside the sandbox"
fi

echo "Scenario passed"
