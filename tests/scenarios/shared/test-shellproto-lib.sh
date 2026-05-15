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

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

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
