#!/bin/bash -p
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/local/go/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  exec /usr/bin/env -i \
    BUILDX_BUILDER="${BUILDX_BUILDER-}" \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME=/tmp \
    SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH-}" \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_DOCKER_REAL_HOME="${WORKCELL_DOCKER_REAL_HOME-}" \
    WORKCELL_REMOTE_BUILDKIT_LOCAL_CA="${WORKCELL_REMOTE_BUILDKIT_LOCAL_CA-}" \
    WORKCELL_REMOTE_BUILDKIT_SSL_CERTS="${WORKCELL_REMOTE_BUILDKIT_SSL_CERTS-}" \
    WORKCELL_DOCKER_HOST_HOME_ROOT="${WORKCELL_DOCKER_HOST_HOME_ROOT-}" \
    WORKCELL_DOCKER_HOST_WORKSPACE_ROOT="${WORKCELL_DOCKER_HOST_WORKSPACE_ROOT-}" \
    WORKCELL_REPRO_BUILD_MODE="${WORKCELL_REPRO_BUILD_MODE-}" \
    WORKCELL_REPRO_DOCKER_CONTEXT="${WORKCELL_REPRO_DOCKER_CONTEXT-}" \
    WORKCELL_REPRO_MANIFEST_PATH="${WORKCELL_REPRO_MANIFEST_PATH-}" \
    WORKCELL_REPRO_PLATFORMS="${WORKCELL_REPRO_PLATFORMS-}" \
    WORKCELL_SANITIZED_ENTRYPOINT=1 \
    /bin/bash -p "$0" "$@"
fi
set -euo pipefail
export PATH="${TRUSTED_HOST_PATH}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
DOCKER_CONTEXT_NAME="${WORKCELL_REPRO_DOCKER_CONTEXT:-}"
REPRO_PLATFORMS="${WORKCELL_REPRO_PLATFORMS:-linux/amd64,linux/arm64}"
REPRO_BUILD_MODE="${WORKCELL_REPRO_BUILD_MODE:-serial}"
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "${ROOT_DIR}" log -1 --pretty=%ct 2>/dev/null || printf '0')}"
REPRO_MANIFEST_PATH="${WORKCELL_REPRO_MANIFEST_PATH:-}"
OCI_EXPORT_ROOT=""
OCI_EXPORT_A=""
OCI_EXPORT_B=""
REPRO_REFERENCE_MANIFEST=""
WORKCELL_DOCKER_SANDBOX_ROOT=""

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "verify-reproducible-build-entrypoint-ok"
  exit 0
fi

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

build_oci_layout() {
  local platforms="$1"
  local dest="$2"
  local build_source_date_epoch="${SOURCE_DATE_EPOCH}"

  rm -rf "${dest}"
  mkdir -p "$(dirname "${dest}")"
  SOURCE_DATE_EPOCH="${build_source_date_epoch}" buildx_cmd build \
    --no-cache \
    --platform "${platforms}" \
    --build-arg "BUILDKIT_MULTI_PLATFORM=1" \
    --build-arg "SOURCE_DATE_EPOCH=${build_source_date_epoch}" \
    --provenance=false \
    --sbom=false \
    --output "type=oci,dest=${dest},tar=false,oci-mediatypes=true,rewrite-timestamp=true" \
    -f "${ROOT_DIR}/runtime/container/Dockerfile" \
    "${ROOT_DIR}" >/dev/null
}

build_oci_layout_pair() {
  local platforms="$1"
  local dest_a="$2"
  local dest_b="$3"
  local pid_a=""
  local pid_b=""
  local status=0

  case "${REPRO_BUILD_MODE}" in
    parallel)
      build_oci_layout "${platforms}" "${dest_a}" &
      pid_a=$!
      build_oci_layout "${platforms}" "${dest_b}" &
      pid_b=$!
      wait "${pid_a}" || status=1
      wait "${pid_b}" || status=1
      return "${status}"
      ;;
    serial)
      build_oci_layout "${platforms}" "${dest_a}"
      build_oci_layout "${platforms}" "${dest_b}"
      ;;
    *)
      echo "Unsupported WORKCELL_REPRO_BUILD_MODE: ${REPRO_BUILD_MODE}" >&2
      exit 2
      ;;
  esac
}

prune_repro_builder_cache() {
  buildx_cmd prune -af >/dev/null 2>&1 || true
}

cleanup() {
  cleanup_workcell_trusted_docker_client
  rm -rf "${OCI_EXPORT_ROOT}"
}

trap cleanup EXIT

if [[ "${1:-}" == "--self-docker-probe" ]]; then
  require_tool docker
  setup_workcell_trusted_docker_client
  select_docker_context
  buildx_cmd version >/dev/null
  echo "verify-reproducible-build-docker-probe-ok"
  exit 0
fi

require_tool docker
require_tool go
setup_workcell_trusted_docker_client
select_docker_context
if [[ -z "${BUILDX_BUILDER:-}" ]]; then
  safe_builder_context="${DOCKER_CONTEXT_NAME//[^[:alnum:]_.-]/-}"
  BUILDX_BUILDER="workcell-repro-${safe_builder_context}"
fi
ensure_workcell_selected_builder
buildx_cmd inspect --bootstrap >/dev/null

mkdir -p "${TMPDIR:-/tmp}"
OCI_EXPORT_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/workcell-repro.XXXXXX")"

OCI_EXPORT_A="${OCI_EXPORT_ROOT}/a"
OCI_EXPORT_B="${OCI_EXPORT_ROOT}/b"
REPRO_REFERENCE_MANIFEST="${OCI_EXPORT_ROOT}/reference.json"
case "${REPRO_BUILD_MODE}" in
  parallel)
    build_oci_layout_pair "${REPRO_PLATFORMS}" "${OCI_EXPORT_A}" "${OCI_EXPORT_B}"
    (cd "${ROOT_DIR}" && go run ./cmd/workcell-metadatautil verify-reproducible-build "${OCI_EXPORT_A}" "${OCI_EXPORT_B}" "${REPRO_PLATFORMS}" "${REPRO_MANIFEST_PATH}" "${SOURCE_DATE_EPOCH}")
    ;;
  serial)
    build_oci_layout "${REPRO_PLATFORMS}" "${OCI_EXPORT_A}"
    (cd "${ROOT_DIR}" && go run ./cmd/workcell-metadatautil generate-reproducible-build-manifest "${OCI_EXPORT_A}" "${REPRO_PLATFORMS}" "${REPRO_REFERENCE_MANIFEST}" "${SOURCE_DATE_EPOCH}")
    rm -rf "${OCI_EXPORT_A}"
    OCI_EXPORT_A=""
    prune_repro_builder_cache
    build_oci_layout "${REPRO_PLATFORMS}" "${OCI_EXPORT_B}"
    (cd "${ROOT_DIR}" && go run ./cmd/workcell-metadatautil verify-reproducible-build-manifest "${OCI_EXPORT_B}" "${REPRO_PLATFORMS}" "${REPRO_REFERENCE_MANIFEST}")
    if [[ -n "${REPRO_MANIFEST_PATH}" ]]; then
      mkdir -p "$(dirname "${REPRO_MANIFEST_PATH}")"
      cp "${REPRO_REFERENCE_MANIFEST}" "${REPRO_MANIFEST_PATH}"
    fi
    ;;
  *)
    echo "Unsupported WORKCELL_REPRO_BUILD_MODE: ${REPRO_BUILD_MODE}" >&2
    exit 2
    ;;
esac

echo "Workcell reproducible build verification passed."
