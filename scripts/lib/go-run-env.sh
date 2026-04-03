#!/usr/bin/env -S BASH_ENV= ENV= bash
# shellcheck shell=bash

ensure_go_run_env() {
  local cache_root="${WORKCELL_GO_CACHE_ROOT:-${TMPDIR:-/tmp}/workcell-go}"
  local gopath="${GOPATH:-${cache_root}/gopath}"
  local gomodcache="${GOMODCACHE:-${cache_root}/mod-cache}"
  local gocache="${GOCACHE:-${cache_root}/build-cache}"

  mkdir -p "${gopath}" "${gomodcache}" "${gocache}"
  export GOPATH="${gopath}"
  export GOMODCACHE="${gomodcache}"
  export GOCACHE="${gocache}"
}

resolve_go_bin() {
  if [[ -n "${WORKCELL_GO_BIN:-}" && -x "${WORKCELL_GO_BIN}" ]]; then
    printf '%s\n' "${WORKCELL_GO_BIN}"
    return 0
  fi

  WORKCELL_GO_BIN="$(command -v go 2>/dev/null || true)"
  if [[ -z "${WORKCELL_GO_BIN}" || ! -x "${WORKCELL_GO_BIN}" ]]; then
    local candidate
    for candidate in \
      /opt/homebrew/bin/go \
      /usr/local/go/bin/go \
      /usr/local/bin/go \
      /usr/bin/go; do
      if [[ -x "${candidate}" ]]; then
        WORKCELL_GO_BIN="${candidate}"
        break
      fi
    done
  fi
  if [[ -z "${WORKCELL_GO_BIN}" || ! -x "${WORKCELL_GO_BIN}" ]]; then
    echo "Missing required tool: go" >&2
    exit 1
  fi

  printf '%s\n' "${WORKCELL_GO_BIN}"
}

run_go_in_repo() {
  local repo_root="$1"
  shift

  ensure_go_run_env
  local go_bin
  go_bin="$(resolve_go_bin)"
  (
    cd "${repo_root}" &&
      "${go_bin}" "$@"
  )
}

exec_go_run_in_repo() {
  local repo_root="$1"
  shift

  ensure_go_run_env
  local go_bin
  go_bin="$(resolve_go_bin)"
  cd "${repo_root}" || exit 1
  exec "${go_bin}" run "$@"
}

build_go_tool_in_repo() {
  local repo_root="$1"
  local output_path="$2"
  shift 2

  ensure_go_run_env
  local go_bin
  go_bin="$(resolve_go_bin)"
  (
    cd "${repo_root}" &&
      "${go_bin}" build -o "${output_path}" "$@"
  )
}
