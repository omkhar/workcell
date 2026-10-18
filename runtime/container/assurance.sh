#!/usr/bin/env -S BASH_ENV= ENV= bash
# shellcheck shell=bash

workcell_container_assurance() {
  local mutability="${1:-}"

  case "${mutability}" in
    readonly)
      printf 'managed-readonly\n'
      ;;
    ephemeral)
      printf 'managed-mutable\n'
      ;;
    *)
      echo "Unsupported Workcell container mutability for assurance mapping: ${mutability}" >&2
      return 1
      ;;
  esac
}

workcell_autonomy_assurance() {
  local autonomy="${1:-}"

  case "${autonomy}" in
    yolo)
      printf 'managed-yolo\n'
      ;;
    prompt)
      printf 'lower-assurance-prompt-autonomy\n'
      ;;
    *)
      echo "Unsupported Workcell agent autonomy for assurance mapping: ${autonomy}" >&2
      return 1
      ;;
  esac
}

# workcell_adapter_assurance ID TIER ALLOW prints the assurance of adapter ID
# from its manifest tier. An uncertified adapter runs only with
# --allow-uncertified-agent (ALLOW=1). The option with a certified adapter, and
# any other tier, fail closed.
workcell_adapter_assurance() {
  local id="${1:-}"
  local tier="${2:-}"
  local allow="${3:-}"

  case "${tier}:${allow}" in
    certified:0)
      printf 'managed-certified-adapter\n'
      ;;
    uncertified:1)
      printf 'lower-assurance-uncertified-adapter\n'
      ;;
    uncertified:0)
      echo "Workcell adapter ${id} is uncertified: no review certified its CLI. Add --allow-uncertified-agent to run it as lower assurance." >&2
      return 1
      ;;
    certified:1)
      echo "--allow-uncertified-agent applies only to an uncertified adapter; ${id} is certified." >&2
      return 1
      ;;
    *)
      echo "Unsupported Workcell adapter tier for assurance mapping: ${id} tier=${tier} allow=${allow}" >&2
      return 1
      ;;
  esac
}

workcell_codex_rules_assurance() {
  local mutability="${1:-}"

  case "${mutability}" in
    readonly | "")
      printf 'managed-immutable-rules\n'
      ;;
    session)
      printf 'lower-assurance-session-rules\n'
      ;;
    *)
      echo "Unsupported Workcell Codex rules mutability for assurance mapping: ${mutability}" >&2
      return 1
      ;;
  esac
}

workcell_effective_codex_rules_mutability() {
  local configured_mutability="${1:-readonly}"
  local autonomy="${2:-yolo}"
  local session_assurance="${3:-}"

  case "${configured_mutability}" in
    "" | readonly | session) ;;
    *)
      echo "Unsupported Workcell Codex rules mutability for effective mapping: ${configured_mutability}" >&2
      return 1
      ;;
  esac

  case "${autonomy}" in
    "" | yolo | prompt) ;;
    *)
      echo "Unsupported Workcell agent autonomy for effective Codex rules mapping: ${autonomy}" >&2
      return 1
      ;;
  esac

  if [[ "${configured_mutability}" == "session" ]]; then
    printf 'session\n'
    return 0
  fi

  if [[ "${autonomy}" == "prompt" ]]; then
    printf 'session\n'
    return 0
  fi

  if [[ "${session_assurance}" == "lower-assurance-package-mutation" ]]; then
    printf 'session\n'
    return 0
  fi

  printf 'readonly\n'
}

workcell_effective_codex_rules_assurance() {
  local effective_mutability=""

  effective_mutability="$(workcell_effective_codex_rules_mutability "$@")" || return 1
  workcell_codex_rules_assurance "${effective_mutability}"
}

emit_session_assurance_notice() {
  local assurance=""
  local emitted=0

  if [[ "${WORKCELL_SESSION_ASSURANCE_NOTICE_EMITTED:-0}" == "1" ]]; then
    return 0
  fi

  # Reasons can stack into a '+'-joined label, so match each reason as a
  # substring and fire every applicable notice; a single ack still emits exactly
  # its one notice.
  assurance="$(workcell_runtime_state_value WORKCELL_SESSION_ASSURANCE || true)"
  case "${assurance}" in
    *control-plane-vcs*)
      echo "Workcell warning: this session intentionally exposed readonly workspace control-plane paths for Git VCS operations. Treat workspace control-plane contents as lower-assurance until container exit." >&2
      emitted=1
      ;;
  esac
  case "${assurance}" in
    *repo-mcp*)
      echo "Workcell warning: this session intentionally honored repo-defined MCP server configs, expanding the agent's outbound reach. Treat the session as lower-assurance until container exit." >&2
      emitted=1
      ;;
  esac
  case "${assurance}" in
    *uncertified-adapter*)
      echo "Workcell warning: this session runs an uncertified adapter. No review certified its CLI, and only its manifest flag allowlist limits its arguments. Treat the session as lower-assurance until container exit." >&2
      emitted=1
      ;;
  esac
  case "${assurance}" in
    *package-mutation*)
      echo "Workcell warning: this session previously ran package-manager mutations as root. In-container control-plane integrity is now lower-assurance until container exit." >&2
      emitted=1
      ;;
  esac
  if [[ "${emitted}" -eq 1 ]]; then
    export WORKCELL_SESSION_ASSURANCE_NOTICE_EMITTED=1
  fi
  return 0
}
