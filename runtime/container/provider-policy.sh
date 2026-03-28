#!/usr/bin/env -S BASH_ENV= ENV= bash

workcell_die() {
  echo "$*" >&2
  exit 2
}

# Only the PID 1 entrypoint path may honor breakglass-style provider flags.
# provider-wrapper.sh always exports WORKCELL_WRAPPER_CONTEXT=1 so nested
# launches cannot re-enable those overrides after the entrypoint policy check.
provider_policy_allows_breakglass() {
  [[ "${WORKCELL_WRAPPER_CONTEXT:-0}" != "1" ]] &&
    [[ "$$" -eq 1 ]] &&
    [[ "${WORKCELL_MODE:-strict}" == "breakglass" ]]
}

effective_codex_profile() {
  local requested_profile="${WORKCELL_MODE:-strict}"

  case "${requested_profile}" in
    strict | build)
      printf '%s\n' "${requested_profile}"
      ;;
    *)
      printf 'strict\n'
      ;;
  esac
}

codex_config_override_is_blocked() {
  local value="$1"
  local key="${value%%=*}"
  local key_lower="${key,,}"

  case "${key_lower}" in
    profile | sandbox | sandbox_mode | sandbox_permissions | web_search | approval_policy | project_doc_fallback_filenames | project_root_markers | mcp* | shell_environment_policy | shell_environment_policy.* | sandbox_workspace_write | sandbox_workspace_write.* | profiles.*.sandbox_mode | profiles.*.approval_policy | profiles.*.web_search | profiles.*.shell_environment_policy | profiles.*.shell_environment_policy.* | profiles.*.sandbox_workspace_write | profiles.*.sandbox_workspace_write.*)
      return 0
      ;;
  esac

  return 1
}

reject_unsafe_codex_args() {
  local expect_value=""
  local arg
  local saw_command=0

  provider_policy_allows_breakglass && return 0

  for arg in "$@"; do
    if [[ -n "${expect_value}" ]]; then
      case "${expect_value}" in
        profile)
          [[ "${arg}" != "$(effective_codex_profile)" ]] && workcell_die "Workcell blocked unsafe Codex override: --profile"
          ;;
        cd)
          workcell_die "Workcell blocked unsafe Codex override: --cd"
          ;;
        sandbox)
          [[ "${arg}" == "danger-full-access" ]] && workcell_die "Workcell blocked unsafe Codex override: remove danger-full-access outside breakglass."
          ;;
        config)
          codex_config_override_is_blocked "${arg}" && workcell_die "Workcell blocked unsafe Codex config override: ${arg%%=*}"
          ;;
      esac
      expect_value=""
      continue
    fi

    if [[ "${saw_command}" -eq 0 ]] && [[ "${arg}" != -* ]]; then
      saw_command=1
      if [[ "${AGENT_UI:-cli}" != "gui" ]]; then
        case "${arg}" in
          app | app-server | cloud | mcp | sandbox)
            workcell_die "Workcell blocked unsupported Codex CLI subcommand outside the managed GUI path: ${arg}"
            ;;
        esac
      fi
      continue
    fi

    case "${arg}" in
      --dangerously-bypass-approvals-and-sandbox | --search | --add-dir | --remote | --full-auto | -a | --ask-for-approval | --enable | --disable)
        workcell_die "Workcell blocked unsafe Codex override: ${arg}"
        ;;
      -p | --profile)
        expect_value="profile"
        ;;
      --cd)
        expect_value="cd"
        ;;
      --ask-for-approval=*)
        workcell_die "Workcell blocked unsafe Codex override: --ask-for-approval"
        ;;
      --add-dir=* | --remote=* | --enable=* | --disable=*)
        workcell_die "Workcell blocked unsafe Codex override: ${arg%%=*}"
        ;;
      --cd=*)
        workcell_die "Workcell blocked unsafe Codex override: --cd"
        ;;
      --profile=*)
        [[ "${arg#--profile=}" != "$(effective_codex_profile)" ]] && workcell_die "Workcell blocked unsafe Codex override: --profile"
        ;;
      -s | --sandbox)
        expect_value="sandbox"
        ;;
      --sandbox=danger-full-access)
        workcell_die "Workcell blocked unsafe Codex override: ${arg}"
        ;;
      -c | --config)
        expect_value="config"
        ;;
      --config=*)
        codex_config_override_is_blocked "${arg#--config=}" && workcell_die "Workcell blocked unsafe Codex config override: ${arg#--config=}"
        ;;
    esac
  done
}

reject_unsafe_claude_args() {
  local arg
  local saw_command=0

  provider_policy_allows_breakglass && return 0

  for arg in "$@"; do
    if [[ "${saw_command}" -eq 0 ]] && [[ "${arg}" != -* ]]; then
      saw_command=1
      case "${arg}" in
        install | update)
          workcell_die "Workcell blocked Claude lifecycle command: ${arg}"
          ;;
      esac
      continue
    fi

    case "${arg}" in
      --dangerously-skip-permissions | --allow-dangerously-skip-permissions | --add-dir | --allowedTools | --mcp-config | --plugin-dir | --settings | --setting-sources | --system-prompt | --append-system-prompt)
        workcell_die "Workcell blocked unsafe Claude override: ${arg}"
        ;;
      --permission-mode)
        workcell_die "Workcell blocked Claude autonomy override: use the host workcell --agent-autonomy option instead."
        ;;
      --permission-mode=*)
        workcell_die "Workcell blocked Claude autonomy override: use the host workcell --agent-autonomy option instead."
        ;;
      --add-dir=* | --allowedTools=* | --mcp-config=* | --plugin-dir=* | --settings=* | --setting-sources=* | --system-prompt=* | --append-system-prompt=*)
        workcell_die "Workcell blocked unsafe Claude override: ${arg%%=*}"
        ;;
    esac
  done
}

reject_unsafe_gemini_args() {
  local expect_value=""
  local arg
  local arg_lower=""

  provider_policy_allows_breakglass && return 0

  for arg in "$@"; do
    if [[ -n "${expect_value}" ]]; then
      case "${expect_value}" in
        approval-mode)
          workcell_die "Workcell blocked Gemini autonomy override: use the host workcell --agent-autonomy option instead."
          ;;
      esac
      expect_value=""
      continue
    fi

    arg_lower="${arg,,}"
    case "${arg_lower}" in
      --*dangerously* | --*bypass*permission* | --sandbox | --sandbox=* | --add-dir | --add-dir=* | -y | --yolo)
        workcell_die "Workcell blocked unsafe Gemini override: ${arg}"
        ;;
      --approval-mode)
        expect_value="approval-mode"
        ;;
      --approval-mode=*)
        workcell_die "Workcell blocked Gemini autonomy override: use the host workcell --agent-autonomy option instead."
        ;;
    esac
  done
}

# entrypoint.sh validates the full provider command before launch. After that,
# provider-wrapper.sh calls the per-provider reject helpers directly because it
# has already fixed the launch target and only needs to re-check user argv.
validate_command_args() {
  local expected_command="$1"

  [[ $# -gt 0 ]] || return 0
  shift

  if [[ $# -eq 0 ]]; then
    return 0
  fi

  if [[ "$1" != "${expected_command}" ]]; then
    workcell_die "Workcell blocked non-provider command: $1 (use the host launcher --allow-arbitrary-command path only for lower-assurance boundary debugging)."
  fi

  case "${expected_command}" in
    codex)
      reject_unsafe_codex_args "${@:2}"
      ;;
    claude)
      reject_unsafe_claude_args "${@:2}"
      ;;
    gemini)
      reject_unsafe_gemini_args "${@:2}"
      ;;
  esac
}
