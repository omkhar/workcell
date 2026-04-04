#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
POLICY_PATH="${ROOT_DIR}/policy/github-hosted-controls.toml"
# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/go-run-env.sh"

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

require_tool actionlint
require_tool zizmor

(
  cd "${ROOT_DIR}"
  actionlint
)
zizmor --persona auditor --config "${ROOT_DIR}/.github/zizmor.yml" "${ROOT_DIR}/.github/workflows/"*.yml

run_go_in_repo "${ROOT_DIR}" run ./cmd/workcell-metadatautil check-workflows "${ROOT_DIR}" "${POLICY_PATH}"

echo "Workcell workflow checks passed."
