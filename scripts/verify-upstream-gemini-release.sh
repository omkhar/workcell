#!/bin/bash -p
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/local/go/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
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
  echo "verify-upstream-gemini-release-entrypoint-ok"
  exit 0
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PACKAGE_JSON_PATH="${ROOT_DIR}/runtime/container/providers/package.json"
PACKAGE_LOCK_PATH="${ROOT_DIR}/runtime/container/providers/package-lock.json"

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

require_tool curl
require_tool jq

extract_lock_field() {
  local package_path="$1"
  local field_name="$2"
  jq -r --arg package_path "${package_path}" --arg field_name "${field_name}" '
    .packages[$package_path][$field_name] // empty
  ' "${PACKAGE_LOCK_PATH}"
}

verify_registry_package() {
  local package_name="$1"
  local package_path="$2"
  local registry_path="$3"
  local version="$4"
  local expected_resolved="$5"
  local expected_integrity="$6"
  local metadata_path="${TMPDIR:-/tmp}/workcell-${registry_path##*/}-${version}.json"

  curl -fsSL "https://registry.npmjs.org/${registry_path}/${version}" -o "${metadata_path}"

  local actual_version actual_resolved actual_integrity
  actual_version="$(jq -r '.version' "${metadata_path}")"
  actual_resolved="$(jq -r '.dist.tarball' "${metadata_path}")"
  actual_integrity="$(jq -r '.dist.integrity' "${metadata_path}")"

  if [[ "${actual_version}" != "${version}" ]]; then
    echo "${package_name} registry metadata returned ${actual_version}, expected ${version}" >&2
    exit 1
  fi
  if [[ "${actual_resolved}" != "${expected_resolved}" ]]; then
    echo "${package_name} tarball URL mismatch: expected ${expected_resolved}, got ${actual_resolved}" >&2
    exit 1
  fi
  if [[ "${actual_integrity}" != "${expected_integrity}" ]]; then
    echo "${package_name} integrity mismatch for ${package_path}" >&2
    exit 1
  fi

  rm -f "${metadata_path}"
}

gemini_version="$(jq -r '.dependencies["@google/gemini-cli"] // empty' "${PACKAGE_JSON_PATH}")"
if [[ -z "${gemini_version}" ]]; then
  echo "package.json must pin @google/gemini-cli" >&2
  exit 1
fi

lock_gemini_version="$(extract_lock_field "node_modules/@google/gemini-cli" "version")"
lock_gemini_resolved="$(extract_lock_field "node_modules/@google/gemini-cli" "resolved")"
lock_gemini_integrity="$(extract_lock_field "node_modules/@google/gemini-cli" "integrity")"
if [[ "${lock_gemini_version}" != "${gemini_version}" ]]; then
  echo "Gemini package-lock version mismatch: package.json=${gemini_version}, package-lock=${lock_gemini_version}" >&2
  exit 1
fi
verify_registry_package "@google/gemini-cli" "node_modules/@google/gemini-cli" "@google%2fgemini-cli" "${lock_gemini_version}" "${lock_gemini_resolved}" "${lock_gemini_integrity}"

core_version="$(extract_lock_field "node_modules/@google/gemini-cli-core" "version")"
core_resolved="$(extract_lock_field "node_modules/@google/gemini-cli-core" "resolved")"
core_integrity="$(extract_lock_field "node_modules/@google/gemini-cli-core" "integrity")"
if [[ -n "${core_version}" || -n "${core_resolved}" || -n "${core_integrity}" ]]; then
  if [[ -z "${core_version}" || -z "${core_resolved}" || -z "${core_integrity}" ]]; then
    echo "Gemini package-lock must either omit @google/gemini-cli-core or pin it completely" >&2
    exit 1
  fi
  verify_registry_package "@google/gemini-cli-core" "node_modules/@google/gemini-cli-core" "@google%2fgemini-cli-core" "${core_version}" "${core_resolved}" "${core_integrity}"
fi

echo "Workcell upstream Gemini release verification passed."
