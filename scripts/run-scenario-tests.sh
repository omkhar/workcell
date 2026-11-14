#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/run-scenario-tests.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"
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
# A stopped scenario has this many seconds after SIGTERM to run its cleanup,
# such as stopping a VM, before its process group gets SIGKILL. A second
# signal ends the wait at once.
STOP_GRACE="${WORKCELL_SCENARIO_STOP_GRACE:-60}"

# A value of more than six digits could overflow the arithmetic below.
case "${SCENARIO_JOBS}" in
  '' | *[!0-9]* | ???????*)
    echo "WORKCELL_SCENARIO_JOBS must be a positive integer of at most six digits." >&2
    exit 1
    ;;
esac
# 10# reads a leading zero as decimal, not octal.
SCENARIO_JOBS=$((10#${SCENARIO_JOBS}))
if [[ "${SCENARIO_JOBS}" -eq 0 ]]; then
  echo "WORKCELL_SCENARIO_JOBS must be at least 1." >&2
  exit 1
fi
case "${STOP_GRACE}" in
  '' | *[!0-9]* | ???????*)
    echo "WORKCELL_SCENARIO_STOP_GRACE must be a non-negative integer number of seconds, of at most six digits." >&2
    exit 1
    ;;
esac
STOP_GRACE=$((10#${STOP_GRACE}))

# A signal only records itself: on_signal sets stop_signal, and the runner
# stops at its next check_stop, outside any trap handler. A trap handler that
# never returns would hold back the next signal on bash 3.2, so the stop could
# not be cut short; and a signal that comes while mktemp runs no longer ends
# the runner before RUN_DIR is set. A second signal sets stop_now, which ends
# the stop's wait for cleanup at once.
stop_signal=""
stop_status=0
stop_now=0
on_signal() {
  if [[ -z "${stop_signal}" ]]; then
    stop_signal="$1"
    stop_status="$2"
  else
    stop_now=1
  fi
}
trap 'on_signal TERM 143' TERM
trap 'on_signal INT 130' INT
trap 'on_signal HUP 129' HUP

# Every temporary file is in one directory, which the exit trap removes.
RUN_DIR=""
# A failed removal must not change the exit status, which says how the run
# ended.
cleanup() {
  if [[ -n "${RUN_DIR}" ]]; then
    rm -rf "${RUN_DIR}" || true
  fi
}
trap cleanup EXIT
RUN_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-scenario-run.XXXXXX")"
SCENARIO_LIST="${RUN_DIR}/scenarios.tsv"
RESULTS_DIR="${RUN_DIR}/results"
mkdir "${RESULTS_DIR}"

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

  if [[ ! -f "${output_file}" ]]; then
    echo "--- ${scenario_id} output (${status}; not captured) ---"
    echo "--- end ${scenario_id} output ---"
    return 0
  fi
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

# scenario_selected prints the SKIP line of a scenario that this run skips
# and fails for it; for a scenario to run it sets SCENARIO_PATH.
scenario_selected() {
  local scenario_id="$1"
  local test_file="$2"
  local requires_creds="$3"
  local lane="$4"
  local platform="$5"
  local validation_tier="$6"
  local manual="$7"

  if [[ "${manual}" == "1" ]]; then
    echo "SKIP ${scenario_id} (manual lane)"
    return 1
  fi

  if ! scenario_platform_matches "${platform}"; then
    echo "SKIP ${scenario_id} (platform ${platform})"
    return 1
  fi

  case "${TIER_SELECTION}" in
    repo-required)
      if [[ "${validation_tier}" != "repo-required" ]]; then
        echo "SKIP ${scenario_id} (validation tier ${validation_tier})"
        return 1
      fi
      ;;
    certification-only)
      if [[ "${validation_tier}" != "certification" ]]; then
        echo "SKIP ${scenario_id} (validation tier ${validation_tier})"
        return 1
      fi
      ;;
  esac

  if [[ "${RUN_ALL}" -eq 0 ]] && [[ "${lane}" != "secretless" ]]; then
    echo "SKIP ${scenario_id} (lane ${lane})"
    return 1
  fi

  if [[ "${RUN_ALL}" -eq 0 ]] && [[ "${requires_creds}" == "1" ]]; then
    echo "SKIP ${scenario_id} (requires credentials)"
    return 1
  fi

  if [[ -z "${test_file}" ]]; then
    echo "SKIP ${scenario_id} (no test_file)"
    return 1
  fi

  SCENARIO_PATH="${SCENARIO_ROOT}/${test_file}"

  if [[ ! -f "${SCENARIO_PATH}" ]]; then
    echo "SKIP ${scenario_id} (test file not found: ${test_file})"
    return 1
  fi
}

# Per scenario index: the manifest id, the PID of a started scenario, which is
# also its process group ID, the result (0 = pass, 1 = fail, 2 = skip) and the
# output block of a failed scenario, which the summary prints in manifest
# order. Only this shell writes them, in memory, so a stop never depends on a
# write to TMPDIR: a scenario that filled it is stopped all the same.
declare -a scenario_ids=()
declare -a scenario_pids=()
declare -a results=()
declare -a failures=()
declare -a failure_statuses=()
idx=0
manifest_pid=""

# start_group_job runs a command as a background job in a process group of its
# own, which holds the command and all of its children, so that the whole job
# can be stopped. Job control is on only for the fork: it gives the job its
# group before the fork returns, and keeps the job from ignoring SIGINT as a
# background job without job control does. The job reads nothing from the
# terminal and writes nothing to it, or a terminal set to tostop would stop
# it. target is "manifest" or a scenario index; stderr goes to error_file, or
# with stdout to output_file when error_file is empty.
start_group_job() {
  local target="$1"
  local output_file="$2"
  local error_file="$3"
  shift 3

  set -m
  if [[ -n "${error_file}" ]]; then
    "$@" </dev/null >"${output_file}" 2>"${error_file}" &
  else
    "$@" </dev/null >"${output_file}" 2>&1 &
  fi
  set +m
  if [[ "${target}" == "manifest" ]]; then
    manifest_pid="$!"
  else
    scenario_pids[target]="$!"
  fi
}

# known_status succeeds for an exit status that a wait for a job returns,
# and fails for the answer of a bash that no longer knows the job: 127, or -1
# from bash 5.
known_status() {
  [[ "$1" -ge 0 ]] && [[ "$1" -le 255 ]] && [[ "$1" -ne 127 ]]
}

# wait_job sets job_status to the exit status of a background job that has
# exited, as kill -0 shows first. The runner never waits for a job that still
# runs: a signal whose trap ran just before such a wait would not cut it
# short, so a hung job would hold back the stop for good. A trapped signal can
# still cut short a wait for an exited job, so for a status above 128 the
# runner stops if it got a signal. A bash that no longer knows the job answers
# -1 (bash 5) or 127.
job_status=0
wait_job() {
  job_status=0
  wait "$1" 2>/dev/null || job_status="$?"
  if [[ "${job_status}" -gt 128 ]]; then
    check_stop
  fi
}

# wait_for_exit polls until a background job has exited, and stops the runner
# if it gets a signal meanwhile.
wait_for_exit() {
  while kill -0 "$1" 2>/dev/null; do
    # A Ctrl-C reaches this sleep too.
    sleep 0.1 || true
    check_stop
  done
}

start_scenario() {
  start_group_job "$1" "${RESULTS_DIR}/$1.output" "" bash "$2"
}

# reap_finished records each running scenario that has exited, and keeps the
# others in running_idx. This shell reaps a job that exits, so its PID stops
# answering.
reap_finished() {
  local i=""
  local -a still_running=()

  for i in ${running_idx[@]+"${running_idx[@]}"}; do
    if kill -0 "${scenario_pids[i]}" 2>/dev/null; then
      still_running+=("${i}")
      continue
    fi
    wait_job "${scenario_pids[i]}"
    finish_scenario "${i}" "${job_status}"
  done
  running_idx=(${still_running[@]+"${still_running[@]}"})
}

# finish_scenario records a scenario that exited with status. A failed
# scenario's output block is built before it counts as failed, and kept only
# when it is whole: a Ctrl-C reaches the command substitution and the tail in
# it too. print_failure builds a missing block again.
finish_scenario() {
  local i="$1"
  local status="$2"
  local block=""

  if [[ "${status}" -eq 0 ]]; then
    results[i]=0
    echo "PASS ${scenario_ids[i]}"
    return 0
  fi
  failure_statuses[i]="exit ${status}"
  if [[ "${status}" -eq 127 ]]; then
    failure_statuses[i]="exit 127, or an exit status the runner lost"
  elif ! known_status "${status}"; then
    failure_statuses[i]="exit status lost by the runner"
  fi
  block="$(print_failed_output "${scenario_ids[i]}" "${failure_statuses[i]}" "${RESULTS_DIR}/${i}.output")" || true
  if [[ "${block}" == *"--- end ${scenario_ids[i]} output ---" ]]; then
    failures[i]="${block}"
  fi
  results[i]=1
  echo "FAIL ${scenario_ids[i]}"
}

# print_failure prints the output block of failed scenario i.
print_failure() {
  local i="$1"

  echo ""
  if [[ -n "${failures[i]:-}" ]]; then
    printf '%s\n' "${failures[i]}"
  else
    print_failed_output "${scenario_ids[i]}" "${failure_statuses[i]:-failed}" "${RESULTS_DIR}/${i}.output"
  fi
}

check_stop() {
  if [[ -n "${stop_signal}" ]]; then
    stop_runner
  fi
}

# stop_runner stops a runner that got a signal, as from a CI timeout or a
# Ctrl-C. It sends SIGTERM to the process group of each running job and waits
# up to STOP_GRACE seconds for each job to exit, so that a scenario's cleanup
# trap can finish; as each job exits, it sends SIGKILL to what is left of its
# group. A second signal, as from a CI runner whose own grace ends, cuts the
# wait short. Then SIGKILL stops each group still waited for, also a job that
# ignores SIGTERM or that got it between its fork and the reset of its signal
# handlers and so lost it. It prints the output of each scenario that failed
# and of each that had not finished, and exits.
stop_runner() {
  local i=""
  local pid=""
  local tenths=0
  local status_i=0
  local told=0
  local -a stop_pids=()
  local -a waiting_pids=()
  local -a still_waiting=()

  set +e +u +m
  if [[ -n "${manifest_pid}" ]]; then
    stop_pids+=("${manifest_pid}")
  fi
  for ((i = 1; i <= idx; i++)); do
    pid="${scenario_pids[i]:-}"
    if [[ -z "${pid}" ]] || [[ -n "${results[i]:-}" ]]; then
      continue
    fi
    # This shell reaps a job that exits, so the PID of a scenario that
    # finished before the signal no longer answers: it is recorded, not
    # stopped. When the signal ended a wait that had just taken the
    # scenario's status, that status is lost (result 3), and the scenario
    # gets no line.
    if ! kill -0 "${pid}" 2>/dev/null; then
      status_i=0
      wait "${pid}" 2>/dev/null || status_i="$?"
      if known_status "${status_i}"; then
        finish_scenario "${i}" "${status_i}"
      else
        results[i]=3
      fi
      continue
    fi
    stop_pids+=("${pid}")
  done
  for pid in ${stop_pids[@]+"${stop_pids[@]}"}; do
    kill -TERM -- "-${pid}" 2>/dev/null
  done
  waiting_pids=(${stop_pids[@]+"${stop_pids[@]}"})
  while [[ "${#waiting_pids[@]}" -gt 0 ]]; do
    still_waiting=()
    for pid in "${waiting_pids[@]}"; do
      if kill -0 "${pid}" 2>/dev/null; then
        still_waiting+=("${pid}")
      else
        kill -KILL -- "-${pid}" 2>/dev/null
      fi
    done
    waiting_pids=(${still_waiting[@]+"${still_waiting[@]}"})
    if [[ "${#waiting_pids[@]}" -eq 0 ]] || [[ "${stop_now}" -eq 1 ]] || [[ "${tenths}" -ge $((STOP_GRACE * 10)) ]]; then
      break
    fi
    if [[ "${told}" -eq 0 ]]; then
      echo "Waiting up to ${STOP_GRACE}s for scenario cleanup; send the signal again to kill now." >&2
      told=1
    fi
    sleep 0.1
    tenths=$((tenths + 1))
  done
  # The commands that print the blocks below inherit the ignored signals, so
  # a later signal cannot cut a block short.
  trap '' TERM INT HUP
  for pid in ${waiting_pids[@]+"${waiting_pids[@]}"}; do
    kill -KILL -- "-${pid}" 2>/dev/null
  done
  for pid in ${stop_pids[@]+"${stop_pids[@]}"}; do
    wait "${pid}" 2>/dev/null
  done
  for ((i = 1; i <= idx; i++)); do
    if [[ "${results[i]:-}" == "1" ]]; then
      print_failure "${i}"
    fi
  done
  for ((i = 1; i <= idx; i++)); do
    if [[ -n "${scenario_pids[i]:-}" ]] && [[ -z "${results[i]:-}" ]]; then
      echo ""
      echo "STOPPED ${scenario_ids[i]}"
      print_failed_output "${scenario_ids[i]}" "stopped by SIG${stop_signal}" "${RESULTS_DIR}/${i}.output"
    fi
  done
  exit "${stop_status}"
}

# The manifest is read by a background job that the runner polls, so that a
# signal stops it at once; bash runs a trap only after a foreground child
# exits.
# go run keeps its build directory under GOTMPDIR, and leaves it behind when a
# signal kills it, so the manifest's is in RUN_DIR, which the exit trap
# removes.
mkdir "${RUN_DIR}/gotmp"
check_stop
start_group_job manifest "${SCENARIO_LIST}" "${RESULTS_DIR}/manifest.err" \
  env GOTMPDIR="${RUN_DIR}/gotmp" "${ROOT_DIR}/scripts/lib/scenario_manifest" list-tsv "${MANIFEST}"
wait_for_exit "${manifest_pid}"
wait_job "${manifest_pid}"
manifest_status="${job_status}"
manifest_pid=""
if [[ -f "${RESULTS_DIR}/manifest.err" ]]; then
  cat "${RESULTS_DIR}/manifest.err" >&2 || true
fi
if [[ "${manifest_status}" -ne 0 ]]; then
  exit 1
fi

declare -a running_idx=()
while IFS=$'\t' read -r scenario_id test_file requires_creds lane platform validation_tier manual; do
  check_stop
  idx=$((idx + 1))
  scenario_ids[idx]="${scenario_id}"
  if ! scenario_selected "${scenario_id}" "${test_file}" "${requires_creds}" "${lane}" "${platform}" "${validation_tier}" "${manual}"; then
    results[idx]=2
    continue
  fi
  check_stop
  start_scenario "${idx}" "${SCENARIO_PATH}"
  running_idx+=("${idx}")
  # Serial is the default: repo-required validation stays serial because
  # several scenarios share host-side state. WORKCELL_SCENARIO_JOBS above 1 is
  # an explicit opt-in for local debugging. Either way the runner polls its
  # jobs and records each scenario as it finishes, so a signal stops it
  # within a poll.
  while [[ "${#running_idx[@]}" -ge "${SCENARIO_JOBS}" ]]; do
    reap_finished
    if [[ "${#running_idx[@]}" -ge "${SCENARIO_JOBS}" ]]; then
      sleep 0.1 || true
      check_stop
    fi
  done
done <"${SCENARIO_LIST}"

while [[ "${#running_idx[@]}" -gt 0 ]]; do
  reap_finished
  if [[ "${#running_idx[@]}" -gt 0 ]]; then
    sleep 0.1 || true
    check_stop
  fi
done
# From here a signal just ends the run, through the exit trap that removes
# RUN_DIR; one that came before is acted on now.
trap 'exit 143' TERM
trap 'exit 130' INT
trap 'exit 129' HUP
check_stop

passed=0
failed=0
skipped=0
for ((i = 1; i <= idx; i++)); do
  case "${results[i]:-}" in
    0) passed=$((passed + 1)) ;;
    2) skipped=$((skipped + 1)) ;;
    *) failed=$((failed + 1)) ;;
  esac
  if [[ "${results[i]:-}" == "1" ]]; then
    print_failure "${i}"
  fi
done

echo ""
echo "Results: passed=${passed} failed=${failed} skipped=${skipped}"

if [[ "${failed}" -gt 0 ]]; then
  exit 1
fi

echo "Scenario tests passed"
