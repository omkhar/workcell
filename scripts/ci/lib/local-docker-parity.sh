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

workcell_ci_docker() {
  if [[ -n "${DOCKER_CONTEXT_NAME:-}" ]]; then
    docker --context "${DOCKER_CONTEXT_NAME}" "$@"
  else
    docker "$@"
  fi
}
