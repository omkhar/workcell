#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  echo "run-scenario-tests-entrypoint-ok"
  exit 0
fi

RUN_ALL=0
case "${1:-}" in
  --all) RUN_ALL=1 ;;
  --secretless-only | "") ;;
  *)
    echo "Usage: run-scenario-tests.sh [--secretless-only|--all]" >&2
    exit 1
    ;;
esac

MANIFEST="${ROOT_DIR}/tests/scenarios/manifest.json"

passed=0
failed=0
skipped=0

run_scenario() {
  local scenario_id="$1"
  local test_file="$2"
  local requires_creds="$3"

  if [[ "${RUN_ALL}" -eq 0 ]] && [[ "${requires_creds}" == "1" ]]; then
    echo "SKIP ${scenario_id} (requires credentials)"
    skipped=$((skipped + 1))
    return
  fi

  if [[ -z "${test_file}" ]]; then
    echo "SKIP ${scenario_id} (no test_file)"
    skipped=$((skipped + 1))
    return
  fi

  local full_test_path="${ROOT_DIR}/tests/scenarios/${test_file}"

  if [[ ! -f "${full_test_path}" ]]; then
    echo "SKIP ${scenario_id} (test file not found: ${test_file})"
    skipped=$((skipped + 1))
    return
  fi

  if bash "${full_test_path}"; then
    echo "PASS ${scenario_id}"
    passed=$((passed + 1))
  else
    echo "FAIL ${scenario_id}"
    failed=$((failed + 1))
  fi
}

while IFS=$'\t' read -r scenario_id test_file requires_creds; do
  run_scenario "${scenario_id}" "${test_file}" "${requires_creds}"
done < <(
  python3 - "${MANIFEST}" <<'PY'
import json, pathlib, sys
m = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
for s in m["scenarios"]:
    sid = s["id"]
    tf = s.get("test_file", "")
    rc = "1" if s.get("requires_credentials", False) else "0"
    print(f"{sid}\t{tf}\t{rc}")
PY
)

echo ""
echo "Results: passed=${passed} failed=${failed} skipped=${skipped}"

if [[ "${failed}" -gt 0 ]]; then
  exit 1
fi

echo "Scenario tests passed"
