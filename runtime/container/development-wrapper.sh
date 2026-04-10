#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

AGENT_NAME="${AGENT_NAME:-${WORKCELL_LAUNCH_TARGET:-}}"
TRUSTED_PATH="/usr/local/bin:/usr/local/sbin:/usr/bin:/usr/sbin:/bin:/sbin"
export WORKCELL_WRAPPER_CONTEXT=1
export ADAPTER_ROOT="/opt/workcell/adapters"

workcell_die() {
  echo "$*" >&2
  exit 2
}

pin_runtime_env() {
  local state_mode=""
  local state_profile=""
  local state_autonomy=""
  local state_mutability=""
  local pid1_mode=""
  local pid1_profile=""
  local pid1_autonomy=""
  local pid1_mutability=""

  HOME=/state/agent-home
  CODEX_HOME="${HOME}/.codex"
  TMPDIR=/state/tmp
  state_mode="$(workcell_runtime_state_value WORKCELL_MODE || true)"
  state_profile="$(workcell_runtime_state_value CODEX_PROFILE || true)"
  state_autonomy="$(workcell_runtime_state_value WORKCELL_AGENT_AUTONOMY || true)"
  state_mutability="$(workcell_runtime_state_value WORKCELL_CONTAINER_MUTABILITY || true)"
  pid1_mode="$(workcell_pid1_env_value WORKCELL_MODE || true)"
  pid1_profile="$(workcell_pid1_env_value CODEX_PROFILE || true)"
  pid1_autonomy="$(workcell_pid1_env_value WORKCELL_AGENT_AUTONOMY || true)"
  pid1_mutability="$(workcell_pid1_env_value WORKCELL_CONTAINER_MUTABILITY || true)"
  WORKCELL_MODE="${state_mode:-${pid1_mode:-strict}}"
  CODEX_PROFILE="${state_profile:-${pid1_profile:-${WORKCELL_MODE}}}"
  WORKCELL_AGENT_AUTONOMY="${state_autonomy:-${pid1_autonomy:-yolo}}"
  WORKCELL_CONTAINER_MUTABILITY="${state_mutability:-${pid1_mutability:-ephemeral}}"
  export HOME CODEX_HOME TMPDIR WORKCELL_MODE CODEX_PROFILE WORKCELL_AGENT_AUTONOMY WORKCELL_CONTAINER_MUTABILITY
}

sanitize_development_env() {
  unset BASH_ENV
  unset ENV
  unset CLAUDE_CONFIG_DIR
  unset DISABLE_AUTOUPDATER
  unset NODE_OPTIONS
  unset NODE_PATH
  unset NODE_EXTRA_CA_CERTS
  unset npm_config_userconfig
  unset NPM_CONFIG_USERCONFIG
  unset LD_AUDIT
  unset LD_LIBRARY_PATH
  unset SSL_CERT_FILE
  unset SSL_CERT_DIR
  workcell_sanitize_git_runtime_env
  export LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so
  export PATH="${TRUSTED_PATH}"
}

emit_session_assurance_notice() {
  local assurance=""

  if [[ "${WORKCELL_SESSION_ASSURANCE_NOTICE_EMITTED:-0}" == "1" ]]; then
    return 0
  fi

  assurance="$(workcell_runtime_state_value WORKCELL_SESSION_ASSURANCE || true)"
  case "${assurance}" in
    lower-assurance-control-plane-vcs)
      echo "Workcell warning: this session intentionally exposed readonly workspace control-plane paths for Git VCS operations. Treat workspace control-plane contents as lower-assurance until container exit." >&2
      export WORKCELL_SESSION_ASSURANCE_NOTICE_EMITTED=1
      ;;
    lower-assurance-package-mutation)
      echo "Workcell warning: this session previously ran package-manager mutations as root. In-container control-plane integrity is now lower-assurance until container exit." >&2
      export WORKCELL_SESSION_ASSURANCE_NOTICE_EMITTED=1
      ;;
  esac
}

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

sanitize_development_env
pin_runtime_env
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
