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
TIER_SELECTION="repo-required"
TIER_SELECTION_EXPLICIT=0
while [[ "$#" -gt 0 ]]; do
  case "$1" in
    --all)
      RUN_ALL=1
      ;;
    --secretless-only)
      RUN_ALL=0
      ;;
    --repo-required)
      TIER_SELECTION="repo-required"
      TIER_SELECTION_EXPLICIT=1
      ;;
    --include-certification)
      TIER_SELECTION="include-certification"
      TIER_SELECTION_EXPLICIT=1
      ;;
    --certification-only)
      TIER_SELECTION="certification-only"
      TIER_SELECTION_EXPLICIT=1
      ;;
    *)
      echo "Usage: run-scenario-tests.sh [--secretless-only|--all] [--repo-required|--include-certification|--certification-only]" >&2
      exit 1
      ;;
  esac
  shift
done

if [[ "${RUN_ALL}" -eq 1 ]] && [[ "${TIER_SELECTION_EXPLICIT}" -eq 0 ]]; then
  TIER_SELECTION="include-certification"
fi

CURRENT_PLATFORM="$(uname -s | tr '[:upper:]' '[:lower:]')"
SCENARIO_LIST="$(mktemp "${TMPDIR:-/tmp}/workcell-scenarios.XXXXXX")"
RESULTS_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-scenario-results.XXXXXX")"

cleanup() {
  rm -f "${SCENARIO_LIST}"
  rm -rf "${RESULTS_DIR}"
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

# Returns: 0 = pass, 1 = fail, 2 = skip
run_scenario() {
  local scenario_id="$1"
  local test_file="$2"
  local requires_creds="$3"
  local lane="$4"
  local platform="$5"
  local validation_tier="$6"
  local manual="$7"

  if [[ "${manual}" == "1" ]]; then
    echo "SKIP ${scenario_id} (manual lane)"
    return 2
  fi

  if ! scenario_platform_matches "${platform}"; then
    echo "SKIP ${scenario_id} (platform ${platform})"
    return 2
  fi

  case "${TIER_SELECTION}" in
    repo-required)
      if [[ "${validation_tier}" != "repo-required" ]]; then
        echo "SKIP ${scenario_id} (validation tier ${validation_tier})"
        return 2
      fi
      ;;
    certification-only)
      if [[ "${validation_tier}" != "certification" ]]; then
        echo "SKIP ${scenario_id} (validation tier ${validation_tier})"
        return 2
      fi
      ;;
  esac

  if [[ "${RUN_ALL}" -eq 0 ]] && [[ "${lane}" != "secretless" ]]; then
    echo "SKIP ${scenario_id} (lane ${lane})"
    return 2
  fi

  if [[ "${RUN_ALL}" -eq 0 ]] && [[ "${requires_creds}" == "1" ]]; then
    echo "SKIP ${scenario_id} (requires credentials)"
    return 2
  fi

  if [[ -z "${test_file}" ]]; then
    echo "SKIP ${scenario_id} (no test_file)"
    return 2
  fi

  local full_test_path="${SCENARIO_ROOT}/${test_file}"

  if [[ ! -f "${full_test_path}" ]]; then
    echo "SKIP ${scenario_id} (test file not found: ${test_file})"
    return 2
  fi

  if bash "${full_test_path}"; then
    echo "PASS ${scenario_id}"
    return 0
  else
    echo "FAIL ${scenario_id}"
    return 1
  fi
}

if ! "${ROOT_DIR}/scripts/lib/scenario_manifest" list-tsv "${MANIFEST}" >"${SCENARIO_LIST}"; then
  exit 1
fi

# Run all scenarios in parallel; each writes its exit code to a per-index file.
idx=0
declare -a pids=()

while IFS=$'\t' read -r scenario_id test_file requires_creds lane platform validation_tier manual; do
  idx=$((idx + 1))
  result_file="${RESULTS_DIR}/${idx}.exit"
  (
    exit_code=0
    run_scenario "${scenario_id}" "${test_file}" "${requires_creds}" "${lane}" "${platform}" "${validation_tier}" "${manual}" || exit_code="$?"
    printf '%d\n' "${exit_code}" >"${result_file}"
  ) &
  pids+=($!)
done <"${SCENARIO_LIST}"

# Wait for all background jobs to complete
for pid in "${pids[@]}"; do
  wait "${pid}" || true
done

# Aggregate results from per-scenario exit files.
# Exit codes: 0 = pass, 1 = fail, 2 = skip
passed=0
failed=0
skipped=0
for i in $(seq 1 "${idx}"); do
  result_file="${RESULTS_DIR}/${i}.exit"
  if [[ -f "${result_file}" ]]; then
    code=$(<"${result_file}")
    case "${code}" in
      0) passed=$((passed + 1)) ;;
      2) skipped=$((skipped + 1)) ;;
      *) failed=$((failed + 1)) ;;
    esac
  else
    failed=$((failed + 1))
  fi
done

echo ""
echo "Results: passed=${passed} failed=${failed} skipped=${skipped}"

if [[ "${failed}" -gt 0 ]]; then
  exit 1
fi

echo "Scenario tests passed"
