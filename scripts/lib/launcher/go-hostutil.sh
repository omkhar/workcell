#!/usr/bin/env -S BASH_ENV= ENV= bash
# shellcheck shell=bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 Omkhar Arasaratnam
#
# scripts/lib/launcher/go-hostutil.sh — Go/Colima host-utility wrapper
# module extracted from scripts/workcell as the next increment of the
# launcher decomposition (roadmap item D4).  These helpers invoke the
# workcell-hostutil and workcell-colimautil Go programs on the host from a
# cached build (go_tool_bin, below), always routed through
# run_clean_host_command_in_dir so the
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

# Per-process memo of the cached hostutil path.  Assigned here so an
# inherited environment value can never pre-seed it.
GO_TOOL_BIN=""
GO_HOSTUTIL_BIN=""

# go_tool_bin_trusted accepts only a regular, non-empty, non-symlink,
# executable file owned by the current user and not writable by group or
# other.  An empty file (a rename that a crash left without data) would
# otherwise run as an empty shell script and exit 0.
go_tool_bin_trusted() {
  [[ -f "$1" && -s "$1" && ! -L "$1" && -O "$1" && -x "$1" ]] &&
    go_tool_path_private "$1"
}

# go_tool_path_private succeeds when $1 is not writable by group or other.  A
# failed find is not a pass.
go_tool_path_private() {
  local writable
  writable="$(find "$1" -maxdepth 0 \( -perm -020 -o -perm -002 \) -print)" || return 1
  [[ -z "${writable}" ]]
}

# go_tool_build_id prints the Go build ID of ./cmd/TOOL.
go_tool_build_id() {
  run_clean_host_command_in_dir "${ROOT_DIR}" env \
    GOPATH="${GOPATH}" \
    GOMODCACHE="${GOMODCACHE}" \
    GOCACHE="${GOCACHE}" \
    "${HOST_GO_BIN}" list -buildvcs=false -export -f '{{.BuildID}}' "./cmd/$1"
}

# go_tool_bin sets GO_TOOL_BIN to a cached build of ./cmd/TOOL and builds it
# on a miss.  The cache key is the main package's Go build ID.  Go derives it
# from the content of every source file in the package's dependency graph
# (in-module sources and the go.mod-selected module versions), the build
# flags, and the toolchain version, so any change to those inputs selects a
# new binary.  The cache directory is owner-only (0700); a cached binary that
# fails go_tool_bin_trusted is removed and rebuilt; a build lands in a temp
# file and is renamed into place so a reader never sees a partial binary.
# ponytail: superseded binaries are not pruned (like GOCACHE entries); add an
# age-based prune if the disk use matters.
go_tool_bin() {
  local tool="$1"
  local bin_dir="${WORKCELL_GO_CACHE_ROOT}/bin"
  local build_id="" rebuilt_id="" bin="" tmp=""

  GO_TOOL_BIN=""
  [[ "${tool}" != workcell-hostutil ]] || GO_TOOL_BIN="${GO_HOSTUTIL_BIN}"
  if [[ -n "${GO_TOOL_BIN}" ]] && go_tool_bin_trusted "${GO_TOOL_BIN}"; then
    return 0
  fi

  build_id="$(go_tool_build_id "${tool}")" || return 1
  if [[ ! "${build_id}" =~ ^[A-Za-z0-9_/-]+$ ]]; then
    echo "Unexpected Go build ID for ${tool}: ${build_id}" >&2
    return 1
  fi
  bin="${bin_dir}/${tool}-${build_id//\//.}"

  mkdir -p "${bin_dir}" || return 1
  if [[ -L "${WORKCELL_GO_CACHE_ROOT}" || ! -O "${WORKCELL_GO_CACHE_ROOT}" ||
    -L "${bin_dir}" || ! -d "${bin_dir}" || ! -O "${bin_dir}" ]] ||
    ! go_tool_path_private "${WORKCELL_GO_CACHE_ROOT}"; then
    echo "Refusing untrusted Go tool cache: ${bin_dir}" >&2
    return 1
  fi
  chmod 0700 "${bin_dir}" || return 1

  if [[ -e "${bin}" || -L "${bin}" ]] && ! go_tool_bin_trusted "${bin}"; then
    rm -f "${bin}"
  fi
  if [[ ! -e "${bin}" && ! -L "${bin}" ]]; then
    tmp="$(mktemp "${bin_dir}/.${tool}.XXXXXX")" || return 1
    if ! run_clean_host_command_in_dir "${ROOT_DIR}" env \
      GOPATH="${GOPATH}" \
      GOMODCACHE="${GOMODCACHE}" \
      GOCACHE="${GOCACHE}" \
      "${HOST_GO_BIN}" build -buildvcs=false -o "${tmp}" "./cmd/${tool}"; then
      rm -f "${tmp}"
      return 1
    fi
    # A source change between the key lookup and the build would store the
    # new binary under the old key; refuse it.
    if ! rebuilt_id="$(go_tool_build_id "${tool}")" || [[ "${rebuilt_id}" != "${build_id}" ]]; then
      rm -f "${tmp}"
      echo "Go sources for ${tool} changed during the build; retry" >&2
      return 1
    fi
    if ! chmod 0700 "${tmp}"; then
      rm -f "${tmp}"
      return 1
    fi
    # Flush the binary data before the rename and the new entry after it, so a
    # crash cannot leave a non-empty partial binary under the final name.
    if ! sync; then
      rm -f "${tmp}"
      return 1
    fi
    mv -f "${tmp}" "${bin}" || return 1
    sync || return 1
  fi
  if ! go_tool_bin_trusted "${bin}"; then
    echo "Refusing untrusted cached Go tool: ${bin}" >&2
    return 1
  fi
  [[ "${tool}" != workcell-hostutil ]] || GO_HOSTUTIL_BIN="${bin}"
  GO_TOOL_BIN="${bin}"
}

go_hostutil() {
  ensure_go_run_env
  go_tool_bin workcell-hostutil || return 1
  run_clean_host_command_in_dir "${ROOT_DIR}" env \
    GOPATH="${GOPATH}" \
    GOMODCACHE="${GOMODCACHE}" \
    GOCACHE="${GOCACHE}" \
    "${GO_TOOL_BIN}" "$@"
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

  go_tool_bin workcell-hostutil || return 1
  run_clean_host_command_in_dir "${ROOT_DIR}" env \
    "${env_args[@]}" \
    "${GO_TOOL_BIN}" "$@"
}

go_colimautil() {
  ensure_go_run_env
  go_tool_bin workcell-colimautil || return 1
  run_clean_host_command_in_dir "${ROOT_DIR}" env \
    GOPATH="${GOPATH}" \
    GOMODCACHE="${GOMODCACHE}" \
    GOCACHE="${GOCACHE}" \
    "${GO_TOOL_BIN}" "$@"
}

# Resolve the hostutil binary once in the sourcing shell.  Most go_hostutil
# calls run in command substitutions, and a subshell cannot update the memo
# for later calls.  The first go_hostutil call reports a failure here again.
ensure_go_run_env
go_tool_bin workcell-hostutil 2>/dev/null || true
