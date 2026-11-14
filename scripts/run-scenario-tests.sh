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
# A failed scenario's captured output is printed up to this many final lines.
readonly FAILED_OUTPUT_LINES=200
SCENARIO_JOBS="${WORKCELL_SCENARIO_JOBS:-1}"
SCENARIO_LIST="$(mktemp "${TMPDIR:-/tmp}/workcell-scenarios.XXXXXX")"
RESULTS_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-scenario-results.XXXXXX")"

case "${SCENARIO_JOBS}" in
  '' | *[!0-9]*)
    echo "WORKCELL_SCENARIO_JOBS must be a positive integer." >&2
    exit 1
    ;;
  0)
    echo "WORKCELL_SCENARIO_JOBS must be at least 1." >&2
    exit 1
    ;;
esac

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

# print_failed_output prints the last FAILED_OUTPUT_LINES lines of a failed
# scenario's captured stdout and stderr between two marker lines. The status
# is "exit N" or the signal that stopped the runner.
print_failed_output() {
  local scenario_id="$1"
  local status="$2"
  local output_file="$3"
  local total_lines=""
  local unterminated=0

  # The command substitution drops a final newline, so only another final
  # byte is left: an unterminated last line that wc -l does not count.
  if [[ -n "$(tail -c 1 "${output_file}")" ]]; then
    unterminated=1
  fi
  total_lines="$(wc -l <"${output_file}")"
  total_lines=$((${total_lines//[[:space:]]/} + unterminated))
  if [[ "${total_lines}" -gt "${FAILED_OUTPUT_LINES}" ]]; then
    echo "--- ${scenario_id} output (${status}; last ${FAILED_OUTPUT_LINES} of ${total_lines} lines) ---"
  else
    echo "--- ${scenario_id} output (${status}; ${total_lines} lines) ---"
  fi
  tail -n "${FAILED_OUTPUT_LINES}" "${output_file}"
  if [[ "${unterminated}" -eq 1 ]]; then
    echo ""
  fi
  echo "--- end ${scenario_id} output ---"
}

# Returns: 0 = pass, 1 = fail, 2 = skip. A failed scenario's output block goes
# to failure_file, which the summary prints in manifest order.
run_scenario() {
  local scenario_id="$1"
  local test_file="$2"
  local requires_creds="$3"
  local lane="$4"
  local platform="$5"
  local validation_tier="$6"
  local manual="$7"
  local output_file="$8"
  local failure_file="$9"

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

  local scenario_exit=0
  local scenario_pid=""
  # The scenario runs in the background in its own process group, so that a
  # runner stopped by a signal prints its output and stops the scenario with
  # all of its children. Job control is on only for the launch: a background
  # job started without it would ignore SIGINT.
  printf '%s\n' "${scenario_id}" >"${output_file%.output}.id"
  set -m
  bash "${full_test_path}" </dev/null >"${output_file}" 2>&1 &
  scenario_pid="$!"
  set +m
  printf '%d\n' "${scenario_pid}" >"${output_file%.output}.pid"
  wait "${scenario_pid}" || scenario_exit="$?"
  if [[ "${scenario_exit}" -eq 0 ]]; then
    echo "PASS ${scenario_id}"
    return 0
  fi
  echo "FAIL ${scenario_id}"
  print_failed_output "${scenario_id}" "exit ${scenario_exit}" "${output_file}" >"${failure_file}"
  return 1
}

if ! "${ROOT_DIR}/scripts/lib/scenario_manifest" list-tsv "${MANIFEST}" >"${SCENARIO_LIST}"; then
  exit 1
fi

idx=0
run_scenario_record() {
  local result_file="$1"
  shift

  (
    exit_code=0
    run_scenario "$@" "${result_file%.exit}.output" "${result_file%.exit}.failure" || exit_code="$?"
    printf '%d\n' "${exit_code}" >"${result_file}"
  )
}

declare -a pids=()

# stop_on_signal handles a runner stopped by a signal, as a CI timeout does.
# It prints the captured output of each scenario that has not finished, and
# then stops those scenarios and their record subshells.
stop_on_signal() {
  local signal="$1"
  local status="$2"
  local i=""
  local prefix=""
  local pid=""

  trap - TERM INT HUP
  for i in $(seq 1 "${idx}"); do
    prefix="${RESULTS_DIR}/${i}"
    [[ -f "${prefix}.output" && -f "${prefix}.id" && ! -f "${prefix}.exit" ]] || continue
    echo ""
    echo "STOPPED $(<"${prefix}.id")"
    print_failed_output "$(<"${prefix}.id")" "stopped by SIG${signal}" "${prefix}.output"
    if [[ -f "${prefix}.pid" ]]; then
      pid="$(<"${prefix}.pid")"
      # The scenario PID is also its process group ID.
      [[ "${pid}" =~ ^[1-9][0-9]*$ ]] && kill -TERM -- "-${pid}" 2>/dev/null || true
    fi
  done
  for pid in ${pids[@]+"${pids[@]}"}; do
    kill -TERM "${pid}" 2>/dev/null || true
  done
  exit "${status}"
}
trap 'stop_on_signal TERM 143' TERM
trap 'stop_on_signal INT 130' INT
trap 'stop_on_signal HUP 129' HUP

if [[ "${SCENARIO_JOBS}" -eq 1 ]]; then
  while IFS=$'\t' read -r scenario_id test_file requires_creds lane platform validation_tier manual; do
    idx=$((idx + 1))
    result_file="${RESULTS_DIR}/${idx}.exit"
    # A background record and a wait let a signal trap run at once; bash
    # runs a trap only after a foreground child exits.
    run_scenario_record "${result_file}" \
      "${scenario_id}" "${test_file}" "${requires_creds}" "${lane}" "${platform}" "${validation_tier}" "${manual}" &
    pids=("$!")
    wait "${pids[0]}" || true
    pids=()
  done <"${SCENARIO_LIST}"
else
  # Explicit opt-in parallel mode for local debugging; repo-required validation
  # stays serial by default because several scenarios share host-side state.
  running_jobs=0
  while IFS=$'\t' read -r scenario_id test_file requires_creds lane platform validation_tier manual; do
    idx=$((idx + 1))
    result_file="${RESULTS_DIR}/${idx}.exit"
    run_scenario_record "${result_file}" \
      "${scenario_id}" "${test_file}" "${requires_creds}" "${lane}" "${platform}" "${validation_tier}" "${manual}" &
    pids+=($!)
    running_jobs=$((running_jobs + 1))
    if [[ "${running_jobs}" -ge "${SCENARIO_JOBS}" ]]; then
      wait "${pids[0]}" || true
      pids=("${pids[@]:1}")
      running_jobs=$((running_jobs - 1))
    fi
  done <"${SCENARIO_LIST}"

  for pid in ${pids[@]+"${pids[@]}"}; do
    wait "${pid}" || true
  done
fi
trap - TERM INT HUP

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
  if [[ -f "${RESULTS_DIR}/${i}.failure" ]]; then
    echo ""
    cat "${RESULTS_DIR}/${i}.failure"
  fi
done

echo ""
echo "Results: passed=${passed} failed=${failed} skipped=${skipped}"

if [[ "${failed}" -gt 0 ]]; then
  exit 1
fi

echo "Scenario tests passed"
