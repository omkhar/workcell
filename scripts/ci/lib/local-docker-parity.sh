#!/usr/bin/env -S BASH_ENV= ENV= bash
# shellcheck shell=bash
source "${ROOT_DIR}/scripts/lib/go-run-env.sh"

setup_workcell_ci_docker() {
  setup_workcell_trusted_docker_client
  export WORKCELL_DOCKER_CLIENT_CWD="${WORKCELL_DOCKER_CLIENT_CWD:-${ROOT_DIR:-${PWD}}}"
  unset DOCKER_HOST
  if [[ -n "${WORKCELL_DOCKER_CONTEXT:-}" ]]; then
    DOCKER_CONTEXT_NAME="${WORKCELL_DOCKER_CONTEXT}"
  fi
  select_workcell_docker_context "Requested Docker context" "No healthy Docker context found" colima default
  export DOCKER_CONTEXT="${DOCKER_CONTEXT_NAME}"
}

cleanup_workcell_ci_docker() {
  cleanup_workcell_trusted_docker_client
}

cleanup_workcell_validator_image() {
  local image="$1"

  [[ -n "${image}" ]] || return 0
  [[ "${WORKCELL_KEEP_VALIDATOR_IMAGE:-0}" != "1" ]] || return 0
  if [[ -z "${DOCKER_CONTEXT_NAME:-}" ]]; then
    setup_workcell_ci_docker >/dev/null 2>&1 || return 0
  fi
  workcell_ci_docker image rm -f "${image}" >/dev/null 2>&1 || true
}

workcell_ci_docker() {
  if [[ -n "${DOCKER_CONTEXT_NAME:-}" ]]; then
    docker --context "${DOCKER_CONTEXT_NAME}" "$@"
  else
    docker "$@"
  fi
}

require_workcell_ci_workspace_mount() {
  local image="$1"
  local workspace="$2"
  local docker_bin=""
  local context_explicit="false"

  docker_bin="$(command -v docker 2>/dev/null || true)"
  [[ -n "${docker_bin}" && "${docker_bin}" == /* ]] || {
    echo "Missing required tool: docker" >&2
    return 2
  }
  [[ -z "${WORKCELL_DOCKER_CONTEXT:-}" ]] || context_explicit="true"
  run_go_in_repo "${ROOT_DIR}" run ./cmd/workcell-citools \
    validate-docker-workspace-bind \
    "${docker_bin}" \
    "${image}" \
    "${workspace}" \
    "${DOCKER_CONTEXT_NAME:-}" \
    "${context_explicit}"
}

workcell_ci_workspace_mount_spec() {
  local workspace="$1"
  local readonly="$2"

  run_go_in_repo "${ROOT_DIR}" run ./cmd/workcell-citools \
    docker-workspace-bind-mount \
    "${workspace}" \
    "${readonly}"
}
