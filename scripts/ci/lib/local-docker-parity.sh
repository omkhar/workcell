#!/usr/bin/env -S BASH_ENV= ENV= bash
# shellcheck shell=bash

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
