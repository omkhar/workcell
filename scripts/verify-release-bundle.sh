#!/bin/bash -p
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  exec /usr/bin/env -i \
    BUILDX_BUILDER="${BUILDX_BUILDER-}" \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME=/tmp \
    SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH-}" \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_REMOTE_BUILDKIT_LOCAL_CA="${WORKCELL_REMOTE_BUILDKIT_LOCAL_CA-}" \
    WORKCELL_REMOTE_BUILDKIT_SSL_CERTS="${WORKCELL_REMOTE_BUILDKIT_SSL_CERTS-}" \
    WORKCELL_DOCKER_HOST_HOME_ROOT="${WORKCELL_DOCKER_HOST_HOME_ROOT-}" \
    WORKCELL_DOCKER_HOST_WORKSPACE_ROOT="${WORKCELL_DOCKER_HOST_WORKSPACE_ROOT-}" \
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
BUNDLE_PREFIX="${WORKCELL_RELEASE_BUNDLE_PREFIX:-workcell-release-check}"
ARCHIVE_REF="${WORKCELL_RELEASE_BUNDLE_REF:-HEAD}"
BUNDLE_NAME="${WORKCELL_RELEASE_BUNDLE_NAME:-workcell-release-check.tar.gz}"
VALIDATOR_IMAGE="${WORKCELL_VALIDATOR_IMAGE:-workcell-validator:local}"
BUNDLE_MANIFEST_PATH="${WORKCELL_RELEASE_BUNDLE_MANIFEST_PATH:-}"

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

sanitized_git() {
  env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME=/tmp \
    LC_ALL=C \
    LANG=C \
    GIT_ATTR_NOSYSTEM=1 \
    GIT_CONFIG_NOSYSTEM=1 \
    GIT_CONFIG_SYSTEM=/dev/null \
    GIT_CONFIG_GLOBAL=/dev/null \
    "$@"
}

sanitized_clone_git() {
  local source_repo="$1"
  local source_git_dir="${source_repo%/}/.git"
  local safe_git_config
  shift

  safe_git_config="$(mktemp "${TMP_ROOT}/safe-gitconfig.XXXXXX")"
  git config --file "${safe_git_config}" --add safe.directory "${source_repo}"
  git config --file "${safe_git_config}" --add safe.directory "${source_git_dir}"

  env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME=/tmp \
    LC_ALL=C \
    LANG=C \
    GIT_ATTR_NOSYSTEM=1 \
    GIT_CONFIG_NOSYSTEM=1 \
    GIT_CONFIG_SYSTEM=/dev/null \
    GIT_CONFIG_GLOBAL="${safe_git_config}" \
    "$@"

  rm -f "${safe_git_config}"
}

mkdir -p "${ROOT_DIR}/tmp"
TMP_ROOT="$(mktemp -d "${ROOT_DIR}/tmp/workcell-release-bundle.XXXXXX")"
EMPTY_GIT_TEMPLATE_DIR="${TMP_ROOT}/empty-git-template"
mkdir -p "${EMPTY_GIT_TEMPLATE_DIR}"

cleanup() {
  cleanup_workcell_trusted_docker_client
  rm -rf "${TMP_ROOT}"
}

trap cleanup EXIT

select_docker_context() {
  select_workcell_docker_context "Requested Docker context" "No healthy Docker context found" colima default
}

prepare_sanitized_clone() {
  local source_repo="$1"
  local clone_dir="$2"

  rm -rf "${clone_dir}"
  sanitized_clone_git "${source_repo}" git clone \
    --quiet \
    --no-checkout \
    --no-local \
    --template "${EMPTY_GIT_TEMPLATE_DIR}" \
    "${source_repo}" \
    "${clone_dir}"
}

resolve_source_date_epoch() {
  local probe_clone="${TMP_ROOT}/source-date-epoch-clone"
  local source_date_epoch

  prepare_sanitized_clone "${ROOT_DIR}" "${probe_clone}"
  source_date_epoch="$(sanitized_git git -C "${probe_clone}" log -1 --pretty=%ct "${ARCHIVE_REF}" 2>/dev/null || printf '0')"
  rm -rf "${probe_clone}"
  printf '%s\n' "${source_date_epoch}"
}

SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(resolve_source_date_epoch)}"

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
  local clone_dir

  clone_dir="${TMP_ROOT}/clone-$(basename "${destination_dir}")"

  mkdir -p "${destination_dir}"
  prepare_sanitized_clone "${ROOT_DIR}" "${clone_dir}"
  sanitized_git git -C "${clone_dir}" archive \
    --format=tar \
    --mtime="@${SOURCE_DATE_EPOCH}" \
    --prefix="${prefix}" \
    -o "${tar_path}" \
    "${ARCHIVE_REF}"
  gzip -n -9 <"${tar_path}" >"${destination}"
  rm -f "${tar_path}"
  rm -rf "${clone_dir}"
  (cd "${destination_dir}" && sha256sum "${BUNDLE_NAME}" >SHA256SUMS)
}

build_bundle_in_validator() {
  local destination_dir="$1"
  local prefix="${BUNDLE_PREFIX%/}/"
  local docker_root=""
  local relative_destination

  mkdir -p "${destination_dir}"
  docker_root="$(workcell_docker_host_path "${ROOT_DIR}")"
  relative_destination="${destination_dir#"${ROOT_DIR}/"}"

  # shellcheck disable=SC2016
  docker_cmd run --rm \
    --entrypoint /bin/bash \
    -v "${docker_root}:/workspace" \
    -w /workspace \
    -e HOME=/tmp \
    -e SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}" \
    -e BUNDLE_NAME="${BUNDLE_NAME}" \
    -e BUNDLE_PREFIX="${prefix}" \
    -e ARCHIVE_REF="${ARCHIVE_REF}" \
    -e DESTINATION_DIR="/workspace/${relative_destination}" \
    -e GIT_ATTR_NOSYSTEM=1 \
    -e GIT_CONFIG_NOSYSTEM=1 \
    -e GIT_CONFIG_SYSTEM=/dev/null \
    -e GIT_CONFIG_GLOBAL=/dev/null \
    "${VALIDATOR_IMAGE}" \
    -lc '
      set -euo pipefail
      tar_path="${DESTINATION_DIR}/${BUNDLE_NAME%.tar.gz}.tar"
      clone_dir="$(mktemp -d /tmp/workcell-release-clone.XXXXXX)"
      template_dir="$(mktemp -d /tmp/workcell-git-template.XXXXXX)"
      gitconfig="$(mktemp /tmp/workcell-safe-gitconfig.XXXXXX)"
      trap '\''rm -rf "${clone_dir}" "${template_dir}" "${gitconfig}"'\'' EXIT
      git config --file "${gitconfig}" --add safe.directory /workspace
      git config --file "${gitconfig}" --add safe.directory /workspace/.git
      GIT_CONFIG_GLOBAL="${gitconfig}" git \
        clone \
        --quiet \
        --no-checkout \
        --no-local \
        --template "${template_dir}" \
        /workspace \
        "${clone_dir}"
      git -C "${clone_dir}" archive \
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
  ensure_workcell_selected_builder
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
