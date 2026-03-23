#!/bin/bash -p
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  exec /usr/bin/env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME=/tmp \
    SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH-}" \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_RELEASE_BUNDLE_DOCKER_CONTEXT="${WORKCELL_RELEASE_BUNDLE_DOCKER_CONTEXT-}" \
    WORKCELL_RELEASE_BUNDLE_MANIFEST_PATH="${WORKCELL_RELEASE_BUNDLE_MANIFEST_PATH-}" \
    WORKCELL_RELEASE_BUNDLE_NAME="${WORKCELL_RELEASE_BUNDLE_NAME-}" \
    WORKCELL_RELEASE_BUNDLE_PREFIX="${WORKCELL_RELEASE_BUNDLE_PREFIX-}" \
    WORKCELL_RELEASE_BUNDLE_REF="${WORKCELL_RELEASE_BUNDLE_REF-}" \
    WORKCELL_SANITIZED_ENTRYPOINT=1 \
    WORKCELL_VALIDATOR_IMAGE="${WORKCELL_VALIDATOR_IMAGE-}" \
    /bin/bash -p "$0" "$@"
fi
set -euo pipefail
export PATH="${TRUSTED_HOST_PATH}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
DOCKER_CONTEXT_NAME="${WORKCELL_RELEASE_BUNDLE_DOCKER_CONTEXT:-}"
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "${ROOT_DIR}" log -1 --pretty=%ct 2>/dev/null || printf '0')}"
BUNDLE_PREFIX="${WORKCELL_RELEASE_BUNDLE_PREFIX:-workcell-release-check}"
ARCHIVE_REF="${WORKCELL_RELEASE_BUNDLE_REF:-HEAD}"
BUNDLE_NAME="${WORKCELL_RELEASE_BUNDLE_NAME:-workcell-release-check.tar.gz}"
VALIDATOR_IMAGE="${WORKCELL_VALIDATOR_IMAGE:-workcell-validator:local}"
BUNDLE_MANIFEST_PATH="${WORKCELL_RELEASE_BUNDLE_MANIFEST_PATH:-}"
WORKCELL_DOCKER_SANDBOX_ROOT=""

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "verify-release-bundle-entrypoint-ok"
  exit 0
fi

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

mkdir -p "${ROOT_DIR}/tmp"
TMP_ROOT="$(mktemp -d "${ROOT_DIR}/tmp/workcell-release-bundle.XXXXXX")"

cleanup() {
  cleanup_workcell_trusted_docker_client
  rm -rf "${TMP_ROOT}"
}

trap cleanup EXIT

select_docker_context() {
  if [[ -n "${DOCKER_CONTEXT_NAME}" ]]; then
    return
  fi

  if docker context inspect colima >/dev/null 2>&1; then
    DOCKER_CONTEXT_NAME="colima"
    return
  fi

  if docker context inspect default >/dev/null 2>&1; then
    DOCKER_CONTEXT_NAME="default"
  fi
}

docker_cmd() {
  if [[ -n "${DOCKER_CONTEXT_NAME}" ]]; then
    docker --context "${DOCKER_CONTEXT_NAME}" "$@"
  else
    docker "$@"
  fi
}

if [[ "${1:-}" == "--self-docker-probe" ]]; then
  require_tool docker
  setup_workcell_trusted_docker_client
  select_docker_context
  buildx_cmd version >/dev/null
  echo "verify-release-bundle-docker-probe-ok"
  exit 0
fi

build_bundle_locally() {
  local destination_dir="$1"
  local destination="${destination_dir}/${BUNDLE_NAME}"
  local tar_path="${destination%.tar.gz}.tar"
  local prefix="${BUNDLE_PREFIX%/}/"

  mkdir -p "${destination_dir}"

  git -C "${ROOT_DIR}" archive \
    --format=tar \
    --mtime="@${SOURCE_DATE_EPOCH}" \
    --prefix="${prefix}" \
    -o "${tar_path}" \
    "${ARCHIVE_REF}"
  gzip -n -9 <"${tar_path}" >"${destination}"
  rm -f "${tar_path}"
  (cd "${destination_dir}" && sha256sum "${BUNDLE_NAME}" >SHA256SUMS)
}

build_bundle_in_validator() {
  local destination_dir="$1"
  local prefix="${BUNDLE_PREFIX%/}/"
  local relative_destination

  mkdir -p "${destination_dir}"
  relative_destination="${destination_dir#"${ROOT_DIR}/"}"

  # shellcheck disable=SC2016
  docker_cmd run --rm \
    --entrypoint /bin/bash \
    -v "${ROOT_DIR}:/workspace" \
    -w /workspace \
    -e SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}" \
    -e BUNDLE_NAME="${BUNDLE_NAME}" \
    -e BUNDLE_PREFIX="${prefix}" \
    -e ARCHIVE_REF="${ARCHIVE_REF}" \
    -e DESTINATION_DIR="/workspace/${relative_destination}" \
    "${VALIDATOR_IMAGE}" \
    -lc '
      set -euo pipefail
      tar_path="${DESTINATION_DIR}/${BUNDLE_NAME%.tar.gz}.tar"
      git archive \
        --format=tar \
        --mtime="@${SOURCE_DATE_EPOCH}" \
        --prefix="${BUNDLE_PREFIX}" \
        -o "${tar_path}" \
        "${ARCHIVE_REF}"
      gzip -n -9 <"${tar_path}" >"${DESTINATION_DIR}/${BUNDLE_NAME}"
      rm -f "${tar_path}"
      (cd "${DESTINATION_DIR}" && sha256sum "${BUNDLE_NAME}" >SHA256SUMS)
    '
}

if [[ -n "${BUNDLE_MANIFEST_PATH}" ]]; then
  require_tool python3
fi

if command -v docker >/dev/null 2>&1; then
  setup_workcell_trusted_docker_client
  select_docker_context
  buildx_cmd build \
    --load \
    -t "${VALIDATOR_IMAGE}" \
    -f "${ROOT_DIR}/tools/validator/Dockerfile" \
    "${ROOT_DIR}" >/dev/null
  build_bundle_in_validator "${TMP_ROOT}/a"
  build_bundle_in_validator "${TMP_ROOT}/b"
else
  build_bundle_locally "${TMP_ROOT}/a"
  build_bundle_locally "${TMP_ROOT}/b"
fi

digest_a="$(shasum -a 256 "${TMP_ROOT}/a/${BUNDLE_NAME}" | awk '{print $1}')"
digest_b="$(shasum -a 256 "${TMP_ROOT}/b/${BUNDLE_NAME}" | awk '{print $1}')"
checksum_a="$(shasum -a 256 "${TMP_ROOT}/a/SHA256SUMS" | awk '{print $1}')"
checksum_b="$(shasum -a 256 "${TMP_ROOT}/b/SHA256SUMS" | awk '{print $1}')"

if [[ "${digest_a}" != "${digest_b}" ]]; then
  echo "Non-reproducible release bundle: ${digest_a} != ${digest_b}" >&2
  exit 1
fi

if [[ "${checksum_a}" != "${checksum_b}" ]]; then
  echo "Non-reproducible release checksum file: ${checksum_a} != ${checksum_b}" >&2
  exit 1
fi

if [[ -n "${BUNDLE_MANIFEST_PATH}" ]]; then
  python3 - "${BUNDLE_MANIFEST_PATH}" "${ARCHIVE_REF}" "${BUNDLE_NAME}" "${BUNDLE_PREFIX%/}/" "${SOURCE_DATE_EPOCH}" "${digest_a}" "${checksum_a}" <<'PY'
import json
import pathlib
import sys

manifest_path = pathlib.Path(sys.argv[1])
manifest = {
    "archive_ref": sys.argv[2],
    "bundle_name": sys.argv[3],
    "bundle_prefix": sys.argv[4],
    "source_date_epoch": int(sys.argv[5]),
    "bundle_sha256": sys.argv[6],
    "checksums_sha256": sys.argv[7],
}
manifest_path.parent.mkdir(parents=True, exist_ok=True)
manifest_path.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY
fi

echo "Workcell release bundle reproducibility passed."
