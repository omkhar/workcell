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
# defined in the sourcing shell (both live in scripts/workcell).

session_run_cli_with_roots() {
  local subcommand="$1"
  shift
  local -a lookup_roots=()
  while IFS= read -r line; do
    lookup_roots+=("${line}")
  done < <(session_lookup_root_args)
  set +e
  local plan
  plan="$(go_hostutil "${subcommand}" "${lookup_roots[@]}" "$@")"
  local status="$?"
  set -e
  printf '%s\n' "${plan}"
  return "${status}"
}
