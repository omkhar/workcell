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

workcell_validator_image_default_tag() {
  local root="$1"
  local dockerfile_cksum=""
  local bootstrap_cksum=""

  dockerfile_cksum="$(cksum "${root}/tools/validator/Dockerfile" | awk '{print $1}')" || return
  bootstrap_cksum="$(cksum "${root}/runtime/container/debian-bootstrap.env" | awk '{print $1}')" || return
  printf 'workcell-validator:local-%s-%s\n' "${dockerfile_cksum}" "${bootstrap_cksum}"
}

claim_workcell_validator_image() {
  local root="$1"
  local image_variable="$2"
  local reservation_variable="$3"
  local prefix=""
  local claimed_reservation=""

  prefix="$(workcell_validator_image_default_tag "${root}")" || return
  [[ "${prefix}" == workcell-validator:local-[0-9]*-[0-9]* ]] || return 1
  claimed_reservation="$(mktemp -d "${TMPDIR:-/tmp}/workcell-validator-owner.XXXXXX")" || return
  printf -v "${reservation_variable}" '%s' "${claimed_reservation}"
  printf -v "${image_variable}" '%s-%s' "${prefix}" "${claimed_reservation##*/}"
}

cleanup_workcell_owned_validator_image() {
  local image="$1"
  local reservation="$2"

  cleanup_workcell_validator_image "${image}"
  rmdir -- "${reservation}" >/dev/null 2>&1 || true
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
  # The target defaults to /workspace. It is CSV encoded with the source, so a
  # path holding a comma or a quote still forms one --mount record.
  local target="${3:-/workspace}"

  run_go_in_repo "${ROOT_DIR}" run ./cmd/workcell-citools \
    docker-workspace-bind-mount \
    "${workspace}" \
    "${readonly}" \
    "${target}"
}
