#!/usr/bin/env -S BASH_ENV= ENV= bash
# shellcheck shell=bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 Omkhar Arasaratnam
#
# Shared shim that fronts every session_*_main hostutil call.  The five
# session_send_main / session_stop_main / session_delete_main /
# session_monitor_main / session_attach_main bash functions used to each
# reimplement the same 8-line "read state-root flags, capture the Go
# binary's plan, propagate the exit status" preamble.  Centralising it
# here means a regression in the preamble (e.g. forgetting `set +e`
# around the capture, or losing the exit-2 propagation) cannot recur in
# only one shim.
#
# session_run_cli_with_roots <subcommand> [args...]
#
#   Runs go_hostutil's top-level `<subcommand>` after prepending the
#   --root=PATH state-root args supplied by session_lookup_root_args.
#   Emits the captured stdout (the Go side's KEY=VALUE plan) on the
#   shim's stdout, and returns the child's exit status so the calling
#   session_*_main can preserve the bash exit-code contract via
#   `exit "${status}"`.
#
# Source from scripts/workcell after go-run-env.sh:
#
#   source "${ROOT_DIR}/scripts/lib/sessionctl-shim.sh"
#
# Requires session_lookup_root_args and go_hostutil to already be
# defined before this helper is called.  When scripts/workcell provides
# run_go_hostutil_preserve_exit, the shim uses it so go-run child exit
# statuses remain visible to callers.

SESSION_LOOKUP_ROOTS=()

collect_session_lookup_roots() {
  local roots_file=""
  local root=""

  roots_file="$(mktemp "${TMPDIR:-/tmp}/workcell-session-roots.XXXXXX")"
  if ! session_lookup_root_args >"${roots_file}"; then
    rm -f "${roots_file}"
    return 1
  fi
  SESSION_LOOKUP_ROOTS=()
  while IFS= read -r root; do
    SESSION_LOOKUP_ROOTS+=("${root}")
  done <"${roots_file}"
  rm -f "${roots_file}"
}

session_run_cli_with_roots() {
  local subcommand="$1"
  shift

  collect_session_lookup_roots || return 1
  if declare -F run_go_hostutil_preserve_exit >/dev/null; then
    run_go_hostutil_preserve_exit "${subcommand}" "${SESSION_LOOKUP_ROOTS[@]}" "$@"
    return $?
  fi

  set +e
  local plan
  plan="$(go_hostutil "${subcommand}" "${SESSION_LOOKUP_ROOTS[@]}" "$@")"
  local status="$?"
  set -e
  printf '%s\n' "${plan}"
  return "${status}"
}
