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

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMMITTED_MANIFEST="${ROOT_DIR}/runtime/container/control-plane-manifest.json"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/workcell-control-plane.XXXXXX")"
NESTED_ROOT=""

cleanup() {
  rm -rf "${TMP_ROOT}"
  if [[ -n "${NESTED_ROOT}" ]]; then
    rm -rf "${NESTED_ROOT}"
  fi
}

trap cleanup EXIT

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
      echo "Tracked control-plane input is missing from the working tree during archived-source verification: ${path}" >&2
      exit 1
    fi
    mkdir -p "$(dirname "${destination_path}")"
    cp -pP "${source_path}" "${destination_path}"
  done < <(git -C "${ROOT_DIR}" ls-files -z)
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

python3 - "${TMP_ROOT}/a.json" <<'PY'
import json
import pathlib
import sys

manifest = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
if manifest.get("schema_version") != 2:
    raise SystemExit("control-plane manifest must use schema_version 2")

host_artifacts = manifest.get("host_artifacts")
runtime_artifacts = manifest.get("runtime_artifacts")
if not isinstance(host_artifacts, list) or not host_artifacts:
    raise SystemExit("control-plane manifest must include non-empty host_artifacts")
if not isinstance(runtime_artifacts, list) or not runtime_artifacts:
    raise SystemExit("control-plane manifest must include non-empty runtime_artifacts")

seen_runtime_paths: set[str] = set()
for entry in host_artifacts:
    if sorted(entry.keys()) != ["repo_path", "sha256"]:
        raise SystemExit(f"unexpected host artifact shape: {entry!r}")
    if len(entry["sha256"]) != 64:
        raise SystemExit(f"invalid host artifact digest: {entry!r}")

for entry in runtime_artifacts:
    for key in ("kind", "repo_path", "runtime_path", "sha256"):
        if key not in entry:
            raise SystemExit(f"runtime artifact is missing {key}: {entry!r}")
    runtime_path = entry["runtime_path"]
    if not isinstance(runtime_path, str) or not runtime_path.startswith("/"):
        raise SystemExit(f"runtime artifact path must be absolute: {entry!r}")
    if runtime_path in seen_runtime_paths:
        raise SystemExit(f"duplicate runtime artifact path: {runtime_path}")
    seen_runtime_paths.add(runtime_path)
    if len(entry["sha256"]) != 64:
        raise SystemExit(f"invalid runtime artifact digest: {entry!r}")
PY

if git -C "${ROOT_DIR}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
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
fi

echo "Workcell control-plane manifest verification passed."
