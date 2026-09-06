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

# workcell-citools check-workflows runs actionlint and the zizmor scan
# (persona auditor, .github/zizmor.yml, all workflow files) itself, so no
# direct actionlint or zizmor call runs here.
run_go_in_repo "${ROOT_DIR}" run ./cmd/workcell-citools check-workflows "${ROOT_DIR}" "${POLICY_PATH}"
"${ROOT_DIR}/scripts/verify-workflow-lanes.sh"
run_go_in_repo "${ROOT_DIR}" run ./cmd/workcell-citools check-retention-policy "${ROOT_DIR}" "${ROOT_DIR}/policy/retention-policy.json"

echo "Workcell workflow checks passed."
