#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
POLICY_PATH="${ROOT_DIR}/policy/github-hosted-controls.toml"
# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/go-run-env.sh"

# check-workflows requires actionlint and zizmor, and runs both itself
# (zizmor with persona auditor, .github/zizmor.yml, every workflow file).
run_go_in_repo "${ROOT_DIR}" run ./cmd/workcell-citools check-workflows "${ROOT_DIR}" "${POLICY_PATH}"
"${ROOT_DIR}/scripts/verify-workflow-lanes.sh"
run_go_in_repo "${ROOT_DIR}" run ./cmd/workcell-citools check-retention-policy "${ROOT_DIR}" "${ROOT_DIR}/policy/retention-policy.json"

echo "Workcell workflow checks passed."
