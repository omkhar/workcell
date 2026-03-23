#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/workcell-build-inputs.XXXXXX")"
NESTED_ROOT=""
BUILD_REF="${WORKCELL_BUILD_INPUT_REF:-$(git -C "${ROOT_DIR}" rev-parse HEAD 2>/dev/null || printf 'UNKNOWN')}"
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "${ROOT_DIR}" log -1 --pretty=%ct 2>/dev/null || printf '0')}"

cleanup() {
  rm -rf "${TMP_ROOT}"
  if [[ -n "${NESTED_ROOT}" ]]; then
    rm -rf "${NESTED_ROOT}"
  fi
}

trap cleanup EXIT

copy_tracked_worktree() {
  local destination="$1"
  local path source_path destination_path

  mkdir -p "${destination}"
  while IFS= read -r -d '' path; do
    source_path="${ROOT_DIR}/${path}"
    destination_path="${destination}/${path}"
    if [[ ! -e "${source_path}" ]]; then
      echo "Tracked release input is missing from the working tree during archived-source verification: ${path}" >&2
      exit 1
    fi
    mkdir -p "$(dirname "${destination_path}")"
    cp -pP "${source_path}" "${destination_path}"
  done < <(git -C "${ROOT_DIR}" ls-files -z)
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

if git -C "${ROOT_DIR}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  NESTED_ROOT="$(mktemp -d "${ROOT_DIR}/tmp/workcell-build-input-nested.XXXXXX")"
  ARCHIVE_ROOT="${TMP_ROOT}/archived-source"
  copy_tracked_worktree "${ARCHIVE_ROOT}"
  copy_tracked_worktree "${NESTED_ROOT}"

  unset WORKCELL_BUILD_INPUT_REQUIRE_TRACKED
  WORKCELL_BUILD_INPUT_ROOT="${ARCHIVE_ROOT}" \
    "${ROOT_DIR}/scripts/generate-build-input-manifest.sh" "${TMP_ROOT}/archive-outside.json"
  WORKCELL_BUILD_INPUT_ROOT="${NESTED_ROOT}" \
    "${ROOT_DIR}/scripts/generate-build-input-manifest.sh" "${TMP_ROOT}/archive-nested.json"

  digest_archive_outside="$(shasum -a 256 "${TMP_ROOT}/archive-outside.json" | awk '{print $1}')"
  digest_archive_nested="$(shasum -a 256 "${TMP_ROOT}/archive-nested.json" | awk '{print $1}')"
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
fi

echo "Workcell build input manifest verification passed."
