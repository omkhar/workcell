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

# shellcheck disable=SC1091
# shellcheck source=provider-policy.sh
source /usr/local/libexec/workcell/provider-policy.sh
# shellcheck disable=SC1091
# shellcheck source=home-control-plane.sh
source /usr/local/libexec/workcell/home-control-plane.sh

umask 077
mkdir -p "${HOME}"
mkdir -p "${TMPDIR}"

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

seed_agent_home "${AGENT_NAME}"

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

validate_command_args "${AGENT_NAME}" "$@"

if [[ $# -gt 0 ]]; then
  case "${AGENT_NAME}" in
    codex)
      if [[ "$1" == "codex" ]]; then
        case "${CODEX_PROFILE}" in
          strict | build)
            set -- /usr/local/libexec/workcell/core/codex --profile "${CODEX_PROFILE}" "${@:2}"
            ;;
          *)
            set -- /usr/local/libexec/workcell/core/codex "${@:2}"
            ;;
        esac
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

printf 'agent=%s ui=%s mode=%s autonomy=%s workspace=%s\n' "${AGENT_NAME}" "${AGENT_UI}" "${WORKCELL_MODE}" "${WORKCELL_AGENT_AUTONOMY}" "${WORKSPACE}" >&2
exec "$@"
