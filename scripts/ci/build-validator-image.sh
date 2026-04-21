#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
source "${ROOT_DIR}/scripts/ci/lib/local-docker-parity.sh"
VALIDATOR_DOCKERFILE="${ROOT_DIR}/tools/validator/Dockerfile"
VALIDATOR_IMAGE_DEFAULT_TAG="workcell-validator:local-$(cksum "${VALIDATOR_DOCKERFILE}" | awk '{print $1}')"
VALIDATOR_IMAGE="${WORKCELL_VALIDATOR_IMAGE:-${VALIDATOR_IMAGE_DEFAULT_TAG}}"
REBUILD_VALIDATOR="${WORKCELL_REBUILD_VALIDATOR_IMAGE:-0}"
CACHE_MODE="${WORKCELL_VALIDATOR_BUILDX_CACHE_MODE:-none}"
BUILDKIT_IMAGE="${WORKCELL_BUILDKIT_IMAGE:-moby/buildkit:buildx-stable-1@sha256:0039c1d47e8748b5afea56f4e85f14febaf34452bd99d9552d2daa82262b5cc5}"

cleanup() {
  cleanup_workcell_ci_docker
}
trap cleanup EXIT

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

require_tool docker
setup_workcell_ci_docker
buildx_cmd version >/dev/null 2>&1 || {
  echo "docker buildx is required for validator-image parity" >&2
  exit 1
}

if [[ "${REBUILD_VALIDATOR}" -eq 0 ]] && workcell_ci_docker image inspect "${VALIDATOR_IMAGE}" >/dev/null 2>&1; then
  echo "${VALIDATOR_IMAGE}"
  exit 0
fi

build_cmd=(
  buildx_cmd build
  --build-arg "WORKCELL_BUILDKIT_IMAGE=${BUILDKIT_IMAGE}"
  -f "${VALIDATOR_DOCKERFILE}"
  -t "${VALIDATOR_IMAGE}"
  --load
)

case "${CACHE_MODE}" in
  gha)
    build_cmd+=(--cache-from "type=gha" --cache-to "type=gha,mode=max")
    ;;
  none) ;;
  *)
    echo "Unsupported validator buildx cache mode: ${CACHE_MODE}" >&2
    exit 2
    ;;
esac

build_cmd+=("${ROOT_DIR}")
"${build_cmd[@]}"
echo "${VALIDATOR_IMAGE}"
