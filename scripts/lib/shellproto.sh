#!/usr/bin/env -S BASH_ENV= ENV= bash
# shellcheck shell=bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 Omkhar Arasaratnam
#
# scripts/lib/shellproto.sh — bash-side parser for the KEY=VALUE plan
# format that internal/shellproto.WriteField emits on the Go side.  The
# format is the lowest-common-denominator contract every translated
# session_*_main, publish-pr dry-run header, and injection-bundle
# result shares; centralising the parse loop here means each shim drops
# its hand-rolled `while IFS= read -r line; case ${line%%=*} in ...` and
# a regression in the parsing semantics (e.g. mis-handling values that
# contain '=', or losing the empty-line skip) cannot recur in only one
# shim.
#
# shellproto_field <plan> <key> [default]
#
#   Returns the value of the named key from the multi-line plan in $1.
#   Lines that lack '=' are skipped silently as a forward-compat hook
#   for future emitters that prepend header rows.  Empty values are
#   accepted: `key=\n` is returned as the empty string.  If the key is
#   not present, the optional default in $3 is returned (empty string
#   if no default is supplied).  Only the FIRST occurrence of the key
#   is returned - the Go side's WriteFields emits at most one record
#   per key, so this matches the contract.
#
# Bash-3.2 compatible: scripts/workcell's shebang pins /bin/bash, which
# is bash 3.2 on macOS, so this helper cannot use associative arrays or
# namerefs.  A scalar-returning function with a manual loop is the
# smallest tool that does the job and stays portable.
#
# Source from scripts/workcell after go-run-env.sh:
#
#   source "${ROOT_DIR}/scripts/lib/shellproto.sh"
#
# Typical use in a session_*_main shim:
#
#   session_id="$(shellproto_field "${plan}" session_id)"
#   no_stdin="$(shellproto_field "${plan}" no_stdin 0)"

shellproto_field() {
  local plan="$1"
  local target_key="$2"
  local default_value="${3:-}"
  local line key value
  while IFS= read -r line; do
    if [[ "${line}" != *=* ]]; then
      continue
    fi
    key="${line%%=*}"
    value="${line#*=}"
    if [[ "${key}" == "${target_key}" ]]; then
      printf '%s' "${value}"
      return 0
    fi
  done <<<"${plan}"
  printf '%s' "${default_value}"
}

# shellproto_assign_globals <plan> <key1:var1> <key2:var2> ...
#
#   Walks the multi-line plan in $1 once and, for each KEY=VALUE line
#   whose KEY matches one of the supplied <key>:<var> pairs, assigns
#   VALUE to the named shell variable.  This replaces the
#   `while IFS=...; case ${key} in ... esac; done <<<"${plan}"` shape
#   that the larger bash multi-key parsers in scripts/workcell used to
#   carry by hand; centralising the loop here avoids per-call drift
#   (e.g. one site forgetting the *=* skip, another mis-handling values
#   that contain '=').
#
#   Empty values are accepted: `key=\n` clears the named variable.
#   Lines without '=' are skipped silently (forward-compat with
#   future header rows).  Only the first occurrence of a key wins,
#   matching the Go side's WriteFields contract.
#
#   Variables that never appear in the plan are left at their existing
#   value; the caller is expected to pre-clear them when a clean slate
#   is required.
#
# Typical use:
#
#   shellproto_assign_globals "${plan}" \
#     session_id:SESSION_META_ID \
#     profile:SESSION_META_PROFILE
shellproto_assign_globals() {
  local plan="$1"
  shift
  local pair key var line lk lv i
  local -a pair_keys=()
  local -a pair_vars=()
  local -a pair_seen=()
  for pair in "$@"; do
    pair_keys+=("${pair%%:*}")
    pair_vars+=("${pair#*:}")
    pair_seen+=("0")
  done
  while IFS= read -r line; do
    if [[ "${line}" != *=* ]]; then
      continue
    fi
    lk="${line%%=*}"
    lv="${line#*=}"
    i=0
    while [[ "${i}" -lt "${#pair_keys[@]}" ]]; do
      key="${pair_keys[i]}"
      var="${pair_vars[i]}"
      if [[ "${pair_seen[i]}" == "0" && "${lk}" == "${key}" ]]; then
        printf -v "${var}" '%s' "${lv}"
        pair_seen[i]="1"
        break
      fi
      i=$((i + 1))
    done
  done <<<"${plan}"
}
