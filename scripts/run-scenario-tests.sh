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
CURRENT_PLATFORM="$(uname -s | tr '[:upper:]' '[:lower:]')"

passed=0
failed=0
skipped=0

scenario_platform_matches() {
  case "${1:-any}" in
    "" | any)
      return 0
      ;;
    macos)
      [[ "${CURRENT_PLATFORM}" == "darwin" ]]
      ;;
    *)
      [[ "${CURRENT_PLATFORM}" == "$1" ]]
      ;;
  esac
}

run_scenario() {
  local scenario_id="$1"
  local test_file="$2"
  local requires_creds="$3"
  local lane="$4"
  local platform="$5"
  local manual="$6"

  if [[ "${manual}" == "1" ]]; then
    echo "SKIP ${scenario_id} (manual lane)"
    skipped=$((skipped + 1))
    return
  fi

  if ! scenario_platform_matches "${platform}"; then
    echo "SKIP ${scenario_id} (platform ${platform})"
    skipped=$((skipped + 1))
    return
  fi

  if [[ "${RUN_ALL}" -eq 0 ]] && [[ "${lane}" != "secretless" ]]; then
    echo "SKIP ${scenario_id} (lane ${lane})"
    skipped=$((skipped + 1))
    return
  fi

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

while IFS=$'\t' read -r scenario_id test_file requires_creds lane platform manual; do
  run_scenario "${scenario_id}" "${test_file}" "${requires_creds}" "${lane}" "${platform}" "${manual}"
done < <(
  python3 - "${MANIFEST}" <<'PY'
import json, pathlib, sys
m = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
for s in m["scenarios"]:
    sid = s["id"]
    tf = s.get("test_file", "")
    rc = "1" if s.get("requires_credentials", False) else "0"
    lane = s.get("lane", "provider-e2e" if s.get("requires_credentials", False) else "secretless")
    platform = s.get("platform", "any")
    manual = "1" if s.get("manual", False) else "0"
    print(f"{sid}\t{tf}\t{rc}\t{lane}\t{platform}\t{manual}")
PY
)

echo ""
echo "Results: passed=${passed} failed=${failed} skipped=${skipped}"

if [[ "${failed}" -gt 0 ]]; then
  exit 1
fi

echo "Scenario tests passed"
