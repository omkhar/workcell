#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

AGENT_NAME="${WORKCELL_LAUNCH_TARGET:-${0##*/}}"
TRUSTED_PATH="/usr/local/bin:/usr/local/sbin:/usr/bin:/usr/sbin:/bin:/sbin"
export WORKCELL_WRAPPER_CONTEXT=1
export ADAPTER_ROOT="/opt/workcell/adapters"

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

sanitize_provider_env() {
  unset BASH_ENV
  unset ENV
  unset NODE_OPTIONS
  unset NODE_PATH
  unset NODE_EXTRA_CA_CERTS
  unset npm_config_userconfig
  unset NPM_CONFIG_USERCONFIG
  unset LD_AUDIT
  unset LD_LIBRARY_PATH
  unset SSL_CERT_FILE
  unset SSL_CERT_DIR
  export NODE_NO_WARNINGS=1
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
    lower-assurance-package-mutation)
      echo "Workcell warning: this session previously ran package-manager mutations as root. In-container control-plane integrity is now lower-assurance until container exit." >&2
      export WORKCELL_SESSION_ASSURANCE_NOTICE_EMITTED=1
      ;;
  esac
}

emit_codex_rules_mutability_notice() {
  local configured_mutability=""
  local effective_mutability=""
  local effective_reason=""

  if [[ "${AGENT_NAME}" != "codex" ]]; then
    return 0
  fi

  configured_mutability="$(workcell_codex_rules_mutability)"
  effective_mutability="$(workcell_current_effective_codex_rules_mutability)"
  effective_reason="$(workcell_codex_rules_effective_reason)"

  if [[ "${effective_mutability}" == "${configured_mutability}" ]]; then
    return 0
  fi

  case "${effective_reason}" in
    prompt-autonomy)
      echo "Workcell note: Codex prompt autonomy uses session-local execpolicy rules until this container exits." >&2
      echo "WORKCELL_EVENT codex-rules-mutability effective=session reason=prompt-autonomy" >&2
      ;;
    package-mutation)
      echo "Workcell warning: package-manager mutation forced session-local Codex execpolicy rules for the remainder of this already-lower-assurance session." >&2
      echo "WORKCELL_EVENT codex-rules-mutability effective=session reason=package-mutation" >&2
      ;;
  esac
}

# shellcheck disable=SC1091
# shellcheck source=assurance.sh
source /usr/local/libexec/workcell/assurance.sh
# shellcheck disable=SC1091
# shellcheck source=provider-policy.sh
source /usr/local/libexec/workcell/provider-policy.sh
# shellcheck disable=SC1091
# shellcheck source=home-control-plane.sh
source /usr/local/libexec/workcell/home-control-plane.sh
# shellcheck disable=SC1091
# shellcheck source=runtime-user.sh
source /usr/local/libexec/workcell/runtime-user.sh

sanitize_provider_env
pin_runtime_env
mkdir -p "${TMPDIR}"
if workcell_should_reexec_as_runtime_user; then
  workcell_reexec_as_runtime_user /usr/local/libexec/workcell/provider-wrapper.sh "$@"
fi
seed_agent_home "${AGENT_NAME}"
emit_session_assurance_notice
emit_codex_rules_mutability_notice

codex_args_include_profile() {
  local arg=""
  local expect_value=0

  for arg in "$@"; do
    if [[ "${expect_value}" -eq 1 ]]; then
      return 0
    fi
    case "${arg}" in
      -p | --profile)
        expect_value=1
        ;;
      --profile=*)
        return 0
        ;;
    esac
  done

  return 1
}

case "${WORKCELL_AGENT_AUTONOMY}" in
  yolo | prompt) ;;
  *)
    workcell_die "Unsupported Workcell agent autonomy mode: ${WORKCELL_AGENT_AUTONOMY}"
    ;;
esac

# Managed autonomy flags stay ahead of provider subcommands. User-authored
# autonomy overrides are denied before exec, so there should be no conflicting
# later flag left in "$@" to outvote the host-selected mode.
declare -a MANAGED_AUTONOMY_ARGS=()

case "${AGENT_NAME}:${WORKCELL_AGENT_AUTONOMY}" in
  codex:yolo)
    MANAGED_AUTONOMY_ARGS=(--ask-for-approval never)
    ;;
  codex:prompt)
    MANAGED_AUTONOMY_ARGS=(--ask-for-approval on-request)
    ;;
  claude:yolo)
    MANAGED_AUTONOMY_ARGS=(--permission-mode bypassPermissions)
    ;;
  claude:prompt)
    MANAGED_AUTONOMY_ARGS=(--permission-mode default)
    ;;
  gemini:yolo)
    MANAGED_AUTONOMY_ARGS=(--approval-mode yolo)
    ;;
  gemini:prompt)
    MANAGED_AUTONOMY_ARGS=(--approval-mode default)
    ;;
esac

case "${AGENT_NAME}" in
  codex)
    declare -a MANAGED_CODEX_PROFILE_ARGS=()
    if ! codex_args_include_profile "$@"; then
      MANAGED_CODEX_PROFILE_ARGS=(--profile "${CODEX_PROFILE}")
    fi
    reject_unsafe_codex_args "$@"
    exec /usr/local/libexec/workcell/real/codex "${MANAGED_CODEX_PROFILE_ARGS[@]}" "${MANAGED_AUTONOMY_ARGS[@]}" "$@"
    ;;
  claude)
    reject_unsafe_claude_args "$@"
    exec /usr/local/libexec/workcell/real/node \
      /opt/workcell/providers/node_modules/@anthropic-ai/claude-code/cli.js \
      "${MANAGED_AUTONOMY_ARGS[@]}" \
      "$@"
    ;;
  gemini)
    reject_unsafe_gemini_args "$@"
    # Gemini CLI self-relaunch conflicts with Workcell's protected exec boundary.
    GEMINI_CLI_NO_RELAUNCH=1 exec /usr/local/libexec/workcell/real/node \
      /opt/workcell/providers/node_modules/@google/gemini-cli/dist/index.js \
      "${MANAGED_AUTONOMY_ARGS[@]}" \
      "$@"
    ;;
  *)
    workcell_die "Unsupported provider wrapper target: ${AGENT_NAME}"
    ;;
esac
