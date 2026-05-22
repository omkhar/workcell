#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
source "${ROOT_DIR}/scripts/ci/lib/local-docker-parity.sh"
VALIDATOR_IMAGE="${WORKCELL_VALIDATOR_IMAGE:-}"
WORKSPACE="${WORKCELL_VALIDATOR_WORKSPACE:-${ROOT_DIR}}"

VALIDATOR_HOME_FOR_CLEANUP=""
cleanup() {
  cleanup_workcell_ci_docker
  if [[ -n "${VALIDATOR_HOME_FOR_CLEANUP}" ]]; then
    rm -rf "${VALIDATOR_HOME_FOR_CLEANUP}"
  fi
}
trap cleanup EXIT

if [[ -z "${VALIDATOR_IMAGE}" ]]; then
  echo "WORKCELL_VALIDATOR_IMAGE is required" >&2
  exit 2
fi
if [[ ! -d "${WORKSPACE}" ]]; then
  echo "Validator workspace does not exist: ${WORKSPACE}" >&2
  exit 2
fi

validator_uid="$(id -u)"
validator_gid="$(id -g)"
# Unpredictable validator HOME under /tmp: see scripts/build-and-test.sh
# for the rationale (avoiding a /tmp planted-symlink TOCTOU surface on
# shared hosts).
validator_home="$(mktemp -d "${TMPDIR:-/tmp}/workcell-home.XXXXXX")"
chmod 0700 "${validator_home}"
VALIDATOR_HOME_FOR_CLEANUP="${validator_home}"
validator_cache="${validator_home}/.cache"
validator_tmp="${validator_home}/.tmp"

setup_workcell_ci_docker

# shellcheck disable=SC2016
workcell_ci_docker run --rm \
  --user "${validator_uid}:${validator_gid}" \
  --entrypoint /bin/bash \
  -v "${WORKSPACE}:/workspace" \
  -w /workspace \
  -e HOME="${validator_home}" \
  -e XDG_CACHE_HOME="${validator_cache}" \
  -e GOCACHE="${validator_cache}/go-build" \
  -e GOMODCACHE="${validator_cache}/go-mod" \
  -e CARGO_TARGET_DIR="${validator_cache}/cargo-target" \
  -e TMPDIR="${validator_tmp}" \
  "${VALIDATOR_IMAGE}" \
  -lc '
    set -euo pipefail
    mkdir -p "${HOME}" "${XDG_CACHE_HOME}" "${GOCACHE}" "${GOMODCACHE}" "${CARGO_TARGET_DIR}" "${TMPDIR}"
    mapfile -d "" doc_files < <(
      find /workspace \
        -path /workspace/.git -prune -o \
        -path /workspace/dist -prune -o \
        -path /workspace/tmp -prune -o \
        -path /workspace/runtime/container/providers/node_modules -prune -o \
        -path /workspace/runtime/container/rust/vendor -prune -o \
        -path /workspace/runtime/container/rust/target -prune -o \
        -type f \( -name "*.md" -o -name "*.txt" -o -name "*.1" \) -print0 | sort -z
    )
    codespell --config /workspace/.codespellrc "${doc_files[@]}"
    mandoc -Tlint /workspace/man/workcell.1 >/dev/null
  '
