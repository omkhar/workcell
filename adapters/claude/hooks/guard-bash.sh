#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

WORKCELL_HOOK_PAYLOAD="$(cat)"

fail() {
  echo "BLOCKED: $*" >&2
  exit 2
}

grep_command() {
  local pattern="$1"
  printf '%s\n' "${command_lower}" | grep -Eq -- "${pattern}"
}

grep_dequoted_command() {
  local pattern="$1"
  printf '%s\n' "${command_lower_dequoted}" | grep -Eq -- "${pattern}"
}

grep_deescaped_command() {
  local pattern="$1"
  printf '%s\n' "${command_lower_deescaped}" | grep -Eq -- "${pattern}"
}

token_is_assignment_word() {
  local token="$1"
  [[ "${token}" =~ ^[[:alpha:]_][[:alnum:]_]*=.*$ ]]
}

token_is_redirection_word() {
  local token="$1"
  local redirection_regex='^[0-9]*[<>].*$'
  [[ "${token}" =~ ${redirection_regex} ]]
}

token_has_glob_pattern() {
  local token="$1"

  [[ "${token}" == *'*'* ]] && return 0
  [[ "${token}" == *'?'* ]] && return 0
  case "${token}" in
    "[" | "[[")
      return 1
      ;;
  esac
  [[ "${token}" == *'['* ]]
}

command_has_glob_command_word() {
  local input="$1"
  local token=""
  local context="command_start"
  local quote_mode="none"
  local escape_next=0
  local i char

  for ((i = 0; i < ${#input}; i++)); do
    char="${input:i:1}"

    if [[ "${quote_mode}" == "single" ]]; then
      if [[ "${char}" == "'" ]]; then
        quote_mode="none"
      else
        token+="${char}"
      fi
      continue
    fi

    if [[ "${quote_mode}" == "double" ]]; then
      if ((escape_next)); then
        token+="${char}"
        escape_next=0
        continue
      fi
      case "${char}" in
        '"')
          quote_mode="none"
          ;;
        "\\")
          escape_next=1
          ;;
        *)
          token+="${char}"
          ;;
      esac
      continue
    fi

    if ((escape_next)); then
      token+="${char}"
      escape_next=0
      continue
    fi

    case "${char}" in
      "'")
        quote_mode="single"
        ;;
      '"')
        quote_mode="double"
        ;;
      "\\")
        escape_next=1
        ;;
      ' ' | $'\t' | $'\r' | $'\n')
        if [[ -n "${token}" ]]; then
          if [[ "${context}" == "command_start" ]]; then
            if token_is_assignment_word "${token}" ||
              token_is_redirection_word "${token}"; then
              token=""
              continue
            fi
            if token_has_glob_pattern "${token}"; then
              return 0
            fi
          fi
          context="arguments"
          token=""
        fi
        ;;
      ';' | '&' | '|' | '(' | ')')
        if [[ -n "${token}" ]]; then
          if [[ "${context}" == "command_start" ]]; then
            if token_is_assignment_word "${token}" ||
              token_is_redirection_word "${token}"; then
              token=""
            elif token_has_glob_pattern "${token}"; then
              return 0
            else
              token=""
            fi
          else
            token=""
          fi
        fi
        context="command_start"
        ;;
      *)
        token+="${char}"
        ;;
    esac
  done

  if [[ -n "${token}" ]] && [[ "${context}" == "command_start" ]]; then
    if token_is_assignment_word "${token}" ||
      token_is_redirection_word "${token}"; then
      return 1
    fi
    if token_has_glob_pattern "${token}"; then
      return 0
    fi
  fi

  return 1
}

command_has_source_command_word() {
  local input="$1"
  local token=""
  local context="command_start"
  local quote_mode="none"
  local escape_next=0
  local i char

  for ((i = 0; i < ${#input}; i++)); do
    char="${input:i:1}"

    if [[ "${quote_mode}" == "single" ]]; then
      if [[ "${char}" == "'" ]]; then
        quote_mode="none"
      else
        token+="${char}"
      fi
      continue
    fi

    if [[ "${quote_mode}" == "double" ]]; then
      if ((escape_next)); then
        token+="${char}"
        escape_next=0
        continue
      fi
      case "${char}" in
        '"')
          quote_mode="none"
          ;;
        "\\")
          escape_next=1
          ;;
        *)
          token+="${char}"
          ;;
      esac
      continue
    fi

    if ((escape_next)); then
      token+="${char}"
      escape_next=0
      continue
    fi

    case "${char}" in
      "'")
        quote_mode="single"
        ;;
      '"')
        quote_mode="double"
        ;;
      "\\")
        escape_next=1
        ;;
      ' ' | $'\t' | $'\r' | $'\n')
        if [[ -n "${token}" ]]; then
          if [[ "${context}" == "command_start" ]]; then
            if token_is_assignment_word "${token}" ||
              token_is_redirection_word "${token}"; then
              token=""
              continue
            fi
            if [[ "${token}" == "source" ]] || [[ "${token}" == "." ]]; then
              return 0
            fi
          fi
          context="arguments"
          token=""
        fi
        ;;
      ';' | '&' | '|' | '(' | ')')
        if [[ -n "${token}" ]]; then
          if [[ "${context}" == "command_start" ]]; then
            if token_is_assignment_word "${token}" ||
              token_is_redirection_word "${token}"; then
              token=""
            elif [[ "${token}" == "source" ]] || [[ "${token}" == "." ]]; then
              return 0
            else
              token=""
            fi
          else
            token=""
          fi
        fi
        context="command_start"
        ;;
      *)
        token+="${char}"
        ;;
    esac
  done

  if [[ -n "${token}" ]] && [[ "${context}" == "command_start" ]]; then
    if token_is_assignment_word "${token}" ||
      token_is_redirection_word "${token}"; then
      return 1
    fi
    if [[ "${token}" == "source" ]] || [[ "${token}" == "." ]]; then
      return 0
    fi
  fi

  return 1
}

command="$(printf '%s' "${WORKCELL_HOOK_PAYLOAD}" | jq -er '.tool_input.command | select(type == "string")' 2>/dev/null || true)"
[[ -n "${command}" ]] || exit 0

command_lower="${command,,}"
command_lower_dequoted="$(printf '%s' "${command_lower}" | tr -d "\"'")"
command_lower_deescaped="$(printf '%s' "${command_lower_dequoted}" | tr -d "\\")"
command_substitution_marker="\$("
parameter_expansion_marker="\${"
ansi_c_quote_marker="\$'"
localized_quote_marker="\$\""
process_substitution_in_marker='<('
process_substitution_out_marker='>('
shell_variable_expansion_regex="\\\$([[:alnum:]_]+|[#?*@!\$-])"
home_control_regex="(^|[[:space:]'\";|&])(~|\$home|/state/agent-home)/(\\.claude|\\.codex|\\.gemini)(/|[[:space:]'\";|&]|$)"
mcp_home_control_regex="(^|[[:space:]'\";|&])(~|\$home|/state/agent-home)/\\.mcp\\.json([[:space:]'\";|&]|$)"

if grep_command '(^|[^[:alnum:]_./-])(git|/usr/bin/git|/usr/local/libexec/workcell/git|/usr/local/libexec/workcell/core/git|/usr/local/libexec/workcell/real/git)([^[:alnum:]_./-]|$)'; then
  if [[ "${command_lower}" == *"--no-verify"* ]] ||
    grep_command '(^|[[:space:]'"'"'";|&])commit.*[[:space:]'"'"'";|&]-n([[:space:]'"'"'";|&]|$)' ||
    grep_command '(^|[[:space:]'"'"'";|&])-c[[:space:]]+(core\.hookspath|core\.worktree|include\.path|includeif\.[^=[:space:]]+\.path)=' ||
    grep_command '--config-env=(core\.hookspath|core\.worktree|include\.path|includeif\.[^=[:space:]]+\.path)=' ||
    grep_command '(^|[[:space:]'"'"'";|&])(git_config_(count|key_[0-9]+|value_[0-9]+|parameters)|git_dir|git_work_tree|git_common_dir)=' ||
    grep_command '(^|[[:space:]'"'"'";|&])--git-dir(=|[[:space:]'"'"'";|&])' ||
    grep_command '(^|[[:space:]'"'"'";|&])--work-tree(=|[[:space:]'"'"'";|&])'; then
    fail "Do not bypass git hooks or Workcell git control-plane protections."
  fi
fi

if { grep_command '(^|[^[:alnum:]_./-])(claude)([^[:alnum:]_./-]|$)' ||
  grep_dequoted_command '(^|[^[:alnum:]_./-])(claude)([^[:alnum:]_./-]|$)' ||
  grep_deescaped_command '(^|[^[:alnum:]_./-])(claude)([^[:alnum:]_./-]|$)'; } &&
  { [[ "${command_lower}" == *"--dangerously-skip-permissions"* ]] ||
    [[ "${command_lower}" == *"--allow-dangerously-skip-permissions"* ]] ||
    [[ "${command_lower}" == *"--allowedtools"* ]] ||
    [[ "${command_lower}" == *"--mcp-config"* ]] ||
    [[ "${command_lower}" == *"--plugin-dir"* ]] ||
    [[ "${command_lower}" == *"--settings"* ]] ||
    [[ "${command_lower}" == *"--setting-sources"* ]] ||
    [[ "${command_lower}" == *"--system-prompt"* ]] ||
    [[ "${command_lower}" == *"--append-system-prompt"* ]] ||
    [[ "${command_lower}" == *"--permission-mode=bypasspermissions"* ]] ||
    [[ "${command_lower}" == *"--permission-mode=dontask"* ]]; }; then
  fail "Do not launch nested Claude processes with unsafe overrides inside Workcell."
fi

if [[ "${command}" == *"${command_substitution_marker}"* ]] ||
  [[ "${command}" == *'`'* ]] ||
  [[ "${command}" == *"${parameter_expansion_marker}"* ]] ||
  [[ "${command}" == *"${ansi_c_quote_marker}"* ]] ||
  [[ "${command}" == *"${localized_quote_marker}"* ]] ||
  [[ "${command}" == *"${process_substitution_in_marker}"* ]] ||
  [[ "${command}" == *"${process_substitution_out_marker}"* ]]; then
  fail "Do not use advanced shell expansion syntax from the Claude Bash tool inside Workcell."
fi

if command_has_glob_command_word "${command}"; then
  fail "Do not use shell glob expansion for command names from the Claude Bash tool inside Workcell."
fi

if grep_command '(^|[[:space:]'"'"'";|&])(eval)([[:space:]'"'"'";|&]|$)' ||
  grep_command "${shell_variable_expansion_regex}"; then
  fail "Do not use eval or shell variable expansion from the Claude Bash tool inside Workcell."
fi

if grep_dequoted_command '(^|[^[:alnum:]_./-])(codex|claude|gemini|/usr/local/libexec/workcell/core/codex|/usr/local/libexec/workcell/core/claude|/usr/local/libexec/workcell/core/gemini|/usr/local/libexec/workcell/real/codex|/opt/workcell/providers/node_modules/\.bin/claude|/opt/workcell/providers/node_modules/\.bin/gemini|/opt/workcell/providers/node_modules/@anthropic-ai/claude-code/cli\.js|/opt/workcell/providers/node_modules/@google/gemini-cli/dist/index\.js)([^[:alnum:]_./-]|$)' ||
  grep_deescaped_command '(^|[^[:alnum:]_./-])(codex|claude|gemini|/usr/local/libexec/workcell/core/codex|/usr/local/libexec/workcell/core/claude|/usr/local/libexec/workcell/core/gemini|/usr/local/libexec/workcell/real/codex|/opt/workcell/providers/node_modules/\.bin/claude|/opt/workcell/providers/node_modules/\.bin/gemini|/opt/workcell/providers/node_modules/@anthropic-ai/claude-code/cli\.js|/opt/workcell/providers/node_modules/@google/gemini-cli/dist/index\.js)([^[:alnum:]_./-]|$)'; then
  fail "Do not launch nested coding-agent CLIs from the Claude Bash tool inside Workcell."
fi

if command_has_source_command_word "${command}"; then
  fail "Do not source nested shell scripts from the Claude Bash tool inside Workcell."
fi

if grep_command '(^|[[:space:]'"'"'";|&])((/usr/bin/env[[:space:]]+)?(bash|sh|zsh|dash|ksh|fish)|/bin/(bash|sh|zsh|dash|ksh)|/usr/bin/(bash|sh|zsh|dash|ksh))([[:space:]'"'"'";|&]|$)' &&
  ! grep_command '(^|[[:space:]'"'"'";|&])((/usr/bin/env[[:space:]]+)?(bash|sh|zsh|dash|ksh|fish)|/bin/(bash|sh|zsh|dash|ksh)|/usr/bin/(bash|sh|zsh|dash|ksh))[[:space:]]+(-c|-lc|--help|--version)([[:space:]'"'"'";|&]|$)'; then
  fail "Do not execute nested shell scripts from the Claude Bash tool inside Workcell."
fi

if grep_command '(^|[[:space:];|&])(rm)([[:space:]'"'"'";|&]|$)' &&
  (grep_command '(^|[[:space:]'"'"'";|&])-[[:alpha:]]*r[[:alpha:]]*([[:space:]'"'"'";|&]|$)' ||
    [[ "${command_lower}" == *"--recursive"* ]]) &&
  (grep_command '(^|[[:space:]'"'"'";|&])-[[:alpha:]]*f[[:alpha:]]*([[:space:]'"'"'";|&]|$)' ||
    [[ "${command_lower}" == *"--force"* ]]); then
  fail "Use trash or a targeted delete instead of rm -rf."
fi

if grep_command 'git.*push.*(main|master)([[:space:]'"'"'";|&]|$)'; then
  fail "Use a feature branch, not a direct push to main or master."
fi

if grep_command '(^|[[:space:]'"'"'";|&])([^[:space:]'"'"'";|&]+/)?(agents\.md|claude\.md|gemini\.md|\.mcp\.json)([[:space:]'"'"'";|&]|$)'; then
  fail "Do not read or modify workspace control files from Bash."
fi

if grep_command '(^|[[:space:]'"'"'";|&])([^[:space:]'"'"'";|&]+/)?(\.claude|\.codex|\.gemini|\.cursor|\.idea|\.vscode|\.zed)(/|[[:space:]'"'"'";|&]|$)'; then
  fail "Do not read or modify workspace control directories from Bash."
fi

if grep_command "${home_control_regex}"; then
  fail "Do not read or modify Workcell home control directories from Bash."
fi

if grep_command "${mcp_home_control_regex}"; then
  fail "Do not read or modify Workcell home control files from Bash."
fi
