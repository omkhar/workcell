#!/usr/bin/env -S BASH_ENV= ENV= bash
# shellcheck shell=bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 Omkhar Arasaratnam
#
# scripts/lib/launcher/host-exec.sh — trusted host-command-execution
# module extracted from scripts/workcell as the next increment of the
# launcher decomposition (roadmap item D4).  These helpers resolve a
# fixed, trusted host tool from an allowlist of absolute paths and run
# host commands under a sanitised environment (env -i with a pinned
# PATH/HOME and C locale), optionally from a caller-supplied working
# directory.  They depend only on env/cd builtins, the readonly
# TRUSTED_HOST_PATH, REAL_HOME (used with a fallback), and
# resolve_workcell_real_home (provided by scripts/lib/trusted-docker-client.sh)
# — no other launcher function — so they are a self-contained,
# behaviour-preserving unit.  See docs/launcher-contract.md for the
# module contract.

resolve_fixed_host_tool() {
  local name="$1"
  shift
  local candidate

  for candidate in "$@"; do
    if [[ -x "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done

  echo "Missing trusted host tool: ${name}" >&2
  exit 1
}

run_clean_host_command() {
  local home="${REAL_HOME:-}"

  [[ "$#" -gt 0 ]] || return 0
  if [[ -z "${home}" ]]; then
    home="$(resolve_workcell_real_home 2>/dev/null || true)"
  fi
  if [[ ! -d "${home}" ]]; then
    home="/"
  fi

  (
    cd "${home}" &&
      env -i \
        PATH="${TRUSTED_HOST_PATH}" \
        HOME="${home}" \
        LC_ALL=C \
        LANG=C \
        "$@"
  )
}

run_clean_host_command_in_dir() {
  local dir="$1"
  local home="${REAL_HOME:-}"

  shift
  [[ "$#" -gt 0 ]] || return 0
  if [[ ! -d "${dir}" ]]; then
    echo "Missing host working directory: ${dir}" >&2
    exit 2
  fi
  if [[ -z "${home}" ]]; then
    home="$(resolve_workcell_real_home 2>/dev/null || true)"
  fi
  if [[ ! -d "${home}" ]]; then
    home="/"
  fi

  (
    cd "${dir}" &&
      env -i \
        PATH="${TRUSTED_HOST_PATH}" \
        HOME="${home}" \
        LC_ALL=C \
        LANG=C \
        "$@"
  )
}
