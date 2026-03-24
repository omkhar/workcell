#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

AGENT_NAME="${WORKCELL_LAUNCH_TARGET:-${0##*/}}"
TRUSTED_PATH="/usr/local/bin:/usr/local/sbin:/usr/bin:/usr/sbin:/bin:/sbin"
export WORKCELL_WRAPPER_CONTEXT=1
export ADAPTER_ROOT="/opt/workcell/adapters"

pid1_env_value() {
  local key="$1"

  [[ -r /proc/1/environ ]] || return 1
  tr '\0' '\n' </proc/1/environ | sed -n "s/^${key}=//p" | head -n1
}

pin_runtime_env() {
  local pid1_mode=""
  local pid1_profile=""
  local pid1_autonomy=""

  HOME=/state/agent-home
  CODEX_HOME="${HOME}/.codex"
  TMPDIR=/state/tmp
  pid1_mode="$(pid1_env_value WORKCELL_MODE || true)"
  pid1_profile="$(pid1_env_value CODEX_PROFILE || true)"
  pid1_autonomy="$(pid1_env_value WORKCELL_AGENT_AUTONOMY || true)"
  WORKCELL_MODE="${pid1_mode:-strict}"
  CODEX_PROFILE="${pid1_profile:-${WORKCELL_MODE}}"
  WORKCELL_AGENT_AUTONOMY="${pid1_autonomy:-yolo}"
  export HOME CODEX_HOME TMPDIR WORKCELL_MODE CODEX_PROFILE WORKCELL_AGENT_AUTONOMY
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

loader_path() {
  case "$(uname -m)" in
    x86_64)
      printf '%s\n' /lib64/ld-linux-x86-64.so.2
      ;;
    aarch64 | arm64)
      printf '%s\n' /lib/ld-linux-aarch64.so.1
      ;;
    *)
      workcell_die "Unsupported architecture for Workcell provider loader."
      ;;
  esac
}

# shellcheck disable=SC1091
# shellcheck source=provider-policy.sh
source /usr/local/libexec/workcell/provider-policy.sh
# shellcheck disable=SC1091
# shellcheck source=home-control-plane.sh
source /usr/local/libexec/workcell/home-control-plane.sh

sanitize_provider_env
pin_runtime_env
mkdir -p "${TMPDIR}"
seed_agent_home "${AGENT_NAME}"

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
    reject_unsafe_codex_args "$@"
    exec "$(loader_path)" /usr/local/libexec/workcell/real/codex "${MANAGED_AUTONOMY_ARGS[@]}" "$@"
    ;;
  claude)
    reject_unsafe_claude_args "$@"
    exec "$(loader_path)" \
      /usr/local/libexec/workcell/real/node \
      /opt/workcell/providers/node_modules/@anthropic-ai/claude-code/cli.js \
      "${MANAGED_AUTONOMY_ARGS[@]}" \
      "$@"
    ;;
  gemini)
    reject_unsafe_gemini_args "$@"
    exec "$(loader_path)" \
      /usr/local/libexec/workcell/real/node \
      /opt/workcell/providers/node_modules/@google/gemini-cli/dist/index.js \
      "${MANAGED_AUTONOMY_ARGS[@]}" \
      "$@"
    ;;
  *)
    workcell_die "Unsupported provider wrapper target: ${AGENT_NAME}"
    ;;
esac
