#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

AGENT_NAME="${AGENT_NAME:-}"
AGENT_UI="${AGENT_UI:-cli}"
HOME="${HOME:-/state/agent-home}"
CODEX_HOME="${CODEX_HOME:-${HOME}/.codex}"
CODEX_PROFILE="${CODEX_PROFILE:-strict}"
WORKCELL_MODE="${WORKCELL_MODE:-${CODEX_PROFILE}}"
WORKCELL_AGENT_AUTONOMY="${WORKCELL_AGENT_AUTONOMY:-yolo}"
TMPDIR="${TMPDIR:-/state/tmp}"
WORKSPACE="${WORKSPACE:-/workspace}"
export ADAPTER_ROOT="/opt/workcell/adapters"

# shellcheck source=runtime/container/assurance.sh
source /usr/local/libexec/workcell/assurance.sh
# shellcheck source=runtime/container/provider-policy.sh
source /usr/local/libexec/workcell/provider-policy.sh
# shellcheck source=runtime/container/home-control-plane.sh
source /usr/local/libexec/workcell/home-control-plane.sh
# shellcheck source=runtime/container/runtime-user.sh
source /usr/local/libexec/workcell/runtime-user.sh

WORKCELL_FILE_TRACE_CHILD_PID=""
WORKCELL_FILE_TRACE_STATUS=0
WORKCELL_FILE_TRACE_TEARDOWN_DONE=0

workcell_run_command_with_file_trace_finish() {
  [[ "${WORKCELL_FILE_TRACE_TEARDOWN_DONE}" == "1" ]] && return 0
  workcell_file_trace_stop_watcher
  workcell_file_trace_emit \
    "event=provider-exit" \
    "agent=${AGENT_NAME}" \
    "status=${WORKCELL_FILE_TRACE_STATUS}"
  WORKCELL_FILE_TRACE_TEARDOWN_DONE=1
}

workcell_run_command_with_file_trace_signal() {
  local signal="$1"

  if [[ -n "${WORKCELL_FILE_TRACE_CHILD_PID}" ]] &&
    kill -0 "${WORKCELL_FILE_TRACE_CHILD_PID}" >/dev/null 2>&1; then
    kill "-${signal}" "${WORKCELL_FILE_TRACE_CHILD_PID}" >/dev/null 2>&1 ||
      kill "${WORKCELL_FILE_TRACE_CHILD_PID}" >/dev/null 2>&1 || true
    wait "${WORKCELL_FILE_TRACE_CHILD_PID}" >/dev/null 2>&1 || true
  fi
  case "${signal}" in
    INT) WORKCELL_FILE_TRACE_STATUS=130 ;;
    TERM) WORKCELL_FILE_TRACE_STATUS=143 ;;
    *) WORKCELL_FILE_TRACE_STATUS=128 ;;
  esac
  trap - INT TERM
  workcell_run_command_with_file_trace_finish
  exit "${WORKCELL_FILE_TRACE_STATUS}"
}

run_command_with_file_trace() {
  local status=0
  local child_pid=""

  workcell_file_trace_emit \
    "event=provider-launch" \
    "agent=${AGENT_NAME}" \
    "ui=${AGENT_UI}" \
    "mode=${WORKCELL_MODE}"
  workcell_file_trace_start_watcher "${HOME}"

  WORKCELL_FILE_TRACE_CHILD_PID=""
  WORKCELL_FILE_TRACE_STATUS=0
  WORKCELL_FILE_TRACE_TEARDOWN_DONE=0
  trap 'workcell_run_command_with_file_trace_signal INT' INT
  trap 'workcell_run_command_with_file_trace_signal TERM' TERM
  "$@" &
  child_pid="$!"
  WORKCELL_FILE_TRACE_CHILD_PID="${child_pid}"
  set +e
  wait "${child_pid}"
  status="$?"
  set -e

  WORKCELL_FILE_TRACE_STATUS="${status}"
  trap - INT TERM
  workcell_run_command_with_file_trace_finish
  return "${status}"
}

umask 077

if [[ "$$" -ne 1 ]]; then
  pid1_comm="$(tr -d '\n' </proc/1/comm 2>/dev/null || true)"
  if [[ "${PPID}" == "1" ]] &&
    [[ "${pid1_comm}" =~ ^(docker-init|tini|dumb-init)$ ]]; then
    :
  elif [[ "${WORKCELL_MODE}" != "strict" ]] || [[ "${CODEX_PROFILE}" != "strict" ]]; then
    echo "Workcell blocked non-PID1 breakglass request: launch breakglass only through the host workcell command." >&2
    exit 2
  else
    CODEX_PROFILE="strict"
    WORKCELL_MODE="strict"
  fi
fi

export TMPDIR

if [[ -z "${AGENT_NAME}" ]]; then
  echo "Workcell requires AGENT_NAME to be set explicitly at the runtime entrypoint." >&2
  exit 2
fi

case "${WORKCELL_AGENT_AUTONOMY}" in
  yolo | prompt) ;;
  *)
    echo "Unsupported Workcell agent autonomy mode at runtime: ${WORKCELL_AGENT_AUTONOMY}" >&2
    exit 2
    ;;
esac

if [[ "$(id -u)" == "0" ]]; then
  workcell_write_runtime_state
fi

if workcell_should_reexec_as_runtime_user; then
  workcell_reexec_as_runtime_user /usr/local/libexec/workcell/entrypoint.sh "$@"
fi

mkdir -p "${HOME}"
mkdir -p "${TMPDIR}"

seed_agent_home "${AGENT_NAME}"
emit_session_assurance_notice

if [[ $# -eq 0 ]]; then
  case "${AGENT_NAME}:${AGENT_UI}" in
    codex:cli)
      set -- codex
      ;;
    codex:gui)
      set -- codex app-server
      ;;
    claude:cli)
      set -- claude
      ;;
    gemini:cli)
      set -- gemini
      ;;
    *)
      echo "Unsupported agent/ui combination: ${AGENT_NAME}:${AGENT_UI}" >&2
      exit 2
      ;;
  esac
fi

if [[ "${WORKCELL_MODE}" == "development" ]] && [[ $# -gt 0 ]] && [[ "$1" != "${AGENT_NAME}" ]]; then
  set -- /bin/bash /usr/local/libexec/workcell/development-wrapper.sh "$@"
else
  validate_command_args "${AGENT_NAME}" "$@"

  if [[ $# -gt 0 ]]; then
    case "${AGENT_NAME}" in
      codex)
        if [[ "$1" == "codex" ]]; then
          set -- /usr/local/libexec/workcell/core/codex "${@:2}"
        fi
        ;;
      claude)
        if [[ "$1" == "claude" ]]; then
          set -- /usr/local/libexec/workcell/core/claude "${@:2}"
        fi
        ;;
      gemini)
        if [[ "$1" == "gemini" ]]; then
          set -- /usr/local/libexec/workcell/core/gemini "${@:2}"
        fi
        ;;
    esac
  fi
fi

printf 'agent=%s ui=%s mode=%s autonomy=%s workspace=%s\n' "${AGENT_NAME}" "${AGENT_UI}" "${WORKCELL_MODE}" "${WORKCELL_AGENT_AUTONOMY}" "${WORKSPACE}" >&2
if workcell_file_trace_enabled; then
  run_command_with_file_trace "$@"
  exit "$?"
fi
exec "$@"
