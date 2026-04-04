#!/bin/bash -p
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  exec /usr/bin/env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME=/tmp \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_BUILDER_ENV_DOCKER_CONTEXT="${WORKCELL_BUILDER_ENV_DOCKER_CONTEXT-}" \
    WORKCELL_BUILDKIT_IMAGE="${WORKCELL_BUILDKIT_IMAGE-}" \
    WORKCELL_BUILDX_VERSION="${WORKCELL_BUILDX_VERSION-}" \
    WORKCELL_COSIGN_VERSION="${WORKCELL_COSIGN_VERSION-}" \
    WORKCELL_QEMU_IMAGE="${WORKCELL_QEMU_IMAGE-}" \
    WORKCELL_SANITIZED_ENTRYPOINT=1 \
    WORKCELL_SYFT_VERSION="${WORKCELL_SYFT_VERSION-}" \
    /bin/bash -p "$0" "$@"
fi
set -euo pipefail
export PATH="${TRUSTED_HOST_PATH}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "generate-builder-environment-manifest-entrypoint-ok"
  exit 0
fi

OUTPUT_PATH="${1:-}"
DOCKER_CONTEXT_NAME="${WORKCELL_BUILDER_ENV_DOCKER_CONTEXT:-}"
BUILDKIT_IMAGE="${WORKCELL_BUILDKIT_IMAGE:-}"
BUILDX_VERSION_TARGET="${WORKCELL_BUILDX_VERSION:-}"
COSIGN_VERSION_TARGET="${WORKCELL_COSIGN_VERSION:-}"
QEMU_IMAGE="${WORKCELL_QEMU_IMAGE:-}"
SYFT_VERSION_TARGET="${WORKCELL_SYFT_VERSION:-}"

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

select_docker_context() {
  select_workcell_docker_context "Requested Docker context" "No healthy Docker context found" colima default
}

docker_cmd() {
  if [[ -n "${DOCKER_CONTEXT_NAME}" ]]; then
    docker --context "${DOCKER_CONTEXT_NAME}" "$@"
  else
    docker "$@"
  fi
}

cleanup() {
  cleanup_workcell_trusted_docker_client
}

trap cleanup EXIT

if [[ "${1:-}" == "--self-docker-probe" ]]; then
  require_tool docker
  setup_workcell_trusted_docker_client
  if [[ -n "${DOCKER_CONTEXT_NAME:-}" ]]; then
    select_docker_context
  fi
  buildx_cmd version >/dev/null
  echo "generate-builder-environment-manifest-docker-probe-ok"
  exit 0
fi

[[ -n "${OUTPUT_PATH}" ]] || {
  echo "usage: $0 OUTPUT_PATH" >&2
  exit 64
}

require_tool docker
require_tool go
setup_workcell_trusted_docker_client
select_docker_context

docker_version_json="$(docker_cmd version --format '{{json .}}')"
buildx_version="$(buildx_cmd version)"
buildx_inspect="$(buildx_cmd inspect --bootstrap)"
cosign_version=""
curl_version="$(curl --version 2>/dev/null | head -n1 || true)"
gzip_version="$(gzip --version 2>/dev/null | head -n1 || true)"
git_version="$(git --version 2>/dev/null || true)"
qemu_version=""
syft_version=""
tar_version="$(tar --version 2>/dev/null | head -n1 || true)"

if command -v cosign >/dev/null 2>&1; then
  cosign_version="$(cosign version 2>/dev/null || true)"
fi

if [[ -n "${QEMU_IMAGE}" ]]; then
  qemu_version="$(docker_cmd run --privileged --rm "${QEMU_IMAGE}" --version)"
fi

if command -v syft >/dev/null 2>&1; then
  syft_version="$(syft version 2>/dev/null || true)"
fi

(cd "${ROOT_DIR}" && go run ./cmd/workcell-metadatautil generate-builder-environment-manifest "${OUTPUT_PATH}" "${BUILDKIT_IMAGE}" "${BUILDX_VERSION_TARGET}" "${COSIGN_VERSION_TARGET}" "${QEMU_IMAGE}" "${SYFT_VERSION_TARGET}" "${buildx_version}" "${buildx_inspect}" "${docker_version_json}" "${qemu_version}" "${cosign_version}" "${curl_version}" "${git_version}" "${gzip_version}" "${syft_version}" "${tar_version}")
