#!/usr/bin/env -S BASH_ENV= ENV= bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 Omkhar Arasaratnam
#
# tests/scenarios/shared/test-shellproto-lib.sh — targeted unit tests
# for scripts/lib/shellproto.sh helpers (shellproto_field and
# shellproto_assign_globals).  The bash KEY=VALUE parser used by every
# session_*_main shim and the support-matrix/git-metadata bulk loaders
# now routes through these helpers, so a regression here would cascade
# into every shim.
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=tests/scenarios/shared/test-shellproto-lib.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}

set -euo pipefail

ROOT_DIR="${workcell_entry_root}"

# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/shellproto.sh"

PASS_COUNT=0

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_eq() {
  local description="$1"
  local got="$2"
  local want="$3"
  if [[ "${got}" != "${want}" ]]; then
    fail "${description}: got=[${got}] want=[${want}]"
  fi
  PASS_COUNT=$((PASS_COUNT + 1))
}

# ---- shellproto_field cases -----------------------------------------------

# Key with '=' in the value: the value side of the first '=' is kept verbatim.
plan_eq_in_value="$(printf 'token=abc=def=ghi\nstatus=ok\n')"
assert_eq "shellproto_field keeps '=' in value" \
  "$(shellproto_field "${plan_eq_in_value}" token)" \
  "abc=def=ghi"

# Missing key returns the default.
plan_missing="$(printf 'a=1\nb=2\n')"
assert_eq "shellproto_field returns default for missing key" \
  "$(shellproto_field "${plan_missing}" c default-val)" \
  "default-val"
assert_eq "shellproto_field returns empty for missing key with no default" \
  "$(shellproto_field "${plan_missing}" c)" \
  ""

# First occurrence wins.
plan_duplicate="$(printf 'k=first\nk=second\nk=third\n')"
assert_eq "shellproto_field returns first occurrence" \
  "$(shellproto_field "${plan_duplicate}" k)" \
  "first"

# Empty plan returns default.
assert_eq "shellproto_field empty plan returns default" \
  "$(shellproto_field "" any fallback)" \
  "fallback"

# Lines without '=' are skipped (forward-compat hook).
plan_with_garbage="$(printf '# header\nrandom-noise\nkey=value\n')"
assert_eq "shellproto_field skips non-KV lines" \
  "$(shellproto_field "${plan_with_garbage}" key)" \
  "value"

# Empty value is accepted.
plan_empty_value="$(printf 'present=\nother=visible\n')"
assert_eq "shellproto_field accepts empty value" \
  "$(shellproto_field "${plan_empty_value}" present default-not-used)" \
  ""

# ---- shellproto_assign_globals cases --------------------------------------

# Basic assignment.
G_ONE="" G_TWO="" G_THREE=""
plan_basic="$(printf 'one=alpha\ntwo=beta\nthree=gamma\n')"
shellproto_assign_globals "${plan_basic}" \
  one:G_ONE \
  two:G_TWO \
  three:G_THREE
assert_eq "assign_globals one" "${G_ONE}" "alpha"
assert_eq "assign_globals two" "${G_TWO}" "beta"
assert_eq "assign_globals three" "${G_THREE}" "gamma"

# '=' in value survives.
G_TOKEN=""
plan_eq="$(printf 'token=k1=v1=v2\n')"
shellproto_assign_globals "${plan_eq}" token:G_TOKEN
assert_eq "assign_globals preserves '=' in value" "${G_TOKEN}" "k1=v1=v2"

# Missing key leaves variable unchanged at caller-supplied value.
G_KEEP="preset-value"
shellproto_assign_globals "$(printf 'other=val\n')" missing:G_KEEP
assert_eq "assign_globals leaves missing var unchanged" "${G_KEEP}" "preset-value"

# First occurrence wins.
G_FIRST=""
plan_dup="$(printf 'k=first\nk=second\n')"
shellproto_assign_globals "${plan_dup}" k:G_FIRST
assert_eq "assign_globals first occurrence wins" "${G_FIRST}" "first"

# Empty plan leaves variables untouched.
G_UNTOUCHED="initial"
shellproto_assign_globals "" some:G_UNTOUCHED
assert_eq "assign_globals empty plan leaves var unchanged" "${G_UNTOUCHED}" "initial"

# Partial match: only the keys present are reassigned; unmatched lines noop.
G_A="" G_B=""
plan_partial="$(printf 'a=A\nstray=Z\nb=B\n')"
shellproto_assign_globals "${plan_partial}" a:G_A b:G_B
assert_eq "assign_globals partial match a" "${G_A}" "A"
assert_eq "assign_globals partial match b" "${G_B}" "B"

# Empty value clears the named variable.
G_CLEAR="prior"
shellproto_assign_globals "$(printf 'flag=\n')" flag:G_CLEAR
assert_eq "assign_globals empty value clears var" "${G_CLEAR}" ""

printf 'OK: shellproto helpers passed %d assertions\n' "${PASS_COUNT}"
