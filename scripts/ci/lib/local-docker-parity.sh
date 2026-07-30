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

require_workcell_ci_workspace_mount() (
  local image="$1"
  local workspace="$2"
  local challenge_path="" challenge_name="" challenge_value=""
  local context_label="${DOCKER_CONTEXT_NAME:-default}"

  workspace="$(cd "${workspace}" && pwd -P)" || {
    echo "Validator workspace does not exist: ${workspace}" >&2
    return 2
  }
  [[ -f "${workspace}/go.mod" ]] && [[ -x "${workspace}/scripts/validate-repo.sh" ]] || {
    echo "Validator workspace is missing required validation inputs: ${workspace}" >&2
    return 2
  }

  challenge_path="$(mktemp "${workspace}/.workcell-validator-bind.XXXXXX")" || {
    echo "Cannot create the validator workspace bind challenge: ${workspace}" >&2
    return 2
  }
  trap 'rm -f "${challenge_path}"' EXIT
  challenge_name="${challenge_path##*/}"
  challenge_value="${challenge_name}.$$.${RANDOM}.${RANDOM}"
  printf '%s\n' "${challenge_value}" >"${challenge_path}"
  chmod 0600 "${challenge_path}"

  # shellcheck disable=SC2016
  if ! workcell_ci_docker run --rm \
    --user "$(id -u):$(id -g)" \
    --entrypoint /bin/bash \
    --mount "type=bind,src=${workspace},dst=/workspace,readonly" \
    -e "WORKCELL_VALIDATOR_BIND_CHALLENGE_NAME=${challenge_name}" \
    -e "WORKCELL_VALIDATOR_BIND_CHALLENGE_VALUE=${challenge_value}" \
    "${image}" \
    -c '
      set -euo pipefail
      test -f /workspace/go.mod
      test -x /workspace/scripts/validate-repo.sh
      test "$(cat "/workspace/${WORKCELL_VALIDATOR_BIND_CHALLENGE_NAME}")" = "${WORKCELL_VALIDATOR_BIND_CHALLENGE_VALUE}"
    ' 2>/dev/null; then
    echo "Validator workspace is not visible through Docker context ${context_label}: ${workspace}" >&2
    if [[ -n "${WORKCELL_DOCKER_CONTEXT:-}" ]]; then
      echo "Configured WORKCELL_DOCKER_CONTEXT=${WORKCELL_DOCKER_CONTEXT} cannot bind this checkout; choose a context that can." >&2
    else
      echo "Select a Docker context whose daemon can bind this checkout, or set WORKCELL_DOCKER_CONTEXT explicitly." >&2
    fi
    return 2
  fi
)
