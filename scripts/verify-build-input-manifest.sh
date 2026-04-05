#!/bin/bash -p
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  exec /usr/bin/env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME="${HOME:-/tmp}" \
    SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH-}" \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_BUILD_INPUT_REF="${WORKCELL_BUILD_INPUT_REF-}" \
    WORKCELL_SANITIZED_ENTRYPOINT=1 \
    /bin/bash -p "$0" "$@"
fi
set -euo pipefail
export PATH="${TRUSTED_HOST_PATH}"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "verify-build-input-manifest-entrypoint-ok"
  exit 0
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/workcell-build-inputs.XXXXXX")"
NESTED_ROOT=""
SAFE_GIT_CONFIG="${TMP_ROOT}/safe-gitconfig"

cleanup() {
  rm -rf "${TMP_ROOT}"
  if [[ -n "${NESTED_ROOT}" ]]; then
    rm -rf "${NESTED_ROOT}"
  fi
}

trap cleanup EXIT

git config --file "${SAFE_GIT_CONFIG}" --add safe.directory "${ROOT_DIR}"
if [[ -d "${ROOT_DIR}/.git" ]]; then
  git config --file "${SAFE_GIT_CONFIG}" --add safe.directory "${ROOT_DIR}/.git"
fi

safe_git() {
  env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME=/tmp \
    LC_ALL=C \
    LANG=C \
    GIT_ATTR_NOSYSTEM=1 \
    GIT_CONFIG_NOSYSTEM=1 \
    GIT_CONFIG_SYSTEM=/dev/null \
    GIT_CONFIG_GLOBAL="${SAFE_GIT_CONFIG}" \
    git "$@"
}

BUILD_REF="${WORKCELL_BUILD_INPUT_REF:-$(safe_git -C "${ROOT_DIR}" rev-parse HEAD 2>/dev/null || printf 'UNKNOWN')}"
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(safe_git -C "${ROOT_DIR}" log -1 --pretty=%ct 2>/dev/null || printf '0')}"

copy_tracked_worktree() {
  local destination="$1"
  local path source_path destination_path

  mkdir -p "${destination}"
  while IFS= read -r -d '' path; do
    source_path="${ROOT_DIR}/${path}"
    destination_path="${destination}/${path}"
    if [[ ! -e "${source_path}" ]]; then
      continue
    fi
    mkdir -p "$(dirname "${destination_path}")"
    cp -pP "${source_path}" "${destination_path}"
  done < <(safe_git -C "${ROOT_DIR}" ls-files -z --cached --modified --others --exclude-standard --deduplicate)
}

export WORKCELL_BUILD_INPUT_REF="${BUILD_REF}"
export SOURCE_DATE_EPOCH
export WORKCELL_BUILD_INPUT_REQUIRE_TRACKED=1

"${ROOT_DIR}/scripts/generate-build-input-manifest.sh" "${TMP_ROOT}/a.json"
"${ROOT_DIR}/scripts/generate-build-input-manifest.sh" "${TMP_ROOT}/b.json"

digest_a="$(shasum -a 256 "${TMP_ROOT}/a.json" | awk '{print $1}')"
digest_b="$(shasum -a 256 "${TMP_ROOT}/b.json" | awk '{print $1}')"

if [[ "${digest_a}" != "${digest_b}" ]]; then
  echo "Non-deterministic build input manifest: ${digest_a} != ${digest_b}" >&2
  diff -u "${TMP_ROOT}/a.json" "${TMP_ROOT}/b.json" || true
  exit 1
fi

if safe_git -C "${ROOT_DIR}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  mkdir -p "${ROOT_DIR}/tmp"
  NESTED_ROOT="$(mktemp -d "${ROOT_DIR}/tmp/workcell-build-input-nested.XXXXXX")"
  ARCHIVE_ROOT="${TMP_ROOT}/archived-source"
  copy_tracked_worktree "${ARCHIVE_ROOT}"
  copy_tracked_worktree "${NESTED_ROOT}"

  unset WORKCELL_BUILD_INPUT_REQUIRE_TRACKED
  WORKCELL_BUILD_INPUT_ROOT="${ARCHIVE_ROOT}" \
    "${ROOT_DIR}/scripts/generate-build-input-manifest.sh" "${TMP_ROOT}/archive-outside.json"
  WORKCELL_BUILD_INPUT_ROOT="${NESTED_ROOT}" \
    "${ROOT_DIR}/scripts/generate-build-input-manifest.sh" "${TMP_ROOT}/archive-nested.json"
  (
    cd "${TMP_ROOT}"
    WORKCELL_BUILD_INPUT_ROOT="${ARCHIVE_ROOT}" \
      "${ROOT_DIR}/scripts/generate-build-input-manifest.sh" "archive-relative.json"
  )

  digest_archive_outside="$(shasum -a 256 "${TMP_ROOT}/archive-outside.json" | awk '{print $1}')"
  digest_archive_nested="$(shasum -a 256 "${TMP_ROOT}/archive-nested.json" | awk '{print $1}')"
  digest_archive_relative="$(shasum -a 256 "${TMP_ROOT}/archive-relative.json" | awk '{print $1}')"
  if [[ "${digest_a}" != "${digest_archive_outside}" ]]; then
    echo "Archived-source manifest diverged from the tracked working-tree manifest: ${digest_archive_outside} != ${digest_a}" >&2
    diff -u "${TMP_ROOT}/a.json" "${TMP_ROOT}/archive-outside.json" || true
    exit 1
  fi
  if [[ "${digest_archive_outside}" != "${digest_archive_nested}" ]]; then
    echo "Nested archived-source manifest diverged from standalone archived-source manifest: ${digest_archive_nested} != ${digest_archive_outside}" >&2
    diff -u "${TMP_ROOT}/archive-outside.json" "${TMP_ROOT}/archive-nested.json" || true
    exit 1
  fi
  if [[ "${digest_archive_outside}" != "${digest_archive_relative}" ]]; then
    echo "Relative-output archived-source manifest diverged from absolute-output archived-source manifest: ${digest_archive_relative} != ${digest_archive_outside}" >&2
    diff -u "${TMP_ROOT}/archive-outside.json" "${TMP_ROOT}/archive-relative.json" || true
    exit 1
  fi
fi

echo "Workcell build input manifest verification passed."
