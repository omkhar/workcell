#!/usr/bin/env -S BASH_ENV= ENV= bash
# shellcheck shell=bash

default_go_cache_root() {
  if [[ -n "${WORKCELL_GO_CACHE_ROOT:-}" ]]; then
    printf '%s\n' "${WORKCELL_GO_CACHE_ROOT}"
    return 0
  fi
  if [[ -n "${HOME:-}" ]]; then
    case "$(uname -s 2>/dev/null || true)" in
      Darwin)
        printf '%s\n' "${HOME}/Library/Caches/workcell/go"
        return 0
        ;;
    esac
  fi
  if [[ -n "${XDG_CACHE_HOME:-}" ]]; then
    printf '%s\n' "${XDG_CACHE_HOME}/workcell/go"
    return 0
  fi
  if [[ -n "${HOME:-}" ]]; then
    printf '%s\n' "${HOME}/.cache/workcell/go"
    return 0
  fi
  local user_suffix="unknown"
  user_suffix="$(id -u 2>/dev/null || printf '%s' "unknown")"
  printf '%s\n' "${TMPDIR:-/tmp}/workcell-go-${user_suffix}"
}

ensure_go_run_env() {
  local cache_root
  cache_root="$(default_go_cache_root)"
  local gopath="${GOPATH:-${cache_root}/gopath}"
  local gomodcache="${GOMODCACHE:-${cache_root}/mod-cache}"
  local gocache="${GOCACHE:-${cache_root}/build-cache}"

  mkdir -p "${cache_root}"
  mkdir -p "${gopath}" "${gomodcache}" "${gocache}"
  chmod 0700 "${cache_root}" 2>/dev/null || true
  export GOPATH="${gopath}"
  export GOMODCACHE="${gomodcache}"
  export GOCACHE="${gocache}"
  export WORKCELL_GO_CACHE_ROOT="${cache_root}"
}

# The repository that holds this library; its content never supplies go.
GO_RUN_ENV_REPO_ROOT="$(cd "${BASH_SOURCE[0]%/*}/../.." && pwd -P)"

# go_bin_path_trusted mirrors publish-pr's IsTrustedHostToolPath for go: the
# path is absolute, outside this repository and the caller's repo root, and,
# unless allow_any_prefix is 1, under a host-tool or Go-install prefix. The
# host-tool prefixes are the scripts/workcell is_trusted_host_tool_path list
# (internal/publishpr trustedHostToolPrefixes in Go) that hold go; the Go
# prefixes add the official, distro, and hosted-runner install roots.
go_bin_path_trusted() {
  local candidate="$1"
  local repo_root="$2"
  local allow_any_prefix="$3"
  local root=""

  [[ "${candidate}" == /* ]] || return 1
  for root in "${GO_RUN_ENV_REPO_ROOT}" "${repo_root}"; do
    [[ -n "${root}" ]] || continue
    case "${candidate}" in
      "${root}" | "${root}"/*) return 1 ;;
    esac
  done
  [[ "${allow_any_prefix}" == "1" ]] && return 0
  case "${candidate}" in
    /usr/bin/* | /bin/* | /usr/local/bin/* | /usr/local/go/* | \
      /usr/lib/go/* | /usr/lib/golang/* | /usr/lib/go-[0-9]*/* | \
      /opt/homebrew/bin/* | /opt/homebrew/Cellar/* | /usr/local/Cellar/* | \
      /home/linuxbrew/.linuxbrew/bin/* | /home/linuxbrew/.linuxbrew/Cellar/* | \
      /opt/hostedtoolcache/go/* | /Users/runner/hostedtoolcache/go/*)
      return 0
      ;;
  esac
  return 1
}

# go_bin_storage_private succeeds when no other user can replace the go at
# canonical path PATH: the file and each parent belong to root or this user,
# and none is group- or other-writable unless it is a root-owned sticky
# directory. find runs from a fixed system path; a failed find is a refusal.
#
# Language-boundary justification: the callers run go by its canonical path,
# so a swap between this check and the exec still runs the new file. Bash has
# no fexecve, and exec through /dev/fd breaks go, which finds GOROOT from its
# own path. Threat model: WORKCELL_GO_BIN may point outside the trusted
# prefixes, into a home or temporary directory. This check refuses storage
# that another user can write, so only root or this user can swap the go.
# Residual risk: a process of this user can still swap it; that process can
# already change this repository and the caller's environment.
go_bin_storage_private() {
  local path="$1"
  local find_bin=""
  local shared=""

  for find_bin in /usr/bin/find /bin/find; do
    [[ -x "${find_bin}" ]] && break
  done
  [[ -x "${find_bin}" ]] || return 1
  while :; do
    shared="$("${find_bin}" "${path}" -prune \( \( ! -user 0 ! -user "${EUID}" \) -o \
      \( \( -perm -0020 -o -perm -0002 \) ! \( -type d -user 0 -perm -1000 \) \) \) -print)" || return 1
    [[ -z "${shared}" ]] || return 1
    [[ "${path}" != / ]] || return 0
    path="${path%/*}"
    path="${path:-/}"
  done
}

# accept_go_bin prints the canonical go when both the raw and the canonical
# path pass go_bin_path_trusted. realpath runs from a fixed system path, and a
# failed canonicalization is a refusal. A go outside the trusted prefixes
# (allow_any_prefix 1) must also pass go_bin_storage_private.
accept_go_bin() {
  local candidate="$1"
  local repo_root="$2"
  local allow_any_prefix="$3"
  local realpath_bin=""
  local canonical=""
  local root_canonical=""

  [[ -n "${candidate}" && -f "${candidate}" && -x "${candidate}" ]] || return 1
  for realpath_bin in /usr/bin/realpath /bin/realpath; do
    [[ -x "${realpath_bin}" ]] && break
  done
  [[ -x "${realpath_bin}" ]] || return 1
  canonical="$("${realpath_bin}" "${candidate}" 2>/dev/null)" || return 1
  if [[ -n "${repo_root}" ]]; then
    root_canonical="$("${realpath_bin}" "${repo_root}" 2>/dev/null)" || return 1
  fi
  go_bin_path_trusted "${candidate}" "${repo_root}" "${allow_any_prefix}" &&
    go_bin_path_trusted "${canonical}" "${root_canonical}" "${allow_any_prefix}" ||
    return 1
  if [[ "${allow_any_prefix}" == "1" ]]; then
    go_bin_storage_private "${canonical}" || return 1
  fi
  printf '%s\n' "${canonical}"
}

# resolve_go_bin [REPO_ROOT] resolves go the way publish-pr resolves its host
# tools, so a workspace-planted go never runs with the caller's environment.
# WORKCELL_GO_BIN is the operator's explicit choice: it skips the prefix list
# (toolchain managers and test wrappers live elsewhere) but never the
# repository exclusion. Otherwise the first go on PATH, then the fixed
# candidates, must sit under a trusted prefix.
resolve_go_bin() {
  local repo_root="${1:-}"
  local candidate=""

  if [[ -n "${WORKCELL_GO_BIN:-}" && -x "${WORKCELL_GO_BIN}" ]]; then
    if ! accept_go_bin "${WORKCELL_GO_BIN}" "${repo_root}" 1; then
      echo "WORKCELL_GO_BIN must be an absolute go outside the repository that only root or this user can replace: ${WORKCELL_GO_BIN}" >&2
      exit 1
    fi
    return 0
  fi

  for candidate in \
    "$(command -v go 2>/dev/null || true)" \
    /opt/homebrew/bin/go \
    /usr/local/go/bin/go \
    /usr/local/bin/go \
    /usr/bin/go; do
    accept_go_bin "${candidate}" "${repo_root}" 0 && return 0
  done
  echo "Missing trusted host tool: go (set WORKCELL_GO_BIN to an absolute go outside the repository)" >&2
  exit 1
}

run_go_in_repo() {
  local repo_root="$1"
  shift

  ensure_go_run_env
  local go_bin
  go_bin="$(resolve_go_bin "${repo_root}")"
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
  go_bin="$(resolve_go_bin "${repo_root}")"
  cd "${repo_root}" || exit 1
  exec "${go_bin}" run "$@"
}

build_go_tool_in_repo() {
  local repo_root="$1"
  local output_path="$2"
  shift 2

  ensure_go_run_env
  local go_bin
  go_bin="$(resolve_go_bin "${repo_root}")"
  (
    cd "${repo_root}" &&
      "${go_bin}" build -buildvcs=false -o "${output_path}" "$@"
  )
}
