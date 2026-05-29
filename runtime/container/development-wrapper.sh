#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

AGENT_NAME="${AGENT_NAME:-${WORKCELL_LAUNCH_TARGET:-}}"
export WORKCELL_WRAPPER_CONTEXT=1
export ADAPTER_ROOT="/opt/workcell/adapters"

workcell_die() {
  echo "$*" >&2
  exit 2
}

# shellcheck source=runtime/container/assurance.sh
source /usr/local/libexec/workcell/assurance.sh
# shellcheck source=runtime/container/home-control-plane.sh
source /usr/local/libexec/workcell/home-control-plane.sh
# shellcheck source=runtime/container/runtime-user.sh
source /usr/local/libexec/workcell/runtime-user.sh

development_wrapper_protected_runtime_match() {
  local candidate_path="${1:-}"
  local protected_path=""

  [[ -n "${candidate_path}" ]] || return 1
  [[ -e "${candidate_path}" ]] || return 1

  for protected_path in \
    /usr/local/libexec/workcell/real/codex \
    /usr/local/libexec/workcell/real/claude \
    /usr/local/libexec/workcell/real/node \
    /usr/local/libexec/workcell/real/git \
    /usr/local/libexec/workcell/core/git \
    /usr/local/libexec/workcell/git \
    /usr/local/libexec/workcell/node; do
    if [[ -e "${protected_path}" ]] && [[ "${candidate_path}" -ef "${protected_path}" ]]; then
      printf '%s\n' "${protected_path}"
      return 0
    fi
  done

  return 1
}

reject_protected_runtime_launch() {
  local command_path="${1:-}"
  local command_name=""
  local protected_match=""

  [[ -n "${command_path}" ]] || return 0
  command_name="${command_path##*/}"

  case "${command_name}" in
    codex | claude | gemini)
      workcell_die "Workcell blocked direct provider command in development mode: ${command_name} (run through the managed provider entrypoint)."
      ;;
  esac

  if [[ "${command_path}" == */* ]]; then
    protected_match="$(development_wrapper_protected_runtime_match "${command_path}" || true)"
    if [[ -n "${protected_match}" ]]; then
      workcell_die "Workcell blocked direct protected runtime execution in development mode: ${command_path}."
    fi
  fi
}

workcell_sanitize_runtime_env
workcell_pin_runtime_env
mkdir -p "${TMPDIR}"
if workcell_should_reexec_as_runtime_user; then
  workcell_reexec_as_runtime_user /usr/local/libexec/workcell/development-wrapper.sh "$@"
fi

[[ -n "${AGENT_NAME}" ]] || workcell_die "Workcell development wrapper requires AGENT_NAME."

seed_agent_home "${AGENT_NAME}"
emit_session_assurance_notice

if [[ $# -eq 0 ]]; then
  set -- /bin/bash
fi

reject_protected_runtime_launch "$1"

exec "$@"
