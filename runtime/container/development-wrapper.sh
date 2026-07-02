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
  COPILOT_HOME="${HOME}/.copilot"
  COPILOT_CACHE_HOME="${HOME}/.cache/github-copilot"
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
  export HOME CODEX_HOME COPILOT_HOME COPILOT_CACHE_HOME TMPDIR WORKCELL_MODE CODEX_PROFILE WORKCELL_AGENT_AUTONOMY WORKCELL_CONTAINER_MUTABILITY
}

sanitize_development_env() {
  local env_name=""

  unset BASH_ENV
  unset ENV
  unset CLAUDE_CONFIG_DIR
  unset GH_CONFIG_DIR
  unset GH_HOST
  unset GH_TOKEN
  unset GITHUB_TOKEN
  unset PLAIN_DIFF
  unset USE_BUILTIN_RIPGREP
  for env_name in "${!COPILOT_@}" "${!GITHUB_COPILOT_@}"; do
    unset "${env_name}"
  done
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
  unset OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT
  unset OTEL_EXPORTER_OTLP_ENDPOINT
  unset OTEL_EXPORTER_OTLP_HEADERS
  unset OTEL_EXPORTER_OTLP_PROTOCOL
  unset OTEL_EXPORTER_OTLP_TIMEOUT
  unset OTEL_EXPORTER_OTLP_TRACES_ENDPOINT
  unset OTEL_EXPORTER_OTLP_TRACES_HEADERS
  unset OTEL_EXPORTER_OTLP_TRACES_PROTOCOL
  unset OTEL_EXPORTER_OTLP_TRACES_TIMEOUT
  unset OTEL_EXPORTER_OTLP_METRICS_ENDPOINT
  unset OTEL_EXPORTER_OTLP_METRICS_HEADERS
  unset OTEL_EXPORTER_OTLP_METRICS_PROTOCOL
  unset OTEL_EXPORTER_OTLP_METRICS_TIMEOUT
  unset OTEL_EXPORTER_OTLP_LOGS_ENDPOINT
  unset OTEL_EXPORTER_OTLP_LOGS_HEADERS
  unset OTEL_EXPORTER_OTLP_LOGS_PROTOCOL
  unset OTEL_EXPORTER_OTLP_LOGS_TIMEOUT
  unset OTEL_RESOURCE_ATTRIBUTES
  unset OTEL_TRACES_EXPORTER
  unset OTEL_METRICS_EXPORTER
  unset OTEL_LOGS_EXPORTER
  workcell_sanitize_git_runtime_env
  export LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so
  export PATH="${TRUSTED_PATH}"
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
    /usr/local/libexec/workcell/real/copilot \
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
    codex | claude | copilot | gemini)
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

reject_protected_runtime_arguments() {
  local command_path="${1:-}"
  local arg=""
  local protected_match=""

  [[ -n "${command_path}" ]] || return 0
  shift || return 0

  for arg in "$@"; do
    [[ "${arg}" == */* ]] || continue
    protected_match="$(development_wrapper_protected_runtime_match "${arg}" || true)"
    if [[ -n "${protected_match}" ]]; then
      workcell_die "Workcell blocked direct protected runtime execution in development mode: ${arg}."
    fi
  done
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
reject_protected_runtime_arguments "$@"

exec "$@"
