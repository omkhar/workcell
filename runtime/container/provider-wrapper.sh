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
  export NODE_NO_WARNINGS=1
  export LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so
  export PATH="${TRUSTED_PATH}"
}

require_managed_provider_launch() {
  case "${AGENT_NAME}" in
    codex | claude | gemini) ;;
    *)
      workcell_die "Unsupported provider wrapper target: ${AGENT_NAME}"
      ;;
  esac

  if [[ "${AGENT_NAME}" == "codex" && "${1:-}" == "execpolicy" ]]; then
    return 0
  fi

  if [[ "${WORKCELL_PROVIDER_LAUNCHER_AUTHORITY:-0}" != "1" ]]; then
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

# Codex 0.134+ rejects the global --profile flag on non-runtime subcommands
# (e.g. `codex features`, `codex login`): it only applies to the runtime
# commands and `codex mcp`. Return success only when the managed --profile
# injection is valid, i.e. the invocation resolves to a runtime subcommand,
# the default TUI (a bare prompt with no subcommand), or a short-circuit
# global flag like --version/--help that Codex accepts alongside --profile.
# Global value-taking flags are consumed so the real subcommand token is found.
codex_managed_profile_applies() {
  local arg="" skip_value=0
  for arg in "$@"; do
    if [[ "${skip_value}" -eq 1 ]]; then
      skip_value=0
      continue
    fi
    case "${arg}" in
      --)
        # Everything after -- is the prompt; default TUI runtime.
        return 0
        ;;
      -c | --config | -m | --model | -i | --image | -C | --cd | \
        -a | --ask-for-approval | -s | --sandbox | -p | --profile)
        skip_value=1
        ;;
      --*=* | -*)
        # Boolean global flag or =-form value flag; keep scanning.
        ;;
      e | exec | review | resume | archive | unarchive | fork | mcp | sandbox)
        # Runtime subcommands (and the `e` alias for exec) that accept --profile.
        return 0
        ;;
      *)
        # First positional that is not a runtime subcommand: either a
        # non-runtime subcommand (login/features/...) that rejects --profile,
        # or a bare prompt for the default TUI runtime. Treat a recognized
        # non-runtime subcommand (including its aliases, e.g. `a` for apply and
        # `cloud-tasks` for cloud) as rejecting; anything else is the default
        # TUI prompt and accepts the profile.
        case "${arg}" in
          login | logout | plugin | mcp-server | app-server | remote-control | \
            completion | update | doctor | apply | a | cloud | cloud-tasks | \
            exec-server | execpolicy | features | help | debug)
            return 1
            ;;
          *)
            return 0
            ;;
        esac
        ;;
    esac
  done
  # No subcommand token: default TUI runtime, profile applies.
  return 0
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
# honor `-c` root config overrides, so emit the per-mode sandbox_mode override
# on stdout (one arg per line) to give GUI sessions the same sandbox layer the
# CLI gets from its profile. The approval policy still comes from the injected
# --ask-for-approval autonomy flag, which app-server accepts. Emits nothing when
# the invocation is not app-server.
codex_app_server_sandbox_args() {
  local arg="" skip_value=0 subcommand=""
  for arg in "$@"; do
    if [[ "${skip_value}" -eq 1 ]]; then
      skip_value=0
      continue
    fi
    case "${arg}" in
      --)
        break
        ;;
      -c | --config | -m | --model | -i | --image | -C | --cd | \
        -a | --ask-for-approval | -s | --sandbox | -p | --profile)
        skip_value=1
        ;;
      --*=* | -*) ;;
      *)
        subcommand="${arg}"
        break
        ;;
    esac
  done
  case "${subcommand}" in
    app | app-server)
      printf '%s\n' "-c" "sandbox_mode=\"$(codex_profile_sandbox_mode)\""
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
    if ! codex_args_include_profile "$@" && codex_managed_profile_applies "$@"; then
      MANAGED_CODEX_PROFILE_ARGS=(--profile "${CODEX_PROFILE}")
    fi
    declare -a MANAGED_CODEX_APP_SERVER_ARGS=()
    mapfile -t MANAGED_CODEX_APP_SERVER_ARGS < <(codex_app_server_sandbox_args "$@")
    reject_unsafe_codex_args "$@"
    exec /usr/local/libexec/workcell/real/codex "${MANAGED_CODEX_PROFILE_ARGS[@]}" "${MANAGED_CODEX_APP_SERVER_ARGS[@]}" "${MANAGED_AUTONOMY_ARGS[@]}" "$@"
    ;;
  claude)
    reject_unsafe_claude_args "$@"
    DISABLE_AUTOUPDATER=1 CLAUDE_CONFIG_DIR="${HOME}/.claude" exec /usr/local/libexec/workcell/real/claude \
      "${MANAGED_AUTONOMY_ARGS[@]}" \
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
