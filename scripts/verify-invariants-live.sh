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
    if ! run_workcell_verify session send --id "${DETACHED_SESSION_ID}" --message alpha >"${DETACHED_SESSION_SEND_ALPHA_OUT}" 2>&1; then
      echo "Expected detached session send(alpha) to succeed" >&2
      cat "${DETACHED_SESSION_SEND_ALPHA_OUT}" >&2
      exit 1
    fi
    if ! run_workcell_verify session send --id "${DETACHED_SESSION_ID}" --message beta >"${DETACHED_SESSION_SEND_BETA_OUT}" 2>&1; then
      echo "Expected detached session send(beta) to succeed" >&2
      cat "${DETACHED_SESSION_SEND_BETA_OUT}" >&2
      exit 1
    fi
    grep -q "^session_id=${DETACHED_SESSION_ID}$" "${DETACHED_SESSION_SEND_ALPHA_OUT}"
    grep -q '^sent_bytes=6$' "${DETACHED_SESSION_SEND_ALPHA_OUT}"
    grep -q '^sent_bytes=5$' "${DETACHED_SESSION_SEND_BETA_OUT}"
    for ((message_poll_attempt = 0; message_poll_attempt < 10; message_poll_attempt++)); do
      if grep -Fq 'SESSION_RECV:alpha' "${DETACHED_SESSION_ATTACH_TYPESCRIPT}" 2>/dev/null &&
        grep -Fq 'SESSION_RECV:beta' "${DETACHED_SESSION_ATTACH_TYPESCRIPT}" 2>/dev/null; then
        DETACHED_ATTACH_MESSAGES_RECEIVED=1
        break
      fi
      if ! kill -0 "${DETACHED_ATTACH_PID}" >/dev/null 2>&1; then
        if wait "${DETACHED_ATTACH_PID}"; then
          DETACHED_ATTACH_EARLY_STATUS=0
        else
          DETACHED_ATTACH_EARLY_STATUS=$?
        fi
        DETACHED_ATTACH_PID=""
        break
      fi
      if ((message_poll_attempt < 9)); then
        sleep 1
      fi
    done
    if [[ "${DETACHED_ATTACH_MESSAGES_RECEIVED}" != "1" ]]; then
      echo "Detached session attach did not stream the live send assertions" >&2
      if [[ -n "${DETACHED_ATTACH_EARLY_STATUS}" ]]; then
        echo "Detached session attach exited early with status ${DETACHED_ATTACH_EARLY_STATUS}" >&2
      fi
      cat "${DETACHED_SESSION_ATTACH_TYPESCRIPT}" >&2 || true
      exit 1
    fi
    if wait "${DETACHED_ATTACH_PID}"; then
      DETACHED_ATTACH_STATUS=0
    else
      DETACHED_ATTACH_STATUS=$?
    fi
    DETACHED_ATTACH_PID=""
    if [[ "${DETACHED_ATTACH_STATUS}" != "0" ]] && [[ "${DETACHED_ATTACH_STATUS}" != "124" ]]; then
      echo "Expected detached session attach to stream live output or timeout cleanly" >&2
      echo "Detached session attach exited with status ${DETACHED_ATTACH_STATUS}" >&2
      cat "${DETACHED_SESSION_ATTACH_TYPESCRIPT}" >&2 || true
      exit 1
    fi
    grep -q 'SESSION_RECV:alpha' "${DETACHED_SESSION_ATTACH_TYPESCRIPT}"
    grep -q 'SESSION_RECV:beta' "${DETACHED_SESSION_ATTACH_TYPESCRIPT}"
    DETACHED_SESSION_GIT_DIR="$(git -C "${DETACHED_SESSION_WORKSPACE}" rev-parse --absolute-git-dir)"
    SOURCE_GIT_DIR="$(git -C "${DETACHED_SESSION_SOURCE_WORKSPACE}" rev-parse --absolute-git-dir)"
    if [[ "${DETACHED_SESSION_GIT_DIR}" != "${DETACHED_SESSION_WORKSPACE}/.git" ]]; then
      echo "Detached session clone did not keep a self-contained git dir: ${DETACHED_SESSION_GIT_DIR}" >&2
      exit 1
    fi
    if [[ "${DETACHED_SESSION_GIT_DIR}" == "${SOURCE_GIT_DIR}" ]]; then
      echo "Detached session clone unexpectedly reused the source workspace git admin directory" >&2
      exit 1
    fi
    for _ in $(seq 1 90); do
      if [[ -f "${DETACHED_SESSION_SENTINEL_PATH}" ]] &&
        grep -q '^SESSION_RECV:alpha$' "${DETACHED_SESSION_SENTINEL_PATH}" &&
        grep -q '^SESSION_RECV:beta$' "${DETACHED_SESSION_SENTINEL_PATH}"; then
        break
      fi
      sleep 2
    done
    grep -q '^SESSION_RECV:alpha$' "${DETACHED_SESSION_SENTINEL_PATH}"
    grep -q '^SESSION_RECV:beta$' "${DETACHED_SESSION_SENTINEL_PATH}"
    if [[ -e "${DETACHED_SESSION_SOURCE_SENTINEL_PATH}" ]]; then
      echo "Detached session wrote into the source workspace instead of the isolated clone: ${DETACHED_SESSION_SOURCE_SENTINEL_PATH}" >&2
      exit 1
    fi
    if ! run_workcell_verify session diff --id "${DETACHED_SESSION_ID}" >"${DETACHED_SESSION_DIFF_OUT}" 2>&1; then
      echo "Expected detached session diff to succeed for the isolated workspace clone" >&2
      cat "${DETACHED_SESSION_DIFF_OUT}" >&2
      exit 1
    fi
    grep -q "^session_id=${DETACHED_SESSION_ID}$" "${DETACHED_SESSION_DIFF_OUT}"
    grep -q "^?? ${DETACHED_SESSION_SOURCE_SENTINEL_REL}$" "${DETACHED_SESSION_DIFF_OUT}"
    if ! run_workcell_verify session stop --id "${DETACHED_SESSION_ID}" >"${DETACHED_SESSION_STOP_OUT}" 2>&1; then
      echo "Expected detached session stop to succeed" >&2
      cat "${DETACHED_SESSION_STOP_OUT}" >&2
      exit 1
    fi
    grep -q "^session_id=${DETACHED_SESSION_ID}$" "${DETACHED_SESSION_STOP_OUT}"
    grep -q '^stop_requested=1$' "${DETACHED_SESSION_STOP_OUT}"
    for _ in 1 2 3 4 5 6 7 8 9 10; do
      if run_workcell_verify session show --id "${DETACHED_SESSION_ID}" >"${DETACHED_SESSION_SHOW_STOPPED_OUT}" 2>&1 &&
        grep -q '"status": "exited"' "${DETACHED_SESSION_SHOW_STOPPED_OUT}" &&
        grep -q '"live_status": "stopped"' "${DETACHED_SESSION_SHOW_STOPPED_OUT}"; then
        break
      fi
      sleep 1
    done
    grep -q "\"session_id\": \"${DETACHED_SESSION_ID}\"" "${DETACHED_SESSION_SHOW_STOPPED_OUT}"
    grep -q '"status": "exited"' "${DETACHED_SESSION_SHOW_STOPPED_OUT}"
    grep -q '"live_status": "stopped"' "${DETACHED_SESSION_SHOW_STOPPED_OUT}"
    grep -q '"exit_status": "0"' "${DETACHED_SESSION_SHOW_STOPPED_OUT}"
    grep -q '^SESSION_STOPPING$' "${DETACHED_SESSION_SENTINEL_PATH}"
    grep -q '"current_assurance": "managed-mutable"' "${DETACHED_SESSION_SHOW_STOPPED_OUT}"
    grep -q '"final_assurance": "managed-mutable"' "${DETACHED_SESSION_SHOW_STOPPED_OUT}"
    if ! wait_for_detached_session_monitor_exit "${DETACHED_SESSION_MONITOR_PID}" "${DETACHED_SESSION_MONITOR_STATE_FILE}"; then
      echo "Detached session monitor remained alive after session finalization: ${DETACHED_SESSION_MONITOR_PID}" >&2
      cat "${DETACHED_SESSION_SHOW_STOPPED_OUT}" >&2
      exit 1
    fi
    test ! -e "${DETACHED_SESSION_AUDIT_DIR}"
    test ! -e "${DETACHED_SESSION_MONITOR_STATE_FILE}"
    if ! run_workcell_verify session timeline --id "${DETACHED_SESSION_ID}" >"${DETACHED_SESSION_TIMELINE_OUT}" 2>&1; then
      echo "Expected detached session timeline to succeed" >&2
      cat "${DETACHED_SESSION_TIMELINE_OUT}" >&2
      exit 1
    fi
    grep -q "event=launch session_id=${DETACHED_SESSION_ID}" "${DETACHED_SESSION_TIMELINE_OUT}"
    grep -q "event=attach-attempt session_id=${DETACHED_SESSION_ID}" "${DETACHED_SESSION_TIMELINE_OUT}"
    test "$(grep -c "event=command session_id=${DETACHED_SESSION_ID}" "${DETACHED_SESSION_TIMELINE_OUT}")" = "$((DETACHED_ATTACH_READY_SEND_COUNT + 2))"
    grep -q "event=stop-request session_id=${DETACHED_SESSION_ID}" "${DETACHED_SESSION_TIMELINE_OUT}"
    grep -q "event=exit session_id=${DETACHED_SESSION_ID}" "${DETACHED_SESSION_TIMELINE_OUT}"
    if ! run_workcell_verify session logs --id "${DETACHED_SESSION_ID}" --kind audit >"${DETACHED_SESSION_LOGS_AUDIT_OUT}" 2>&1; then
      echo "Expected detached session audit log retrieval to succeed" >&2
      cat "${DETACHED_SESSION_LOGS_AUDIT_OUT}" >&2
      exit 1
    fi
    grep -q "event=launch session_id=${DETACHED_SESSION_ID}" "${DETACHED_SESSION_LOGS_AUDIT_OUT}"
    grep -q "event=exit session_id=${DETACHED_SESSION_ID}" "${DETACHED_SESSION_LOGS_AUDIT_OUT}"
    if ! run_workcell_verify session logs --id "${DETACHED_SESSION_ID}" --kind debug >"${DETACHED_SESSION_LOGS_DEBUG_OUT}" 2>&1; then
      echo "Expected detached session debug log retrieval to succeed" >&2
      cat "${DETACHED_SESSION_LOGS_DEBUG_OUT}" >&2
      exit 1
    fi
    grep -q "profile=${LIVE_DETACHED_PROFILE_NAME}" "${DETACHED_SESSION_LOGS_DEBUG_OUT}"
    if ! run_workcell_verify session logs --id "${DETACHED_SESSION_ID}" --kind file-trace >"${DETACHED_SESSION_LOGS_FILE_TRACE_OUT}" 2>&1; then
      echo "Expected detached session file-trace retrieval to succeed" >&2
      cat "${DETACHED_SESSION_LOGS_FILE_TRACE_OUT}" >&2
      exit 1
    fi
    if ! grep -Eq 'event=watch-start|event=host-collect-missing' "${DETACHED_SESSION_LOGS_FILE_TRACE_OUT}"; then
      echo "Expected detached session file-trace retrieval to include watcher activity or an explicit host collection fallback" >&2
      cat "${DETACHED_SESSION_LOGS_FILE_TRACE_OUT}" >&2
      exit 1
    fi
    if run_workcell_verify session logs --id "${DETACHED_SESSION_ID}" --kind transcript >"${DETACHED_SESSION_LOGS_TRANSCRIPT_OUT}" 2>&1; then
      echo "Expected detached session transcript retrieval to fail when transcript capture is not enabled" >&2
      exit 1
    fi
    grep -q "No transcript log is recorded for session ${DETACHED_SESSION_ID}" "${DETACHED_SESSION_LOGS_TRANSCRIPT_OUT}"
    if ! run_workcell_verify session list --json --workspace "${DETACHED_SESSION_WORKSPACE}" --colima-profile "${LIVE_DETACHED_PROFILE_NAME}" >"${DETACHED_SESSION_LIST_OUT}" 2>&1; then
      echo "Expected detached session list --json to include the isolated workspace session" >&2
      cat "${DETACHED_SESSION_LIST_OUT}" >&2
      exit 1
    fi
    grep -q "\"session_id\": \"${DETACHED_SESSION_ID}\"" "${DETACHED_SESSION_LIST_OUT}"
    grep -q "\"workspace\": \"${DETACHED_SESSION_WORKSPACE}\"" "${DETACHED_SESSION_LIST_OUT}"
    grep -q '"status": "exited"' "${DETACHED_SESSION_LIST_OUT}"
    cleanup_detached_session_runtime
    DETACHED_SESSION_ID=""
    DETACHED_SESSION_WORKSPACE=""
    DETACHED_SESSION_SOURCE_SENTINEL_PATH=""
    AUDIT_LOG="$(verify_profile_target_state_dir "${LIVE_DEBUG_PROFILE_NAME}")/workcell.audit.log"
    PACKAGE_MUTATION_AUDIT_COMMAND="$(
      cat <<'EOF'
for attempt in 1 2 3; do
  # Remove a package baked into the runtime image so the mutation audit does
  # not depend on live Debian snapshot availability.
  if sudo -n /usr/local/libexec/workcell/apt-helper.sh apt-get remove -y unzip >/dev/null; then
    exit 0
  fi
  if [[ "${attempt}" -eq 3 ]]; then
    exit 1
  fi
  sleep "$((attempt * 5))"
done
EOF
    )"
    ACK_BREAKGLASS_TODAY_UTC="$(date -u +%Y-%m-%d)"
    if ! run_workcell_verify \
      --agent codex \
      --mode build \
      --workspace "${ROOT_DIR}" \
      --vm-memory 7 \
      --vm-disk 80 \
      --injection-policy "${AUTH_STATUS_ROOT}/policy.toml" \
      --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" \
      --allow-arbitrary-command \
      "--ack-arbitrary-command=${ACK_BREAKGLASS_TODAY_UTC}" \
      -- /bin/bash -lc "${PACKAGE_MUTATION_AUDIT_COMMAND}"; then
      echo "Expected package-mutation audit verification run to succeed" >&2
      exit 1
    fi
    cp "${AUDIT_LOG}" "${AUDIT_SESSION_LOG}"
    grep -Eq 'event=launch .* mode=build agent=codex ' "${AUDIT_SESSION_LOG}"
    grep -q 'record_digest=' "${AUDIT_SESSION_LOG}"
    grep -q 'execution_path=lower-assurance-debug-command' "${AUDIT_SESSION_LOG}"
    grep -q 'provider_native_sandbox_configured=disabled' "${AUDIT_SESSION_LOG}"
    grep -q 'provider_native_sandbox_effective=disabled' "${AUDIT_SESSION_LOG}"
    grep -q 'provider_native_sandbox_reason=workcell-pinned-off-due-to-bwrap-userns-incompatibility' "${AUDIT_SESSION_LOG}"
    grep -q 'event=assurance-change' "${AUDIT_SESSION_LOG}"
    grep -q 'reason=package-mutation' "${AUDIT_SESSION_LOG}"
    grep -q 'session_assurance_final=lower-assurance-package-mutation' "${AUDIT_SESSION_LOG}"
    grep -q 'event=exit' "${AUDIT_SESSION_LOG}"
    grep -q 'package_mutation_downgraded=1' "${AUDIT_SESSION_LOG}"
    PACKAGE_MUTATION_FAILURE_COMMAND="$(
      cat <<'EOF'
if sudo -n /usr/local/libexec/workcell/apt-helper.sh apt-get install -y workcell-package-that-must-not-exist-verify-fixture \
  >/tmp/workcell-package-mutation-failure.out 2>/tmp/workcell-package-mutation-failure.err; then
  echo "Expected apt-helper to propagate package-manager failure status" >&2
  cat /tmp/workcell-package-mutation-failure.out >&2 || true
  cat /tmp/workcell-package-mutation-failure.err >&2 || true
  exit 1
fi
codex --version >/tmp/workcell-package-mutation-post-failure.out 2>&1
grep -q "session previously ran package-manager mutations as root" /tmp/workcell-package-mutation-post-failure.out
EOF
    )"
    ACK_BREAKGLASS_TODAY_UTC="$(date -u +%Y-%m-%d)"
    if ! run_workcell_verify \
      --agent codex \
      --mode build \
      --workspace "${ROOT_DIR}" \
      --vm-memory 7 \
      --vm-disk 80 \
      --injection-policy "${AUTH_STATUS_ROOT}/policy.toml" \
      --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" \
      --allow-arbitrary-command \
      "--ack-arbitrary-command=${ACK_BREAKGLASS_TODAY_UTC}" \
      -- /bin/bash -lc "${PACKAGE_MUTATION_FAILURE_COMMAND}"; then
      echo "Expected package-mutation failure propagation verification run to succeed" >&2
      exit 1
    fi
    # Assert scripts/container-smoke.sh keeps the Linux runtime apt-broker
    # slow-wait probe strings.  Migrated to Go (D3): internal/workcellhardening
    # behind the workcell-citools workcell-smoke-apt-broker-probe subcommand
    # preserves the exact exit codes and stderr messages of the former inline
    # `for required in ...; do grep -Fq -- "${required}" ...; done` loop (six
    # fixed-string presence probes).  `|| exit 1` matches the former loop's
    # `exit 1` on a violated invariant.
    go_verify_citools workcell-smoke-apt-broker-probe "${ROOT_DIR}" || exit 1
    ACK_BREAKGLASS_TODAY_UTC="$(date -u +%Y-%m-%d)"
    if ! run_workcell_verify \
      --agent codex \
      --mode build \
      --workspace "${ROOT_DIR}" \
      --vm-memory 7 \
      --vm-disk 80 \
      --injection-policy "${AUTH_STATUS_ROOT}/policy.toml" \
      --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" \
      --allow-arbitrary-command \
      "--ack-arbitrary-command=${ACK_BREAKGLASS_TODAY_UTC}" \
      -- /bin/bash -lc 'test -f /opt/workcell/host-injections/manifest.json && grep -q "Repository Working Agreement" /workspace/AGENTS.md && test ! -d /workspace/AGENTS.md'; then
      echo "Expected live launcher run to stage an injection manifest and mount the tracked workspace AGENTS.md snapshot as a file" >&2
      exit 1
    fi
    delete_verify_colima_profile "${LIVE_DEBUG_PROFILE_NAME}"
    delete_verify_colima_profile "${LIVE_DETACHED_PROFILE_NAME}"
    AUDIT_RESTORE_PROFILE_NAME="workcell-audit-restore-$$"
    AUDIT_RESTORE_DIR="${REAL_HOME}/.colima/${AUDIT_RESTORE_PROFILE_NAME}"
    AUDIT_RESTORE_STATE_DIR="$(verify_profile_target_state_dir "${AUDIT_RESTORE_PROFILE_NAME}")"
    AUDIT_RESTORE_LIMA_DIR="${REAL_HOME}/.colima/_lima/colima-${AUDIT_RESTORE_PROFILE_NAME}"
    AUDIT_RESTORE_LOG="${AUDIT_RESTORE_STATE_DIR}/workcell.audit.log"
    mkdir -p "${AUDIT_RESTORE_DIR}" "${AUDIT_RESTORE_STATE_DIR}" "${AUDIT_RESTORE_LIMA_DIR}"
    printf '%s\n' "${NONGIT_WORKSPACE}" >"${AUDIT_RESTORE_DIR}/workcell.managed"
    cat >"${AUDIT_RESTORE_LIMA_DIR}/lima.yaml" <<'EOF'
cpu: 4
memory: 8
disk: 60
runtime: docker
vmType: vz
mountType: virtiofs
EOF
    printf 'timestamp=test event=launch workspace=%q\n' "${NONGIT_WORKSPACE}" >"${AUDIT_RESTORE_LOG}"
    if run_workcell_verify \
      --test-fail-after-profile-refresh \
      --agent codex \
      --no-default-injection-policy \
      --prepare \
      --allow-nongit-workspace \
      --workspace "${NONGIT_WORKSPACE}" \
      --no-default-injection-policy \
      --colima-profile "${AUDIT_RESTORE_PROFILE_NAME}" \
      --agent-arg --version >/tmp/workcell-audit-restore.out 2>&1; then
      echo "Expected managed-profile refresh test hook to fail after stashing the audit log" >&2
      exit 1
    fi
    grep -q 'Workcell test hook: forcing failure after managed profile refresh.' /tmp/workcell-audit-restore.out
    grep -q 'timestamp=test event=launch' "${AUDIT_RESTORE_LOG}"
    delete_verify_colima_profile "${AUDIT_RESTORE_PROFILE_NAME}"

    STRICT_REFRESH_PROFILE_NAME="workcell-strict-refresh-$$"
    delete_verify_colima_profile "${STRICT_REFRESH_PROFILE_NAME}"
    STRICT_REFRESH_DIR="${REAL_HOME}/.colima/${STRICT_REFRESH_PROFILE_NAME}"
    STRICT_REFRESH_STATE_DIR="$(verify_profile_target_state_dir "${STRICT_REFRESH_PROFILE_NAME}")"
    mkdir -p "${STRICT_REFRESH_DIR}" "${STRICT_REFRESH_STATE_DIR}"
    printf '%s\n' "${NONGIT_WORKSPACE}" >"${STRICT_REFRESH_DIR}/workcell.managed"
    cat >"${STRICT_REFRESH_DIR}/colima.yaml" <<'EOF'
cpu: 4
memory: 7
disk: 60
runtime: docker
vmType: vz
mountType: virtiofs
EOF
    cat >"${STRICT_REFRESH_DIR}/workcell.image-ready" <<'EOF'
image_tag=workcell:local
image_id=sha256:strict-refresh-fixture
source_date_epoch=0
EOF
    printf 'timestamp=test event=launch workspace=%q\n' "${NONGIT_WORKSPACE}" >"${STRICT_REFRESH_STATE_DIR}/workcell.audit.log"
    VERIFY_INVARIANTS_EXPECTED_FAILURE=1
    set +e
    run_workcell_verify \
      --test-fail-after-profile-refresh \
      --agent codex \
      --no-default-injection-policy \
      --allow-nongit-workspace \
      --workspace "${NONGIT_WORKSPACE}" \
      --no-default-injection-policy \
      --colima-profile "${STRICT_REFRESH_PROFILE_NAME}" \
      --agent-arg --version >/tmp/workcell-strict-refresh-preflight.out 2>&1
    strict_refresh_status=$?
    set -e
    VERIFY_INVARIANTS_EXPECTED_FAILURE=0
    if [[ "${strict_refresh_status}" -ne 88 ]]; then
      echo "Expected strict-mode refresh launch without --prepare to reach the post-refresh test hook, got ${strict_refresh_status}" >&2
      cat /tmp/workcell-strict-refresh-preflight.out >&2
      exit 1
    fi
    grep -q "Refreshing managed Colima profile ${STRICT_REFRESH_PROFILE_NAME} to apply the requested reviewed VM resources." /tmp/workcell-strict-refresh-preflight.out
    grep -q "No prepared runtime image is recorded for strict mode on profile ${STRICT_REFRESH_PROFILE_NAME}." /tmp/workcell-strict-refresh-preflight.out
    grep -q "Workcell will seed or refresh the prepared runtime image automatically before launching codex in strict mode." /tmp/workcell-strict-refresh-preflight.out
    assert_output_did_not_start_colima \
      /tmp/workcell-strict-refresh-preflight.out \
      "Strict-mode refresh launch should still stop at the post-refresh hook before Colima startup"
    grep -q 'Workcell test hook: forcing failure after managed profile refresh.' /tmp/workcell-strict-refresh-preflight.out
    VERIFY_INVARIANTS_EXPECTED_FAILURE=1
    set +e
    run_workcell_verify \
      --test-fail-after-profile-refresh \
      --agent codex \
      --no-default-injection-policy \
      --prepare \
      --allow-nongit-workspace \
      --workspace "${NONGIT_WORKSPACE}" \
      --no-default-injection-policy \
      --colima-profile "${STRICT_REFRESH_PROFILE_NAME}" \
      --agent-arg --version >/tmp/workcell-strict-refresh-prepare.out 2>&1
    strict_refresh_prepare_status=$?
    set -e
    VERIFY_INVARIANTS_EXPECTED_FAILURE=0
    if [[ "${strict_refresh_prepare_status}" -ne 0 ]]; then
      echo "Expected follow-up strict prepare to continue as a fresh managed launch after the refresh cleanup, got ${strict_refresh_prepare_status}" >&2
      cat /tmp/workcell-strict-refresh-prepare.out >&2
      exit 1
    fi
    if grep -q 'Refusing to reuse unmanaged Colima profile' /tmp/workcell-strict-refresh-prepare.out; then
      echo "Follow-up strict prepare should not regress into unmanaged-profile safety after the refresh cleanup path." >&2
      cat /tmp/workcell-strict-refresh-prepare.out >&2
      exit 1
    fi
    if grep -q -- '--repair-profile' /tmp/workcell-strict-refresh-prepare.out; then
      echo "Follow-up strict prepare should not request --repair-profile after the managed refresh cleanup path." >&2
      cat /tmp/workcell-strict-refresh-prepare.out >&2
      exit 1
    fi
    grep -q 'prepare=1 seeding the prepared runtime image before launch.' /tmp/workcell-strict-refresh-prepare.out
    grep -q "Prepared runtime image is ready for profile ${STRICT_REFRESH_PROFILE_NAME}" /tmp/workcell-strict-refresh-prepare.out
    delete_verify_colima_profile "${STRICT_REFRESH_PROFILE_NAME}"
  fi
fi
