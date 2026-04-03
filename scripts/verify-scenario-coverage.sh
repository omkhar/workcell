#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCENARIO_ROOT="${WORKCELL_SCENARIO_ROOT:-${ROOT_DIR}/tests/scenarios}"
MANIFEST="${WORKCELL_SCENARIO_MANIFEST:-${SCENARIO_ROOT}/manifest.json}"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  echo "verify-scenario-coverage-entrypoint-ok"
  exit 0
fi

"${ROOT_DIR}/scripts/lib/scenario_manifest" verify-coverage "${SCENARIO_ROOT}" "${MANIFEST}"

echo "Scenario coverage verification passed"
