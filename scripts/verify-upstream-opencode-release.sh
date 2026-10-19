#!/bin/bash -p
# shellcheck source=scripts/lib/trusted-entrypoint.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/trusted-entrypoint.sh"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "verify-upstream-opencode-release-entrypoint-ok"
  exit 0
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PACKAGE_JSON_PATH="${ROOT_DIR}/runtime/container/providers-uncertified/package.json"
PACKAGE_LOCK_PATH="${ROOT_DIR}/runtime/container/providers-uncertified/package-lock.json"

require_tool curl
require_tool jq
# shellcheck source=scripts/lib/npm-registry-verify.sh
source "${ROOT_DIR}/scripts/lib/npm-registry-verify.sh"

opencode_version="$(jq -r '.dependencies["opencode-ai"] // empty' "${PACKAGE_JSON_PATH}")"
if [[ -z "${opencode_version}" ]]; then
  echo "package.json must pin opencode-ai" >&2
  exit 1
fi
lock_opencode_version="$(extract_lock_field "${PACKAGE_LOCK_PATH}" "node_modules/opencode-ai" "version")"
if [[ "${lock_opencode_version}" != "${opencode_version}" ]]; then
  echo "opencode package-lock version mismatch: package.json=${opencode_version}, package-lock=${lock_opencode_version}" >&2
  exit 1
fi

verify_lock_package_tree "${PACKAGE_LOCK_PATH}" opencode-ai

echo "Workcell upstream opencode release verification passed."
