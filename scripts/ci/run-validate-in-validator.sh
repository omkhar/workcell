#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
source "${ROOT_DIR}/scripts/ci/lib/local-docker-parity.sh"
VALIDATOR_IMAGE="${WORKCELL_VALIDATOR_IMAGE:-}"
VALIDATE_PROFILE="${WORKCELL_VALIDATE_REPO_PROFILE:-release-preflight}"
WORKSPACE="${WORKCELL_VALIDATOR_WORKSPACE:-${ROOT_DIR}}"
SKIP_HEAVY_SHELLCHECK="${WORKCELL_SKIP_HEAVY_HOST_SHELLCHECK:-0}"

cleanup() {
  cleanup_workcell_ci_docker
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
validator_home="/tmp/workcell-home-${validator_uid}"
validator_cache="${validator_home}/.cache"
validator_tmp="${validator_home}/.tmp"

setup_workcell_ci_docker

# shellcheck disable=SC2016
workcell_ci_docker run --rm \
  --user "${validator_uid}:${validator_gid}" \
  --entrypoint /bin/bash \
  -e WORKCELL_SKIP_HEAVY_HOST_SHELLCHECK="${SKIP_HEAVY_SHELLCHECK}" \
  -e WORKCELL_VALIDATE_REPO_PROFILE="${VALIDATE_PROFILE}" \
  -e HOME="${validator_home}" \
  -e XDG_CACHE_HOME="${validator_cache}" \
  -e GOCACHE="${validator_cache}/go-build" \
  -e GOMODCACHE="${validator_cache}/go-mod" \
  -e CARGO_TARGET_DIR="${validator_cache}/cargo-target" \
  -e TMPDIR="${validator_tmp}" \
  -v "${WORKSPACE}:/workspace" \
  -w /workspace \
  "${VALIDATOR_IMAGE}" \
  -lc '
    set -euo pipefail
    mkdir -p "${HOME}" "${XDG_CACHE_HOME}" "${GOCACHE}" "${GOMODCACHE}" "${CARGO_TARGET_DIR}" "${TMPDIR}"
    ./scripts/validate-repo.sh
  '
