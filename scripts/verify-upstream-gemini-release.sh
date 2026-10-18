#!/bin/bash -p
# shellcheck source=scripts/lib/trusted-entrypoint.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/trusted-entrypoint.sh"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "verify-upstream-gemini-release-entrypoint-ok"
  exit 0
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PACKAGE_JSON_PATH="${ROOT_DIR}/runtime/container/providers/package.json"
PACKAGE_LOCK_PATH="${ROOT_DIR}/runtime/container/providers/package-lock.json"

require_tool curl
require_tool jq
# shellcheck source=scripts/lib/npm-registry-verify.sh
source "${ROOT_DIR}/scripts/lib/npm-registry-verify.sh"

gemini_version="$(jq -r '.dependencies["@google/gemini-cli"] // empty' "${PACKAGE_JSON_PATH}")"
if [[ -z "${gemini_version}" ]]; then
  echo "package.json must pin @google/gemini-cli" >&2
  exit 1
fi

lock_gemini_version="$(extract_lock_field "${PACKAGE_LOCK_PATH}" "node_modules/@google/gemini-cli" "version")"
lock_gemini_resolved="$(extract_lock_field "${PACKAGE_LOCK_PATH}" "node_modules/@google/gemini-cli" "resolved")"
lock_gemini_integrity="$(extract_lock_field "${PACKAGE_LOCK_PATH}" "node_modules/@google/gemini-cli" "integrity")"
if [[ "${lock_gemini_version}" != "${gemini_version}" ]]; then
  echo "Gemini package-lock version mismatch: package.json=${gemini_version}, package-lock=${lock_gemini_version}" >&2
  exit 1
fi
verify_registry_package "@google/gemini-cli" "node_modules/@google/gemini-cli" "@google%2fgemini-cli" "${lock_gemini_version}" "${lock_gemini_resolved}" "${lock_gemini_integrity}"

core_version="$(extract_lock_field "${PACKAGE_LOCK_PATH}" "node_modules/@google/gemini-cli-core" "version")"
core_resolved="$(extract_lock_field "${PACKAGE_LOCK_PATH}" "node_modules/@google/gemini-cli-core" "resolved")"
core_integrity="$(extract_lock_field "${PACKAGE_LOCK_PATH}" "node_modules/@google/gemini-cli-core" "integrity")"
if [[ -n "${core_version}" || -n "${core_resolved}" || -n "${core_integrity}" ]]; then
  if [[ -z "${core_version}" || -z "${core_resolved}" || -z "${core_integrity}" ]]; then
    echo "Gemini package-lock must either omit @google/gemini-cli-core or pin it completely" >&2
    exit 1
  fi
  verify_registry_package "@google/gemini-cli-core" "node_modules/@google/gemini-cli-core" "@google%2fgemini-cli-core" "${core_version}" "${core_resolved}" "${core_integrity}"
fi

echo "Workcell upstream Gemini release verification passed."
