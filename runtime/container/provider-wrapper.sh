#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

AGENT_NAME="${WORKCELL_LAUNCH_TARGET:-${0##*/}}"
TRUSTED_PATH="/usr/local/bin:/usr/local/sbin:/usr/bin:/usr/sbin:/bin:/sbin"
WORKCELL_COPILOT_TOKEN_HANDOFF_CONTAINER_DIR="${WORKCELL_COPILOT_TOKEN_HANDOFF_CONTAINER_DIR:-/opt/workcell/copilot-token-handoff}"
copilot_token_handoff_consumed_file="${WORKCELL_COPILOT_TOKEN_HANDOFF_CONTAINER_DIR}/copilot-token-consumed"
WORKCELL_COPILOT_AUTH_REQUIRED=""
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
  WORKCELL_COPILOT_AUTH_REQUIRED="$(workcell_pid1_env_value WORKCELL_COPILOT_AUTH_REQUIRED || true)"
  case "${WORKCELL_COPILOT_AUTH_REQUIRED}" in
    0 | 1 | '') ;;
    *) WORKCELL_COPILOT_AUTH_REQUIRED="" ;;
  esac
  export HOME CODEX_HOME COPILOT_HOME COPILOT_CACHE_HOME TMPDIR WORKCELL_MODE CODEX_PROFILE WORKCELL_AGENT_AUTONOMY WORKCELL_CONTAINER_MUTABILITY WORKCELL_COPILOT_AUTH_REQUIRED
}

sanitize_provider_env() {
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
  export NODE_NO_WARNINGS=1
  export LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so
  export PATH="${TRUSTED_PATH}"
}

workcell_provider_parent_is_launcher() {
  local parent_exe=""
  local launcher_path=""

  parent_exe="$(readlink "/proc/${PPID}/exe" 2>/dev/null || true)"
  [[ -n "${parent_exe}" ]] || return 1

  for launcher_path in \
    /usr/local/libexec/workcell/core/launcher \
    /usr/local/libexec/workcell/core/git; do
    if [[ -e "${launcher_path}" && "${parent_exe}" -ef "${launcher_path}" ]]; then
      return 0
    fi
  done

  return 1
}

require_managed_provider_launch() {
  case "${AGENT_NAME}" in
    codex | claude | copilot | gemini) ;;
    *)
      workcell_die "Unsupported provider wrapper target: ${AGENT_NAME}"
      ;;
  esac

  if [[ "${WORKCELL_PROVIDER_LAUNCHER_AUTHORITY:-0}" != "1" ]] ||
    ! workcell_provider_parent_is_launcher; then
    workcell_die "Workcell blocked direct provider wrapper execution."
  fi
  unset WORKCELL_PROVIDER_LAUNCHER_AUTHORITY
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

# shellcheck source=runtime/container/assurance.sh
source /usr/local/libexec/workcell/assurance.sh
# shellcheck source=runtime/container/provider-policy.sh
source /usr/local/libexec/workcell/provider-policy.sh
# shellcheck source=runtime/container/home-control-plane.sh
source /usr/local/libexec/workcell/home-control-plane.sh
# shellcheck source=runtime/container/runtime-user.sh
source /usr/local/libexec/workcell/runtime-user.sh

sanitize_provider_env
pin_runtime_env
mkdir -p "${TMPDIR}"
if workcell_should_reexec_as_runtime_user; then
  workcell_reexec_as_runtime_user /usr/local/libexec/workcell/provider-wrapper.sh "$@"
fi
require_managed_provider_launch "$@"
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
    # A bare `--` ends option parsing: every following token is literal PROMPT
    # text, never an operator flag (e.g. `codex -- --profile breakglass` passes
    # "--profile breakglass" to the session as the prompt). Stop here so a
    # post-`--` `--profile` is NOT mistaken for an operator-supplied profile —
    # otherwise managed-profile injection would be skipped and the sandbox/
    # approval contract dropped. This mirrors codex_first_subcommand (above) and
    # reject_unsafe_codex_args (runtime/container/provider-policy.sh), which both
    # return/break at `--`.
    if [[ "${arg}" == "--" ]]; then
      return 1
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

# Resolve the Codex subcommand token from "$@" into CODEX_FIRST_SUBCOMMAND
# (empty for the default TUI: no subcommand, a bare prompt, or everything after
# `--`). Global value-taking flags consume their value so the real subcommand
# token is found, and `--*=*`/boolean flags (e.g. --version/--help) are skipped.
# Both managed-injection decisions below classify this single token.
# This list must cover EVERY value-taking top-level Codex global (from
# `codex --help`), and stay in lockstep with reject_unsafe_codex_args
# (runtime/container/provider-policy.sh): the policy must likewise consume or
# reject each of these values before its first-subcommand blocklist, or a flag
# value would be mistaken for the command token (e.g.
# `codex --local-provider ollama plugin`).
codex_first_subcommand() {
  CODEX_FIRST_SUBCOMMAND=""
  local arg="" skip_value=0
  for arg in "$@"; do
    if [[ "${skip_value}" -eq 1 ]]; then
      skip_value=0
      continue
    fi
    case "${arg}" in
      --)
        return 0
        ;;
      -c | --config | -m | --model | -i | --image | -C | --cd | \
        -a | --ask-for-approval | -s | --sandbox | -p | --profile | \
        --add-dir | --remote | --remote-auth-token-env | --local-provider | \
        --enable | --disable)
        skip_value=1
        ;;
      --*=* | -*) ;;
      *)
        CODEX_FIRST_SUBCOMMAND="${arg}"
        return 0
        ;;
    esac
  done
}

# Codex 0.134+ rejects the global --profile flag on non-runtime subcommands
# (e.g. `codex features`, `codex login`, `codex app-server`): it only applies to
# the runtime commands. Inject the managed --profile unless the resolved
# subcommand is a recognized non-runtime one (including aliases `a` for apply and
# `cloud-tasks` for cloud); runtime subcommands, the default TUI (empty token),
# and --version/--help all accept it. Call codex_first_subcommand first.
codex_managed_profile_applies() {
  case "${CODEX_FIRST_SUBCOMMAND}" in
    login | logout | plugin | mcp-server | app-server | remote-control | \
      completion | update | doctor | apply | a | cloud | cloud-tasks | \
      exec-server | execpolicy | features | help | debug)
      return 1
      ;;
    *)
      return 0
      ;;
  esac
}

# Map the active Workcell Codex profile to its sandbox_mode. Mirrors the
# sandbox_mode in the per-profile `<name>.config.toml` layer files; anything
# other than breakglass uses the workspace-write floor.
codex_profile_sandbox_mode() {
  case "${CODEX_PROFILE}" in
    breakglass) printf 'danger-full-access\n' ;;
    *) printf 'workspace-write\n' ;;
  esac
}

# Codex 0.139 rejects --profile on `app-server` (the managed GUI backend), so
# codex_managed_profile_applies skips profile injection there. app-server does
# honor `-c` root config overrides, so set MANAGED_CODEX_APP_SERVER_ARGS to the
# per-mode sandbox_mode override, giving GUI sessions the same sandbox layer the
# CLI gets from its profile. The approval policy still comes from the injected
# --ask-for-approval autonomy flag, which app-server accepts. Leaves the array
# untouched when the invocation is not app-server. Call codex_first_subcommand
# first.
codex_app_server_sandbox_args() {
  case "${CODEX_FIRST_SUBCOMMAND}" in
    app | app-server)
      MANAGED_CODEX_APP_SERVER_ARGS=(-c "sandbox_mode=\"$(codex_profile_sandbox_mode)\"")
      ;;
  esac
}

sanitize_gemini_sandbox_env() {
  unset GEMINI_SANDBOX
  unset GEMINI_SANDBOX_IMAGE
  unset GEMINI_SANDBOX_IMAGE_DEFAULT
  unset GEMINI_SANDBOX_PROXY_COMMAND
  unset BUILD_SANDBOX
  unset SANDBOX
  unset SANDBOX_FLAGS
  unset SANDBOX_MOUNTS
  unset SANDBOX_ENV
  unset SANDBOX_PORTS
  unset SANDBOX_SET_UID_GID
  unset SEATBELT_PROFILE
}

gemini_provider_script_path() {
  local package_json="/opt/workcell/providers/node_modules/@google/gemini-cli/package.json"
  local relative_path=""

  relative_path="$(jq -r '
    .bin
    | if type == "string" then .
      elif type == "object" then (.gemini // (to_entries | .[0].value))
      else empty
      end
  ' "${package_json}")"

  [[ -n "${relative_path}" && "${relative_path}" != "null" ]] || workcell_die "Unable to resolve Gemini provider entrypoint from ${package_json}."
  printf '/opt/workcell/providers/node_modules/@google/gemini-cli/%s\n' "${relative_path}"
}

workcell_read_copilot_handoff_file() {
  local token_file=""

  if [[ -r "${WORKCELL_RUNTIME_COPILOT_TOKEN_FILE_PATH}" ]]; then
    token_file="$(head -n1 "${WORKCELL_RUNTIME_COPILOT_TOKEN_FILE_PATH}")"
  fi
  unset WORKCELL_COPILOT_TOKEN_FILE
  printf '%s\n' "${token_file}"
}

workcell_mark_copilot_token_consumed() {
  if [[ -d "$(dirname "${copilot_token_handoff_consumed_file}")" ]]; then
    : >"${copilot_token_handoff_consumed_file}" ||
      workcell_die "Copilot auth token handoff consumed marker could not be written."
    chmod 0600 "${copilot_token_handoff_consumed_file}" || true
  fi
}

workcell_load_copilot_github_token() {
  local token=""
  local token_file=""

  token_file="$(workcell_read_copilot_handoff_file)"
  [[ -n "${token_file}" ]] || workcell_die "Copilot auth token handoff file is required."
  [[ -r "${token_file}" ]] || workcell_die "Copilot auth token handoff file is not readable."
  token="$(tr -d '\r\n' <"${token_file}")"
  rm -f -- "${token_file}" || workcell_die "Copilot auth token handoff file could not be removed."
  [[ -n "${token}" ]] || workcell_die "Copilot auth token is empty. Stage a non-empty copilot_github_token."
  workcell_mark_copilot_token_consumed
  printf '%s\n' "${token}"
}

workcell_discard_copilot_github_token() {
  local token_file=""

  token_file="$(workcell_read_copilot_handoff_file)"
  if [[ -n "${token_file}" ]]; then
    rm -f -- "${token_file}" || workcell_die "Copilot auth token handoff file could not be removed."
  fi
  workcell_mark_copilot_token_consumed
}

copilot_no_auth_invocation() {
  local first="${1:-}"
  local second="${2:-}"

  if [[ "${first}" == "copilot" ]]; then
    first="${second}"
  fi

  case "${first}" in
    -h | --help | -v | --version | help | version | completion)
      return 0
      ;;
  esac
  return 1
}

copilot_invocation_requires_auth() {
  case "${WORKCELL_COPILOT_AUTH_REQUIRED}" in
    0)
      return 1
      ;;
    1)
      return 0
      ;;
  esac
  if copilot_no_auth_invocation "$@"; then
    return 1
  fi
  return 0
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
  copilot:yolo)
    MANAGED_AUTONOMY_ARGS=(
      "--available-tools=view,create,edit,apply_patch,grep,glob"
      "--allow-tool=read"
      "--allow-tool=write"
      --no-ask-user
    )
    ;;
  copilot:prompt)
    MANAGED_AUTONOMY_ARGS=(
      "--available-tools=view,create,edit,apply_patch,grep,glob"
    )
    ;;
esac

declare -a MANAGED_COPILOT_SAFETY_ARGS=(
  "--no-auto-update"
  "--no-remote"
  "--no-remote-export"
  "--disable-builtin-mcps"
  "--no-custom-instructions"
  "--disallow-temp-dir"
  "--log-dir"
  "${COPILOT_HOME}/logs"
  "--secret-env-vars=GH_TOKEN,GITHUB_TOKEN,COPILOT_GITHUB_TOKEN"
)

case "${AGENT_NAME}" in
  codex)
    codex_first_subcommand "$@"
    declare -a MANAGED_CODEX_PROFILE_ARGS=()
    if ! codex_args_include_profile "$@" && codex_managed_profile_applies; then
      MANAGED_CODEX_PROFILE_ARGS=(--profile "${CODEX_PROFILE}")
    fi
    declare -a MANAGED_CODEX_APP_SERVER_ARGS=()
    codex_app_server_sandbox_args
    reject_unsafe_codex_args "$@"
    exec /usr/local/libexec/workcell/real/codex "${MANAGED_CODEX_PROFILE_ARGS[@]}" "${MANAGED_CODEX_APP_SERVER_ARGS[@]}" "${MANAGED_AUTONOMY_ARGS[@]}" "$@"
    ;;
  claude)
    reject_unsafe_claude_args "$@"
    DISABLE_AUTOUPDATER=1 CLAUDE_CONFIG_DIR="${HOME}/.claude" exec /usr/local/libexec/workcell/real/claude \
      "${MANAGED_AUTONOMY_ARGS[@]}" \
      "$@"
    ;;
  copilot)
    declare copilot_github_token=""
    reject_unsafe_copilot_args "$@"
    if copilot_invocation_requires_auth "$@"; then
      copilot_github_token="$(workcell_load_copilot_github_token)"
      unset WORKCELL_COPILOT_GITHUB_TOKEN
      unset WORKCELL_COPILOT_TOKEN_FILE
      COPILOT_GITHUB_TOKEN="${copilot_github_token}" \
        COPILOT_AUTO_UPDATE=false \
        COPILOT_ENABLE_HTTP2=false \
        exec /usr/local/libexec/workcell/real/copilot \
        "${MANAGED_COPILOT_SAFETY_ARGS[@]}" \
        "${MANAGED_AUTONOMY_ARGS[@]}" \
        "$@"
    fi
    workcell_discard_copilot_github_token
    unset WORKCELL_COPILOT_GITHUB_TOKEN
    unset WORKCELL_COPILOT_TOKEN_FILE
    COPILOT_AUTO_UPDATE=false COPILOT_ENABLE_HTTP2=false exec /usr/local/libexec/workcell/real/copilot \
      "${MANAGED_COPILOT_SAFETY_ARGS[@]}" \
      "$@"
    ;;
  gemini)
    sanitize_gemini_sandbox_env
    reject_unsafe_gemini_args "$@"
    # Gemini CLI self-relaunch conflicts with Workcell's protected exec boundary.
    GEMINI_CLI_NO_RELAUNCH=1 GEMINI_SANDBOX=false exec /usr/local/libexec/workcell/real/node \
      "$(gemini_provider_script_path)" \
      "${MANAGED_AUTONOMY_ARGS[@]}" \
      "$@"
    ;;
  *)
    workcell_die "Unsupported provider wrapper target: ${AGENT_NAME}"
    ;;
esac
