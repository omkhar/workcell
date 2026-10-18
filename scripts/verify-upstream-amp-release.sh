#!/bin/bash -p
# shellcheck source=scripts/lib/trusted-entrypoint.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/trusted-entrypoint.sh"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "verify-upstream-amp-release-entrypoint-ok"
  exit 0
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PACKAGE_JSON_PATH="${ROOT_DIR}/runtime/container/providers-uncertified/package.json"
PACKAGE_LOCK_PATH="${ROOT_DIR}/runtime/container/providers-uncertified/package-lock.json"

require_tool curl
require_tool jq
# shellcheck source=scripts/lib/npm-registry-verify.sh
source "${ROOT_DIR}/scripts/lib/npm-registry-verify.sh"

verify_pinned_package_tree "${PACKAGE_JSON_PATH}" "${PACKAGE_LOCK_PATH}" @ampcode/cli

echo "Workcell upstream amp release verification passed."
