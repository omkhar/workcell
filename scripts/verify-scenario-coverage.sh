#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCENARIO_ROOT="${WORKCELL_SCENARIO_ROOT:-${ROOT_DIR}/tests/scenarios}"
MANIFEST="${WORKCELL_SCENARIO_MANIFEST:-${SCENARIO_ROOT}/manifest.json}"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  echo "verify-scenario-coverage-entrypoint-ok"
  exit 0
fi

python3 "${ROOT_DIR}/scripts/lib/scenario_manifest.py" verify-coverage "${SCENARIO_ROOT}" "${MANIFEST}"

echo "Scenario coverage verification passed"
