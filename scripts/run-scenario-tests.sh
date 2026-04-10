#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCENARIO_ROOT="${WORKCELL_SCENARIO_ROOT:-${ROOT_DIR}/tests/scenarios}"
MANIFEST="${WORKCELL_SCENARIO_MANIFEST:-${SCENARIO_ROOT}/manifest.json}"

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

CURRENT_PLATFORM="$(uname -s | tr '[:upper:]' '[:lower:]')"

passed=0
failed=0
skipped=0
SCENARIO_LIST="$(mktemp "${TMPDIR:-/tmp}/workcell-scenarios.XXXXXX")"

cleanup() {
  rm -f "${SCENARIO_LIST}"
}
trap cleanup EXIT

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

  local full_test_path="${SCENARIO_ROOT}/${test_file}"

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

if ! "${ROOT_DIR}/scripts/lib/scenario_manifest" list-tsv "${MANIFEST}" >"${SCENARIO_LIST}"; then
  exit 1
fi

while IFS=$'\t' read -r scenario_id test_file requires_creds lane platform manual; do
  run_scenario "${scenario_id}" "${test_file}" "${requires_creds}" "${lane}" "${platform}" "${manual}"
done <"${SCENARIO_LIST}"

echo ""
echo "Results: passed=${passed} failed=${failed} skipped=${skipped}"

if [[ "${failed}" -gt 0 ]]; then
  exit 1
fi

echo "Scenario tests passed"
