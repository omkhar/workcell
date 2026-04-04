#!/bin/bash -p
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  exec /usr/bin/env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME="${HOME:-/tmp}" \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_SANITIZED_ENTRYPOINT=1 \
    /bin/bash -p "$0" "$@"
fi
set -euo pipefail
export PATH="${TRUSTED_HOST_PATH}"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "verify-control-plane-manifest-entrypoint-ok"
  exit 0
fi

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

resolve_go_bin() {
  local candidate

  if candidate="$(command -v go 2>/dev/null)"; then
    printf '%s\n' "${candidate}"
    return 0
  fi

  for candidate in \
    /opt/homebrew/bin/go \
    /usr/local/go/bin/go \
    /usr/local/bin/go \
    /usr/bin/go; do
    if [[ -x "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done

  echo "Missing required tool: go" >&2
  exit 1
}

GO_BIN="$(resolve_go_bin)"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMMITTED_MANIFEST="${ROOT_DIR}/runtime/container/control-plane-manifest.json"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/workcell-control-plane.XXXXXX")"
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

copy_tracked_worktree() {
  local destination="$1"
  local path=""
  local source_path=""
  local destination_path=""

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

"${ROOT_DIR}/scripts/generate-control-plane-manifest.sh" "${TMP_ROOT}/a.json"
"${ROOT_DIR}/scripts/generate-control-plane-manifest.sh" "${TMP_ROOT}/b.json"

digest_a="$(shasum -a 256 "${TMP_ROOT}/a.json" | awk '{print $1}')"
digest_b="$(shasum -a 256 "${TMP_ROOT}/b.json" | awk '{print $1}')"

if [[ "${digest_a}" != "${digest_b}" ]]; then
  echo "Non-deterministic control-plane manifest: ${digest_a} != ${digest_b}" >&2
  diff -u "${TMP_ROOT}/a.json" "${TMP_ROOT}/b.json" || true
  exit 1
fi

committed_digest="$(shasum -a 256 "${COMMITTED_MANIFEST}" | awk '{print $1}')"
if [[ "${digest_a}" != "${committed_digest}" ]]; then
  echo "Committed control-plane manifest diverged from the generated manifest: ${committed_digest} != ${digest_a}" >&2
  diff -u "${COMMITTED_MANIFEST}" "${TMP_ROOT}/a.json" || true
  exit 1
fi

(cd "${ROOT_DIR}" && "${GO_BIN}" run ./cmd/workcell-metadatautil verify-control-plane-manifest "${TMP_ROOT}/a.json")

if safe_git -C "${ROOT_DIR}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  mkdir -p "${ROOT_DIR}/tmp"
  NESTED_ROOT="$(mktemp -d "${ROOT_DIR}/tmp/workcell-control-plane-nested.XXXXXX")"
  ARCHIVE_ROOT="${TMP_ROOT}/archived-source"
  copy_tracked_worktree "${ARCHIVE_ROOT}"
  copy_tracked_worktree "${NESTED_ROOT}"

  WORKCELL_CONTROL_PLANE_ROOT="${ARCHIVE_ROOT}" \
    "${ROOT_DIR}/scripts/generate-control-plane-manifest.sh" "${TMP_ROOT}/archive-outside.json"
  WORKCELL_CONTROL_PLANE_ROOT="${NESTED_ROOT}" \
    "${ROOT_DIR}/scripts/generate-control-plane-manifest.sh" "${TMP_ROOT}/archive-nested.json"

  digest_archive_outside="$(shasum -a 256 "${TMP_ROOT}/archive-outside.json" | awk '{print $1}')"
  digest_archive_nested="$(shasum -a 256 "${TMP_ROOT}/archive-nested.json" | awk '{print $1}')"
  if [[ "${digest_a}" != "${digest_archive_outside}" ]]; then
    echo "Archived-source control-plane manifest diverged from the tracked working-tree manifest: ${digest_archive_outside} != ${digest_a}" >&2
    diff -u "${TMP_ROOT}/a.json" "${TMP_ROOT}/archive-outside.json" || true
    exit 1
  fi
  if [[ "${digest_archive_outside}" != "${digest_archive_nested}" ]]; then
    echo "Nested archived-source control-plane manifest diverged from standalone archived-source manifest: ${digest_archive_nested} != ${digest_archive_outside}" >&2
    diff -u "${TMP_ROOT}/archive-outside.json" "${TMP_ROOT}/archive-nested.json" || true
    exit 1
  fi

  cp -R "${ARCHIVE_ROOT}" "${TMP_ROOT}/symlink-artifact"
  ln -sf "${TMP_ROOT}/a.json" "${TMP_ROOT}/symlink-artifact/scripts/workcell"
  SYMLINK_LOG="${TMP_ROOT}/symlink-out.log"
  if env \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME="${HOME:-/tmp}" \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_CONTROL_PLANE_ROOT="${TMP_ROOT}/symlink-artifact" \
    "${ROOT_DIR}/scripts/generate-control-plane-manifest.sh" "${TMP_ROOT}/symlink-out.json" \
    >"${SYMLINK_LOG}" 2>&1; then
    echo "Expected control-plane manifest generation to reject symlinked tracked artifacts" >&2
    exit 1
  fi
  if ! grep -q "must not be a symlink" "${SYMLINK_LOG}"; then
    echo "Expected symlinked tracked artifacts to fail with an explicit manifest error" >&2
    cat "${SYMLINK_LOG}" >&2
    exit 1
  fi
fi

echo "Workcell control-plane manifest verification passed."
