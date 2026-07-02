#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-copilot-provider-e2e.XXXXXX")"

cleanup() {
  local status=$?
  chmod -R u+w "${TMP_DIR}" 2>/dev/null || true
  rm -rf "${TMP_DIR}"
  exit "${status}"
}
trap cleanup EXIT

WORKSPACE="${TMP_DIR}/workspace"
mkdir -p "${WORKSPACE}"
git -C "${WORKSPACE}" init -q
printf 'copilot provider e2e certification workspace\n' >"${WORKSPACE}/README.md"
WORKSPACE="$(cd "${WORKSPACE}" && pwd -P)"

"${ROOT_DIR}/scripts/provider-e2e.sh" \
  --agent copilot \
  --workspace "${WORKSPACE}" \
  --require-injection
