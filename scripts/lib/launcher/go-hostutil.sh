#!/usr/bin/env -S BASH_ENV= ENV= bash
# shellcheck shell=bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 Omkhar Arasaratnam
#
# scripts/lib/launcher/go-hostutil.sh — Go/Colima host-utility wrapper
# module extracted from scripts/workcell as the next increment of the
# launcher decomposition (roadmap item D4).  These helpers invoke the
# workcell-hostutil and workcell-colimautil Go programs on the host via
# `go run`, always routed through run_clean_host_command_in_dir so the
# child executes from ${ROOT_DIR} under the sanitised host environment
# (env -i with a pinned PATH/HOME and C locale) provided by
# scripts/lib/launcher/host-exec.sh.  They depend only on
# ensure_go_run_env plus the GOPATH/GOMODCACHE/GOCACHE it exports
# (scripts/lib/go-run-env.sh), run_clean_host_command_in_dir
# (scripts/lib/launcher/host-exec.sh), and the readonly ROOT_DIR global
# set in scripts/workcell — all sourced or assigned before the first
# wrapper call — so they are a self-contained, behaviour-preserving unit.
# HOST_GO_BIN is resolved by this module (below) since it is the sole
# consumer; resolve_fixed_host_tool comes from the host-exec.sh module
# sourced immediately before this one.
# run_go_hostutil_preserve_exit additionally
# recovers the Go child's real exit code from its `exit status N` stderr
# trailer.  go_hostutil_publish_pr forwards an explicit allowlist of
# terminal/GnuPG/SSH/XDG/GitHub environment variables (read at call time)
# so host-side PR publication can reach the operator's credentials.  See
# docs/launcher-contract.md for the module contract.

HOST_GO_BIN="$(resolve_fixed_host_tool go /opt/homebrew/bin/go /usr/local/go/bin/go /usr/local/bin/go /usr/bin/go)"

go_hostutil() {
  ensure_go_run_env
  run_clean_host_command_in_dir "${ROOT_DIR}" env \
    GOPATH="${GOPATH}" \
    GOMODCACHE="${GOMODCACHE}" \
    GOCACHE="${GOCACHE}" \
    "${HOST_GO_BIN}" run ./cmd/workcell-hostutil "$@"
}

run_go_hostutil_preserve_exit() {
  local stderr_file=""
  local stderr_capture=""
  local rc=0

  stderr_file="$(mktemp "${TMPDIR:-/tmp}/workcell-hostutil-stderr.XXXXXX")"
  trap 'rm -f "${stderr_file}"' RETURN
  go_hostutil "$@" 2>"${stderr_file}" || rc=$?
  stderr_capture="$(cat "${stderr_file}")"
  rm -f "${stderr_file}"
  trap - RETURN

  if [[ "${stderr_capture}" =~ (^|$'\n')exit\ status\ ([0-9]+)$ ]]; then
    if [[ ${rc} -eq 1 ]]; then
      rc="${BASH_REMATCH[2]}"
    fi
    if [[ -z "${BASH_REMATCH[1]}" ]]; then
      stderr_capture=""
    else
      stderr_capture="${stderr_capture%$'\n'exit status [0-9]*}"
    fi
  fi
  if [[ -n "${stderr_capture}" ]]; then
    printf '%s\n' "${stderr_capture}" >&2
  fi
  return "${rc}"
}

go_hostutil_publish_pr() {
  ensure_go_run_env
  local -a env_args=(
    "GOPATH=${GOPATH}"
    "GOMODCACHE=${GOMODCACHE}"
    "GOCACHE=${GOCACHE}"
  )

  [[ -n "${TERM:-}" ]] && env_args+=("TERM=${TERM}")
  [[ -n "${GPG_TTY:-}" ]] && env_args+=("GPG_TTY=${GPG_TTY}")
  [[ -n "${GNUPGHOME:-}" ]] && env_args+=("GNUPGHOME=${GNUPGHOME}")
  [[ -n "${SSH_AUTH_SOCK:-}" ]] && env_args+=("SSH_AUTH_SOCK=${SSH_AUTH_SOCK}")
  [[ -n "${SSH_AGENT_PID:-}" ]] && env_args+=("SSH_AGENT_PID=${SSH_AGENT_PID}")
  [[ -n "${SSH_ASKPASS:-}" ]] && env_args+=("SSH_ASKPASS=${SSH_ASKPASS}")
  [[ -n "${GIT_ASKPASS:-}" ]] && env_args+=("GIT_ASKPASS=${GIT_ASKPASS}")
  [[ -n "${XDG_CONFIG_HOME:-}" ]] && env_args+=("XDG_CONFIG_HOME=${XDG_CONFIG_HOME}")
  [[ -n "${XDG_STATE_HOME:-}" ]] && env_args+=("XDG_STATE_HOME=${XDG_STATE_HOME}")
  [[ -n "${XDG_CACHE_HOME:-}" ]] && env_args+=("XDG_CACHE_HOME=${XDG_CACHE_HOME}")
  [[ -n "${XDG_DATA_HOME:-}" ]] && env_args+=("XDG_DATA_HOME=${XDG_DATA_HOME}")
  [[ -n "${XDG_RUNTIME_DIR:-}" ]] && env_args+=("XDG_RUNTIME_DIR=${XDG_RUNTIME_DIR}")
  [[ -n "${GH_TOKEN:-}" ]] && env_args+=("GH_TOKEN=${GH_TOKEN}")
  [[ -n "${GITHUB_TOKEN:-}" ]] && env_args+=("GITHUB_TOKEN=${GITHUB_TOKEN}")
  [[ -n "${GH_HOST:-}" ]] && env_args+=("GH_HOST=${GH_HOST}")
  [[ -n "${GH_CONFIG_DIR:-}" ]] && env_args+=("GH_CONFIG_DIR=${GH_CONFIG_DIR}")

  run_clean_host_command_in_dir "${ROOT_DIR}" env \
    "${env_args[@]}" \
    "${HOST_GO_BIN}" run ./cmd/workcell-hostutil "$@"
}

go_colimautil() {
  ensure_go_run_env
  run_clean_host_command_in_dir "${ROOT_DIR}" env \
    GOPATH="${GOPATH}" \
    GOMODCACHE="${GOMODCACHE}" \
    GOCACHE="${GOCACHE}" \
    "${HOST_GO_BIN}" run ./cmd/workcell-colimautil "$@"
}
