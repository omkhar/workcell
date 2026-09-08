# shellcheck shell=bash
# Darwin live-Colima verification lane. This file is sourced by
# scripts/verify-invariants.sh only; it is not a standalone entrypoint. It
# inherits the parent's sanitized environment, ERR and EXIT traps, helper
# functions, and PID (the Colima profile names embed the parent's $$, and the
# parent's cleanup trap reads the LIVE_* / *_PROFILE_NAME / DETACHED_*
# variables this lane sets).
#
# VERIFY_INVARIANTS_EXPECTED_FAILURE is set here but read only by the parent's
# ERR trap, so a standalone scan of this fragment flags it as unused.
# shellcheck disable=SC2034

detached_session_monitor_command_matches() {
  local monitor_pid="$1"
  local state_file="$2"
  local monitor_command=""

  monitor_command="$(ps -o command= -p "${monitor_pid}" 2>/dev/null | head -n1 || true)"
  [[ "${monitor_command}" == *"${ROOT_DIR}/scripts/workcell"*' session monitor --state-file '*"${state_file}" ]]
}

wait_for_detached_session_monitor_exit() {
  local monitor_pid="$1"
  local state_file="$2"
  local attempts="${3:-10}"
  local interval="${4:-1}"
  local attempt=0

  for ((attempt = 0; attempt < attempts; attempt++)); do
    detached_session_monitor_command_matches "${monitor_pid}" "${state_file}" || return 0
    sleep "${interval}"
  done
  ! detached_session_monitor_command_matches "${monitor_pid}" "${state_file}"
}

if [[ "$(uname -s)" == "Darwin" ]] &&
  host_tool_exists /opt/homebrew/bin/colima /usr/local/bin/colima &&
  host_tool_exists /opt/homebrew/bin/docker /usr/local/bin/docker /Applications/Docker.app/Contents/Resources/bin/docker; then
  if [[ "$(free_bytes_for_path "${ROOT_DIR}")" -lt $((5 * 1024 * 1024 * 1024)) ]]; then
    echo "Cannot run live-debug audit verification on Darwin: host filesystem has less than 5 GiB free." >&2
    exit 1
  else
    LIVE_DEBUG_PROFILE_NAME="workcell-live-debug-$$"
    LIVE_DETACHED_PROFILE_NAME="wcl-live-det-$$"
    delete_verify_colima_profile "${LIVE_DEBUG_PROFILE_NAME}"
    delete_verify_colima_profile "${LIVE_DETACHED_PROFILE_NAME}"
    LIVE_DEBUG_LOG="${BARRIER_VERIFY_ROOT}/debug/live-debug.log"
    LIVE_DEBUG_PREPARE_OUT="${BARRIER_VERIFY_ROOT}/debug/live-debug.prepare.out"
    LIVE_DEBUG_REFRESH_OUT="${BARRIER_VERIFY_ROOT}/debug/live-debug.refresh.out"
    LIVE_DEBUG_FILE_TRACE_OUT="${BARRIER_VERIFY_ROOT}/debug/live-debug.file-trace.out"
    LIVE_DEBUG_LOGS_FILE_TRACE_OUT="${BARRIER_VERIFY_ROOT}/debug/live-debug.logs-file-trace.out"
    LIVE_DEBUG_INSPECT_FILE_TRACE_OUT="${BARRIER_VERIFY_ROOT}/debug/live-debug.inspect-file-trace.out"
    LIVE_DEBUG_LOGS_DEBUG_OUT="${BARRIER_VERIFY_ROOT}/debug/live-debug.logs-debug.out"
    AUDIT_SESSION_LOG="${BARRIER_VERIFY_ROOT}/debug/live-debug.audit-session.log"
    if ! run_workcell_verify \
      --agent codex \
      --prepare-only \
      --rebuild \
      --workspace "${ROOT_DIR}" \
      --vm-memory 6 \
      --vm-disk 80 \
      --injection-policy "${AUTH_STATUS_ROOT}/policy.toml" \
      --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" \
      --debug-log "${LIVE_DEBUG_LOG}" >"${LIVE_DEBUG_PREPARE_OUT}" 2>&1; then
      echo "Expected audit verification prepare run to seed a managed image" >&2
      cat "${LIVE_DEBUG_PREPARE_OUT}" >&2
      exit 1
    fi
    assert_output_matches_regex 'Starting managed Colima profile|starting colima' "${LIVE_DEBUG_LOG}" \
      "Expected audit verification prepare run debug log to capture managed Colima startup"
    assert_output_matches_regex 'Preparing the runtime image for profile|runtime-build|runtime-builder' "${LIVE_DEBUG_LOG}" \
      "Expected audit verification prepare run debug log to capture runtime image preparation"
    if ! run_workcell_verify \
      --agent codex \
      --no-default-injection-policy \
      --workspace "${ROOT_DIR}" \
      --vm-memory 6 \
      --vm-disk 80 \
      --no-default-injection-policy \
      --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" \
      --file-trace-log "${FILE_TRACE_CAPTURE}" \
      --agent-arg --version >"${LIVE_DEBUG_FILE_TRACE_OUT}" 2>&1; then
      echo "Expected launched session with --file-trace-log to succeed" >&2
      cat "${LIVE_DEBUG_FILE_TRACE_OUT}" >&2
      exit 1
    fi
    test -s "${FILE_TRACE_CAPTURE}"
    grep -q 'event=provider-launch' "${FILE_TRACE_CAPTURE}"
    grep -q 'event=watch-start' "${FILE_TRACE_CAPTURE}"
    grep -q 'event=provider-exit' "${FILE_TRACE_CAPTURE}"
    if ! run_workcell_verify \
      --logs file-trace \
      --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" >"${LIVE_DEBUG_LOGS_FILE_TRACE_OUT}" 2>&1; then
      echo "Expected --logs file-trace to print the latest retained file trace log" >&2
      exit 1
    fi
    grep -q 'event=provider-launch' "${LIVE_DEBUG_LOGS_FILE_TRACE_OUT}"
    if ! run_workcell_verify \
      --inspect \
      --agent codex \
      --no-default-injection-policy \
      --workspace "${ROOT_DIR}" \
      --vm-memory 6 \
      --vm-disk 80 \
      --no-default-injection-policy \
      --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" >"${LIVE_DEBUG_INSPECT_FILE_TRACE_OUT}" 2>&1; then
      echo "Expected --inspect to surface the latest retained file trace log" >&2
      cat "${LIVE_DEBUG_INSPECT_FILE_TRACE_OUT}" >&2
      exit 1
    fi
    grep -q "latest_file_trace_log=${FILE_TRACE_CAPTURE}" "${LIVE_DEBUG_INSPECT_FILE_TRACE_OUT}"
    if ! run_workcell_verify \
      --logs debug \
      --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" >"${LIVE_DEBUG_LOGS_DEBUG_OUT}" 2>&1; then
      echo "Expected successful prepare run to persist the latest debug-log pointer" >&2
      exit 1
    fi
    assert_output_matches_regex 'Starting managed Colima profile|starting colima' "${LIVE_DEBUG_LOGS_DEBUG_OUT}" \
      "Expected workcell logs debug to print the retained managed Colima startup log"
    for agent in codex claude gemini; do
      if ! run_workcell_verify GIT_PAGER=cat PAGER=cat \
        --agent "${agent}" \
        --mode development \
        --workspace "${ROOT_DIR}" \
        --vm-memory 6 \
        --vm-disk 80 \
        --no-default-injection-policy \
        --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" \
        -- bash -lc 'git -c safe.directory=/workspace status --short >/tmp/workcell-development-shell.out && printf "WORKCELL_DEVELOPMENT_SHELL_OK\n"' \
        >"${BARRIER_VERIFY_ROOT}/debug/live-development-shell-${agent}.out" 2>&1; then
        echo "Expected managed development shell command to succeed for ${agent} even with inherited host pager env" >&2
        cat "${BARRIER_VERIFY_ROOT}/debug/live-development-shell-${agent}.out" >&2
        exit 1
      fi
      grep -q '^WORKCELL_DEVELOPMENT_SHELL_OK$' "${BARRIER_VERIFY_ROOT}/debug/live-development-shell-${agent}.out"
      if [[ "${agent}" == "codex" ]] &&
        grep -Eq 'Preparing the runtime image for profile|runtime-build|429 Too Many Requests' \
          "${BARRIER_VERIFY_ROOT}/debug/live-development-shell-${agent}.out" &&
        ! grep -q 'Workcell timed out waiting for managed Colima profile' \
          "${BARRIER_VERIFY_ROOT}/debug/live-development-shell-${agent}.out"; then
        echo "Expected refreshed managed development shell to reuse the prepared runtime image without rebuilding" >&2
        cat "${BARRIER_VERIFY_ROOT}/debug/live-development-shell-${agent}.out" >&2
        exit 1
      fi
    done
    LIVE_DEBUG_COLIMA_BIN="/usr/local/bin/colima"
    if [[ -x /opt/homebrew/bin/colima ]]; then
      LIVE_DEBUG_COLIMA_BIN="/opt/homebrew/bin/colima"
    fi
    if ! go_verify_hostutil helper run-host-colima-with-timeout 60 \
      "--colima-bin=${LIVE_DEBUG_COLIMA_BIN}" \
      "--real-home=${REAL_HOME}" \
      "--colima-home=${REAL_HOME}/.colima" \
      -- start --profile "${LIVE_DEBUG_PROFILE_NAME}" >/dev/null; then
      echo "Expected managed profile to start before exact reaper certification" >&2
      exit 1
    fi
    LIVE_DEBUG_OLD_PROFILE_PIDS=""
    for _ in {1..20}; do
      LIVE_DEBUG_OLD_PROFILE_PIDS="$(
        ps -axo pid=,command= | go_verify_hostutil helper colima-profile-process-pids "${LIVE_DEBUG_PROFILE_NAME}"
      )"
      [[ -z "${LIVE_DEBUG_OLD_PROFILE_PIDS}" ]] || break
      sleep 0.25
    done
    if [[ -z "${LIVE_DEBUG_OLD_PROFILE_PIDS}" ]]; then
      echo "Expected a managed profile process before exact reaper certification" >&2
      exit 1
    fi
    LIVE_DEBUG_PROFILE_PROCESS_EVIDENCE="${BARRIER_VERIFY_ROOT}/debug/live-debug.pre-refresh-processes.out"
    : >"${LIVE_DEBUG_PROFILE_PROCESS_EVIDENCE}"
    while IFS= read -r old_profile_pid; do
      if ! ps -p "${old_profile_pid}" -o pid=,command= >>"${LIVE_DEBUG_PROFILE_PROCESS_EVIDENCE}"; then
        echo "Expected managed profile process ${old_profile_pid} to remain observable before exact reaper certification" >&2
        exit 1
      fi
    done <<<"${LIVE_DEBUG_OLD_PROFILE_PIDS}"
    sed 's/^/exact_reaper_pre_refresh_process=/' "${LIVE_DEBUG_PROFILE_PROCESS_EVIDENCE}"
    if ! run_workcell_verify GIT_PAGER=cat PAGER=cat \
      --agent codex \
      --mode development \
      --workspace "${ROOT_DIR}" \
      --vm-memory 7 \
      --vm-disk 80 \
      --no-default-injection-policy \
      --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" \
      -- bash -lc 'git -c safe.directory=/workspace status --short >/tmp/workcell-development-shell-refresh.out && printf "WORKCELL_DEVELOPMENT_REFRESH_OK\n"' \
      >"${LIVE_DEBUG_REFRESH_OUT}" 2>&1; then
      echo "Expected managed development shell refresh lane to succeed after the reviewed VM resources changed" >&2
      cat "${LIVE_DEBUG_REFRESH_OUT}" >&2
      exit 1
    fi
    grep -q '^WORKCELL_DEVELOPMENT_REFRESH_OK$' "${LIVE_DEBUG_REFRESH_OUT}"
    grep -q "Refreshing managed Colima profile ${LIVE_DEBUG_PROFILE_NAME} to apply the requested reviewed VM resources." "${LIVE_DEBUG_REFRESH_OUT}"
    if ! LIVE_DEBUG_POST_REFRESH_PS="$(ps -axo pid=,command=)"; then
      echo "Expected to read the host process inventory after exact reaper certification" >&2
      exit 1
    fi
    while IFS= read -r old_profile_pid; do
      if ! awk -v pid="${old_profile_pid}" '$1 == pid { found = 1 } END { exit found }' <<<"${LIVE_DEBUG_POST_REFRESH_PS}"; then
        echo "Expected refreshed profile process ${old_profile_pid} to be absent after exact reaper certification" >&2
        exit 1
      fi
    done <<<"${LIVE_DEBUG_OLD_PROFILE_PIDS}"
    if grep -Eq 'Preparing the runtime image for profile|runtime-build|429 Too Many Requests' "${LIVE_DEBUG_REFRESH_OUT}" &&
      ! grep -q 'Workcell timed out waiting for managed Colima profile' "${LIVE_DEBUG_REFRESH_OUT}"; then
      echo "Expected refreshed managed development shell to reuse or restore the prepared runtime image without rebuilding" >&2
      cat "${LIVE_DEBUG_REFRESH_OUT}" >&2
      exit 1
    fi
    DETACHED_SESSION_START_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.start.out"
    DETACHED_SESSION_SHOW_RUNNING_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.show-running.out"
    DETACHED_SESSION_ATTACH_TYPESCRIPT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.attach.typescript"
    DETACHED_SESSION_ATTACH_READY_SEND_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.send-attach-ready.out"
    DETACHED_SESSION_SEND_ALPHA_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.send-alpha.out"
    DETACHED_SESSION_SEND_BETA_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.send-beta.out"
    DETACHED_SESSION_DIFF_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.diff.out"
    DETACHED_SESSION_STOP_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.stop.out"
    DETACHED_SESSION_SHOW_STOPPED_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.show-stopped.out"
    DETACHED_SESSION_TIMELINE_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.timeline.out"
    DETACHED_SESSION_LOGS_AUDIT_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.logs-audit.out"
    DETACHED_SESSION_LOGS_DEBUG_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.logs-debug.out"
    DETACHED_SESSION_LOGS_FILE_TRACE_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.logs-file-trace.out"
    DETACHED_SESSION_LOGS_TRANSCRIPT_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.logs-transcript.out"
    DETACHED_SESSION_LIST_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.list.out"
    DETACHED_SESSION_DEBUG_LOG="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.debug.log"
    DETACHED_SESSION_FILE_TRACE_LOG="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.file-trace.log"
    DETACHED_SESSION_SOURCE_WORKSPACE="${BARRIER_VERIFY_ROOT}/debug/live-detached-session-source"
    DETACHED_SESSION_SOURCE_SENTINEL_REL=".workcell-detached-session-sentinel-$$.log"
    DETACHED_SESSION_HOST_GIT_BIN="$(command -v git)"
    rm -rf "${DETACHED_SESSION_SOURCE_WORKSPACE}"
    env -i \
      PATH="${TRUSTED_HOST_PATH}" \
      HOME="${REAL_HOME}" \
      LC_ALL=C \
      LANG=C \
      "${DETACHED_SESSION_HOST_GIT_BIN}" clone --quiet --no-hardlinks "${ROOT_DIR}" "${DETACHED_SESSION_SOURCE_WORKSPACE}"
    DETACHED_SESSION_SOURCE_WORKSPACE="$(cd "${DETACHED_SESSION_SOURCE_WORKSPACE}" && pwd -P)"
    DETACHED_SESSION_SOURCE_SENTINEL_PATH="${DETACHED_SESSION_SOURCE_WORKSPACE}/${DETACHED_SESSION_SOURCE_SENTINEL_REL}"
    if [[ -e "${DETACHED_SESSION_SOURCE_SENTINEL_PATH}" ]]; then
      echo "Detached session source sentinel already exists in the source workspace: ${DETACHED_SESSION_SOURCE_SENTINEL_PATH}" >&2
      exit 1
    fi
    DETACHED_SESSION_WORKER_COMMAND="$(
      cat <<'EOF'
set -euo pipefail
WORKER_SENTINEL_REL="${1:?missing detached-session sentinel path}"
: >"/workspace/${WORKER_SENTINEL_REL}"
test -t 0
test -t 1
test -t 2
printf 'SESSION_TTY_OK\n'
printf 'SESSION_TTY_OK\n' >>"/workspace/${WORKER_SENTINEL_REL}"
printf 'SESSION_READY\n'
printf 'SESSION_READY\n' >>"/workspace/${WORKER_SENTINEL_REL}"
test -r /workspace/AGENTS.md
test ! -w /workspace/AGENTS.md
test -r /workspace/.git/config
test ! -w /workspace/.git/config
test -d /opt/workcell/host-inputs
[[ -z "$(find /opt/workcell/host-inputs -mindepth 1 -print -quit)" ]]
printf 'SESSION_MASKS_OK\n'
printf 'SESSION_MASKS_OK\n' >>"/workspace/${WORKER_SENTINEL_REL}"
trap 'printf "SESSION_STOPPING\n"; printf "SESSION_STOPPING\n" >>"/workspace/${WORKER_SENTINEL_REL}"; exit 0' TERM INT
while IFS= read -r line; do
  printf 'SESSION_RECV:%s\n' "${line}"
  printf 'SESSION_RECV:%s\n' "${line}" >>"/workspace/${WORKER_SENTINEL_REL}"
done
EOF
    )"
    ACK_BREAKGLASS_TODAY_UTC="$(date -u +%Y-%m-%d)"
    if ! run_workcell_verify \
      session start \
      --agent codex \
      --mode development \
      --workspace "${DETACHED_SESSION_SOURCE_WORKSPACE}" \
      --session-workspace isolated \
      --no-default-injection-policy \
      --colima-profile "${LIVE_DETACHED_PROFILE_NAME}" \
      --debug-log "${DETACHED_SESSION_DEBUG_LOG}" \
      --file-trace-log "${DETACHED_SESSION_FILE_TRACE_LOG}" \
      --allow-arbitrary-command \
      "--ack-arbitrary-command=${ACK_BREAKGLASS_TODAY_UTC}" \
      -- /bin/bash -lc "${DETACHED_SESSION_WORKER_COMMAND}" -- "${DETACHED_SESSION_SOURCE_SENTINEL_REL}" >"${DETACHED_SESSION_START_OUT}" 2>&1; then
      echo "Expected detached session start to succeed against the live runtime" >&2
      cat "${DETACHED_SESSION_START_OUT}" >&2
      exit 1
    fi
    DETACHED_SESSION_ID="$(sed -n 's/^session_id=//p' "${DETACHED_SESSION_START_OUT}" | head -n1)"
    DETACHED_SESSION_WORKSPACE="$(sed -n 's/^workspace=//p' "${DETACHED_SESSION_START_OUT}" | head -n1)"
    [[ -n "${DETACHED_SESSION_ID}" ]] || {
      echo "Detached session start did not report a session_id" >&2
      cat "${DETACHED_SESSION_START_OUT}" >&2
      exit 1
    }
    [[ -n "${DETACHED_SESSION_WORKSPACE}" ]] || {
      echo "Detached session start did not report a workspace path" >&2
      cat "${DETACHED_SESSION_START_OUT}" >&2
      exit 1
    }
    DETACHED_SESSION_MONITOR_PID="$(sed -n 's/^monitor_pid=//p' "${DETACHED_SESSION_START_OUT}" | head -n1)"
    [[ -n "${DETACHED_SESSION_MONITOR_PID}" ]] || {
      echo "Detached session start did not report a monitor_pid" >&2
      cat "${DETACHED_SESSION_START_OUT}" >&2
      exit 1
    }
    grep -q '^status=running$' "${DETACHED_SESSION_START_OUT}"
    grep -q '^live_status=running$' "${DETACHED_SESSION_START_OUT}"
    grep -q '^control_mode=detached$' "${DETACHED_SESSION_START_OUT}"
    grep -q "^workspace_origin=${DETACHED_SESSION_SOURCE_WORKSPACE}$" "${DETACHED_SESSION_START_OUT}"
    grep -q "^workspace_root=${DETACHED_SESSION_SOURCE_WORKSPACE}$" "${DETACHED_SESSION_START_OUT}"
    if ! kill -0 "${DETACHED_SESSION_MONITOR_PID}" >/dev/null 2>&1; then
      echo "Detached session reported a dead monitor_pid immediately after start: ${DETACHED_SESSION_MONITOR_PID}" >&2
      cat "${DETACHED_SESSION_START_OUT}" >&2
      exit 1
    fi
    case "${DETACHED_SESSION_WORKSPACE}" in
      "${DETACHED_SESSION_SOURCE_WORKSPACE}/.git/workcell-sessions/"*"/repo") ;;
      *)
        echo "Detached session workspace did not stay under the repo git-admin area: ${DETACHED_SESSION_WORKSPACE}" >&2
        exit 1
        ;;
    esac
    test -d "${DETACHED_SESSION_WORKSPACE}"
    if ! run_workcell_verify session show --id "${DETACHED_SESSION_ID}" >"${DETACHED_SESSION_SHOW_RUNNING_OUT}" 2>&1; then
      echo "Expected session show to succeed for a running detached session" >&2
      cat "${DETACHED_SESSION_SHOW_RUNNING_OUT}" >&2
      exit 1
    fi
    grep -q "\"session_id\": \"${DETACHED_SESSION_ID}\"" "${DETACHED_SESSION_SHOW_RUNNING_OUT}"
    grep -q '"status": "running"' "${DETACHED_SESSION_SHOW_RUNNING_OUT}"
    grep -q '"live_status": "running"' "${DETACHED_SESSION_SHOW_RUNNING_OUT}"
    grep -q "\"workspace_origin\": \"${DETACHED_SESSION_SOURCE_WORKSPACE}\"" "${DETACHED_SESSION_SHOW_RUNNING_OUT}"
    grep -q "\"workspace_root\": \"${DETACHED_SESSION_SOURCE_WORKSPACE}\"" "${DETACHED_SESSION_SHOW_RUNNING_OUT}"
    grep -q "\"worktree_path\": \"${DETACHED_SESSION_WORKSPACE}\"" "${DETACHED_SESSION_SHOW_RUNNING_OUT}"
    grep -q "\"monitor_pid\": \"${DETACHED_SESSION_MONITOR_PID}\"" "${DETACHED_SESSION_SHOW_RUNNING_OUT}"
    DETACHED_SESSION_AUDIT_DIR="$(jq -r '.session_audit_dir // empty' "${DETACHED_SESSION_SHOW_RUNNING_OUT}")"
    [[ -n "${DETACHED_SESSION_AUDIT_DIR}" ]] || {
      echo "Detached session show output did not report session_audit_dir" >&2
      cat "${DETACHED_SESSION_SHOW_RUNNING_OUT}" >&2
      exit 1
    }
    DETACHED_SESSION_MONITOR_STATE_FILE="${DETACHED_SESSION_AUDIT_DIR}/session-monitor.env"
    DETACHED_SESSION_MONITOR_COMMAND="$(ps -o command= -p "${DETACHED_SESSION_MONITOR_PID}" 2>/dev/null | head -n1 || true)"
    case "${DETACHED_SESSION_MONITOR_COMMAND}" in
      *"${ROOT_DIR}/scripts/workcell"*' session monitor --state-file '*"${DETACHED_SESSION_MONITOR_STATE_FILE}") ;;
      *)
        echo "Detached session monitor pid did not match the expected monitor command: ${DETACHED_SESSION_MONITOR_PID}" >&2
        printf '%s\n' "${DETACHED_SESSION_MONITOR_COMMAND}" >&2
        exit 1
        ;;
    esac
    DETACHED_SESSION_SENTINEL_PATH="${DETACHED_SESSION_WORKSPACE}/${DETACHED_SESSION_SOURCE_SENTINEL_REL}"
    for _ in $(seq 1 90); do
      if [[ -f "${DETACHED_SESSION_SENTINEL_PATH}" ]] &&
        grep -q '^SESSION_READY$' "${DETACHED_SESSION_SENTINEL_PATH}" &&
        grep -q '^SESSION_MASKS_OK$' "${DETACHED_SESSION_SENTINEL_PATH}"; then
        break
      fi
      sleep 2
    done
    if [[ ! -f "${DETACHED_SESSION_SENTINEL_PATH}" ]]; then
      echo "Detached session sentinel did not appear in the isolated workspace: ${DETACHED_SESSION_SENTINEL_PATH}" >&2
      cat "${DETACHED_SESSION_START_OUT}" >&2
      exit 1
    fi
    grep -q '^SESSION_READY$' "${DETACHED_SESSION_SENTINEL_PATH}"
    grep -q '^SESSION_MASKS_OK$' "${DETACHED_SESSION_SENTINEL_PATH}"
    grep -q '^SESSION_TTY_OK$' "${DETACHED_SESSION_SENTINEL_PATH}"
    DETACHED_ATTACH_STATUS=0
    DETACHED_ATTACH_READY=0
    DETACHED_ATTACH_READY_MESSAGE="attach-ready"
    DETACHED_ATTACH_READY_SEND_COUNT=0
    DETACHED_ATTACH_EARLY_STATUS=""
    DETACHED_ATTACH_MESSAGES_RECEIVED=0
    (
      VERIFY_INVARIANTS_EXPECTED_FAILURE=1
      run_typescript_probe_with_timeout 30 \
        "${DETACHED_SESSION_ATTACH_TYPESCRIPT}" \
        "${ROOT_DIR}/scripts/workcell" \
        session attach \
        --id "${DETACHED_SESSION_ID}" \
        --no-stdin
    ) &
    DETACHED_ATTACH_PID=$!
    for ((ready_send_attempt = 0; ready_send_attempt < 5; ready_send_attempt++)); do
      if ! run_workcell_verify session send \
        --id "${DETACHED_SESSION_ID}" \
        --message "${DETACHED_ATTACH_READY_MESSAGE}" >"${DETACHED_SESSION_ATTACH_READY_SEND_OUT}" 2>&1; then
        echo "Expected detached session attach-readiness send to succeed" >&2
        cat "${DETACHED_SESSION_ATTACH_READY_SEND_OUT}" >&2
        exit 1
      fi
      DETACHED_ATTACH_READY_SEND_COUNT=$((DETACHED_ATTACH_READY_SEND_COUNT + 1))
      for ((ready_poll_attempt = 0; ready_poll_attempt < 3; ready_poll_attempt++)); do
        if grep -Fq "SESSION_RECV:${DETACHED_ATTACH_READY_MESSAGE}" "${DETACHED_SESSION_ATTACH_TYPESCRIPT}" 2>/dev/null; then
          DETACHED_ATTACH_READY=1
          break 2
        fi
        if ! kill -0 "${DETACHED_ATTACH_PID}" >/dev/null 2>&1; then
          if wait "${DETACHED_ATTACH_PID}"; then
            DETACHED_ATTACH_EARLY_STATUS=0
          else
            DETACHED_ATTACH_EARLY_STATUS=$?
          fi
          DETACHED_ATTACH_PID=""
          break 2
        fi
        if ((ready_poll_attempt < 2)); then
          sleep 1
        fi
      done
    done
    if [[ "${DETACHED_ATTACH_READY}" != "1" ]]; then
      echo "Detached session attach did not become ready before live send assertions" >&2
      if [[ -n "${DETACHED_ATTACH_EARLY_STATUS}" ]]; then
        echo "Detached session attach exited early with status ${DETACHED_ATTACH_EARLY_STATUS}" >&2
      fi
      cat "${DETACHED_SESSION_ATTACH_TYPESCRIPT}" >&2 || true
      cat "${DETACHED_SESSION_ATTACH_READY_SEND_OUT}" >&2 || true
      exit 1
    fi
  fi
fi
