#!/usr/bin/env -S BASH_ENV= ENV= bash

# shellcheck disable=SC1091
source /usr/local/libexec/workcell/assurance.sh

workcell_managed_session_root_for_path() {
  local path="$1"

  case "${path}" in
    /state/agent-home | /state/agent-home/*)
      printf '/state/agent-home\n'
      ;;
    /state/injected | /state/injected/*)
      printf '/state/injected\n'
      ;;
    *)
      workcell_die "Workcell session target is outside the managed session roots: ${path}"
      ;;
  esac
}

workcell_assert_no_symlink_path_components() {
  local path="$1"
  local label="$2"
  local include_target="${3:-1}"
  local root=""
  local current=""

  root="$(workcell_managed_session_root_for_path "${path}")"
  if [[ "${include_target}" == "1" ]]; then
    current="${path}"
  else
    current="$(dirname "${path}")"
  fi
  while :; do
    if [[ -L "${current}" ]]; then
      workcell_die "Workcell refused ${label}: symlinked session path component ${current}"
    fi
    if [[ "${current}" == "${root}" ]]; then
      return 0
    fi
    current="$(dirname "${current}")"
  done
}

workcell_prepare_session_parent() {
  local target_path="$1"
  local label="$2"
  local parent_path=""

  workcell_assert_no_symlink_path_components "${target_path}" "${label}" 0
  parent_path="$(dirname "${target_path}")"
  mkdir -p "${parent_path}"
  workcell_assert_no_symlink_path_components "${target_path}" "${label}" 0
}

workcell_prepare_session_directory() {
  local target_path="$1"
  local label="$2"

  workcell_prepare_session_parent "${target_path}" "${label}"
  if [[ -e "${target_path}" ]] && [[ ! -d "${target_path}" ]]; then
    rm -rf "${target_path}"
  fi
  mkdir -p "${target_path}"
  workcell_assert_no_symlink_path_components "${target_path}" "${label}"
}

workcell_reset_session_target() {
  local target_path="$1"
  local label="$2"

  workcell_prepare_session_parent "${target_path}" "${label}"
  if [[ -L "${target_path}" ]]; then
    rm -f "${target_path}"
  elif [[ -e "${target_path}" ]]; then
    rm -rf "${target_path}"
  fi
  workcell_assert_no_symlink_path_components "${target_path}" "${label}"
}

workcell_link_control_plane_path() {
  local source_path="$1"
  local target_path="$2"

  workcell_reset_session_target "${target_path}" "control-plane link"
  ln -s "${source_path}" "${target_path}"
}

workcell_copy_control_plane_tree() {
  local source_path="$1"
  local target_path="$2"
  local file_mode="$3"
  local dir_mode="$4"

  workcell_reset_session_target "${target_path}" "control-plane tree"
  mkdir -p "${target_path}"
  cp -R "${source_path}/." "${target_path}"
  find "${target_path}" -type d -exec chmod "${dir_mode}" {} +
  find "${target_path}" -type f -exec chmod "${file_mode}" {} +
  chmod "${dir_mode}" "${target_path}"
}

WORKCELL_WORKSPACE_IMPORT_ROOT="${WORKCELL_WORKSPACE_IMPORT_ROOT:-/opt/workcell/workspace-control-plane}"
WORKCELL_CODEX_RULES_MUTABILITY="${WORKCELL_CODEX_RULES_MUTABILITY:-readonly}"

workcell_session_assurance() {
  workcell_runtime_state_value WORKCELL_SESSION_ASSURANCE || true
}

workcell_current_agent_autonomy() {
  workcell_runtime_state_value WORKCELL_AGENT_AUTONOMY || printf '%s\n' "${WORKCELL_AGENT_AUTONOMY:-yolo}"
}

workcell_assert_session_regular_writable_file() {
  local target_path="$1"
  local label="$2"

  if [[ ! -f "${target_path}" ]]; then
    workcell_die "Workcell failed to seed ${label}: missing file ${target_path}"
  fi
  if [[ -L "${target_path}" ]]; then
    workcell_die "Workcell failed to seed ${label}: expected a session-local copy, not a symlink: ${target_path}"
  fi
  if [[ ! -w "${target_path}" ]]; then
    workcell_die "Workcell failed to seed ${label}: file is not writable: ${target_path}"
  fi
}

workcell_codex_rules_mutability() {
  case "${WORKCELL_CODEX_RULES_MUTABILITY:-readonly}" in
    readonly | session)
      printf '%s\n' "${WORKCELL_CODEX_RULES_MUTABILITY:-readonly}"
      ;;
    *)
      workcell_die "Unsupported Workcell Codex rules mutability: ${WORKCELL_CODEX_RULES_MUTABILITY}"
      ;;
  esac
}

workcell_codex_rules_promoted_for_session_assurance() {
  local configured_mutability=""
  local assurance=""

  configured_mutability="$(workcell_codex_rules_mutability)"
  assurance="$(workcell_session_assurance)"
  [[ "${configured_mutability}" == "readonly" ]] &&
    [[ "${assurance}" == "lower-assurance-package-mutation" ]]
}

workcell_codex_rules_promoted_for_prompt_autonomy() {
  local configured_mutability=""
  local autonomy=""

  configured_mutability="$(workcell_codex_rules_mutability)"
  autonomy="$(workcell_current_agent_autonomy)"
  [[ "${configured_mutability}" == "readonly" ]] &&
    [[ "${autonomy}" == "prompt" ]]
}

workcell_codex_rules_effective_reason() {
  local configured_mutability=""
  local autonomy=""
  local assurance=""

  configured_mutability="$(workcell_codex_rules_mutability)"
  autonomy="$(workcell_current_agent_autonomy)"
  assurance="$(workcell_session_assurance)"

  if [[ "${configured_mutability}" == "session" ]]; then
    printf 'operator-opt-in\n'
    return 0
  fi
  if [[ "${autonomy}" == "prompt" ]]; then
    printf 'prompt-autonomy\n'
    return 0
  fi
  if [[ "${assurance}" == "lower-assurance-package-mutation" ]]; then
    printf 'package-mutation\n'
    return 0
  fi

  printf 'managed-default\n'
}

workcell_current_effective_codex_rules_mutability() {
  workcell_effective_codex_rules_mutability \
    "$(workcell_codex_rules_mutability)" \
    "$(workcell_current_agent_autonomy)" \
    "$(workcell_session_assurance)"
}

workcell_manifest_active() {
  [[ -n "${WORKCELL_INJECTION_MANIFEST:-}" ]]
}

workcell_manifest_path() {
  printf '%s\n' "${WORKCELL_INJECTION_MANIFEST:-}"
}

workcell_manifest_root() {
  dirname "$(workcell_manifest_path)"
}

workcell_ensure_manifest() {
  if ! workcell_manifest_active; then
    return 1
  fi

  if [[ ! -f "$(workcell_manifest_path)" ]]; then
    workcell_die "Workcell injection manifest is missing: $(workcell_manifest_path)"
  fi
}

workcell_manifest_string() {
  local filter="$1"

  workcell_ensure_manifest || return 1
  jq -r "${filter}" "$(workcell_manifest_path)"
}

workcell_manifest_source_path() {
  local relative_path="$1"

  case "${relative_path}" in
    "" | /* | *".."*)
      workcell_die "Invalid Workcell injection source path: ${relative_path}"
      ;;
  esac

  printf '%s/%s\n' "$(workcell_manifest_root)" "${relative_path}"
}

workcell_validate_direct_mount_path() {
  local mount_path="$1"
  case "${mount_path}" in
    /opt/workcell/host-inputs/*) ;;
    *)
      workcell_die "Workcell direct input mount path is outside the managed host-input root: ${mount_path}"
      ;;
  esac
}

workcell_manifest_direct_mount_path() {
  local filter="$1"
  local mount_path=""

  workcell_ensure_manifest || return 1
  mount_path="$(jq -r "${filter}" "$(workcell_manifest_path)")"
  [[ -n "${mount_path}" ]] || return 0
  workcell_validate_direct_mount_path "${mount_path}"
  printf '%s\n' "${mount_path}"
}

workcell_resolve_manifest_input_path() {
  local source_ref="$1"
  local mount_path="$2"

  if [[ -n "${mount_path}" ]]; then
    workcell_validate_direct_mount_path "${mount_path}"
    printf '%s\n' "${mount_path}"
    return 0
  fi

  workcell_manifest_source_path "${source_ref}"
}

workcell_copy_manifest_credential_file() {
  local key="$1"
  local target_path="$2"
  local source_path=""

  source_path="$(workcell_manifest_direct_mount_path ".credentials[\"${key}\"].mount_path // empty" || true)"
  [[ -n "${source_path}" ]] || return 1

  workcell_reset_session_target "${target_path}" "credential copy"
  if [[ ! -f "${source_path}" ]]; then
    workcell_die "Workcell expected mounted credential file for ${key}: ${source_path}"
  fi
  cp "${source_path}" "${target_path}"
  chmod 0600 "${target_path}"
  return 0
}

workcell_workspace_import_path() {
  local relative_path="$1"
  local import_path="${WORKCELL_WORKSPACE_IMPORT_ROOT}/${relative_path}"

  [[ -f "${import_path}" ]] || return 1
  printf '%s\n' "${import_path}"
}

workcell_render_claude_settings() {
  local baseline_path="${ADAPTER_ROOT}/claude/.claude/settings.json"
  local target_path="${HOME}/.claude/settings.json"
  local api_key_source=""
  local helper_dir=""
  local helper_secret=""
  local helper_script=""

  api_key_source="$(workcell_manifest_direct_mount_path '.credentials["claude_api_key"].mount_path // empty' || true)"
  if [[ -z "${api_key_source}" ]]; then
    workcell_link_control_plane_path "${baseline_path}" "${target_path}"
    return 0
  fi

  helper_dir="${HOME}/.claude/workcell"
  helper_secret="${helper_dir}/claude-api-key"
  helper_script="${helper_dir}/api-key-helper.sh"

  workcell_prepare_session_directory "${helper_dir}" "Claude helper directory"
  if [[ ! -f "${api_key_source}" ]]; then
    workcell_die "Workcell expected mounted credential file for claude_api_key: ${api_key_source}"
  fi
  workcell_reset_session_target "${helper_secret}" "Claude helper secret"
  cp "${api_key_source}" "${helper_secret}"
  chmod 0600 "${helper_secret}"
  workcell_reset_session_target "${helper_script}" "Claude helper script"
  printf '#!/bin/sh\nset -eu\ncat %s\n' "${helper_secret@Q}" >"${helper_script}"
  chmod 0700 "${helper_script}"
  workcell_reset_session_target "${target_path}" "Claude settings"
  jq --arg helper "${helper_script}" '.apiKeyHelper = $helper' "${baseline_path}" >"${target_path}"
  chmod 0600 "${target_path}"
}

workcell_target_is_allowed() {
  local target_path="$1"

  case "${target_path}" in
    /state/agent-home | /state/agent-home/* | /state/injected | /state/injected/*) ;;
    *)
      return 1
      ;;
  esac

  case "${target_path}" in
    /state/agent-home/.codex/AGENTS.md | \
      /state/agent-home/.codex/auth.json | \
      /state/agent-home/.codex/config.toml | \
      /state/agent-home/.codex/managed_config.toml | \
      /state/agent-home/.codex/requirements.toml | \
      /state/agent-home/.claude/settings.json | \
      /state/agent-home/.claude/CLAUDE.md | \
      /state/agent-home/.claude/workcell | \
      /state/agent-home/.claude/workcell/* | \
      /state/agent-home/.config/claude-code/auth.json | \
      /state/agent-home/.mcp.json | \
      /state/agent-home/.gemini/settings.json | \
      /state/agent-home/.gemini/GEMINI.md | \
      /state/agent-home/.gemini/.env | \
      /state/agent-home/.gemini/oauth_creds.json | \
      /state/agent-home/.gemini/projects.json | \
      /state/agent-home/.config/gcloud/application_default_credentials.json | \
      /state/agent-home/.config/gh/config.yml | \
      /state/agent-home/.config/gh/hosts.yml | \
      /state/agent-home/.ssh | \
      /state/agent-home/.ssh/* | \
      /state/agent-home/.codex/agents | \
      /state/agent-home/.codex/agents/* | \
      /state/agent-home/.codex/rules | \
      /state/agent-home/.codex/rules/* | \
      /state/agent-home/.codex/mcp | \
      /state/agent-home/.codex/mcp/*)
      return 1
      ;;
  esac

  return 0
}

workcell_copy_manifest_entry() {
  local source_path="$1"
  local target_path="$2"
  local kind="$3"
  local file_mode="$4"
  local dir_mode="$5"

  if ! workcell_target_is_allowed "${target_path}"; then
    workcell_die "Workcell injection target is not allowed: ${target_path}"
  fi

  case "${kind}" in
    file)
      workcell_reset_session_target "${target_path}" "injected copy"
      workcell_assert_no_symlink_path_components "${target_path}" "injected copy" 0
      cp "${source_path}" "${target_path}"
      chmod "${file_mode}" "${target_path}"
      ;;
    dir)
      workcell_reset_session_target "${target_path}" "injected copy"
      workcell_assert_no_symlink_path_components "${target_path}" "injected copy" 0
      mkdir -p "${target_path}"
      cp -R "${source_path}/." "${target_path}"
      find "${target_path}" -type d -exec chmod "${dir_mode}" {} +
      find "${target_path}" -type f -exec chmod "${file_mode}" {} +
      chmod "${dir_mode}" "${target_path}"
      ;;
    *)
      workcell_die "Unsupported Workcell injection kind: ${kind}"
      ;;
  esac
}

workcell_render_provider_doc() {
  local baseline_path="$1"
  local target_path="$2"
  local provider_key="$3"
  local common_rel=""
  local provider_rel=""
  local workspace_common_doc=""
  local workspace_provider_doc=""

  if workcell_manifest_active; then
    common_rel="$(workcell_manifest_string '.documents.common // empty')"
    provider_rel="$(workcell_manifest_string ".documents.${provider_key} // empty")"
  fi

  case "${provider_key}" in
    codex)
      workspace_common_doc="$(workcell_workspace_import_path 'AGENTS.md' || true)"
      ;;
    claude)
      workspace_common_doc="$(workcell_workspace_import_path 'AGENTS.md' || true)"
      workspace_provider_doc="$(workcell_workspace_import_path 'CLAUDE.md' || true)"
      ;;
    gemini)
      workspace_common_doc="$(workcell_workspace_import_path 'AGENTS.md' || true)"
      workspace_provider_doc="$(workcell_workspace_import_path 'GEMINI.md' || true)"
      ;;
  esac

  if [[ -z "${workspace_common_doc}" ]] && [[ -z "${workspace_provider_doc}" ]] &&
    [[ -z "${common_rel}" ]] && [[ -z "${provider_rel}" ]]; then
    workcell_link_control_plane_path "${baseline_path}" "${target_path}"
    return 0
  fi

  workcell_reset_session_target "${target_path}" "provider document"
  {
    cat "${baseline_path}"
    if [[ -n "${workspace_common_doc}" ]]; then
      printf '\n\n<!-- Workcell imported workspace %s -->\n\n' "$(basename "${workspace_common_doc}")"
      cat "${workspace_common_doc}"
    fi
    if [[ -n "${workspace_provider_doc}" ]]; then
      printf '\n\n<!-- Workcell imported workspace %s -->\n\n' "$(basename "${workspace_provider_doc}")"
      cat "${workspace_provider_doc}"
    fi
    if [[ -n "${common_rel}" ]]; then
      printf '\n\n<!-- Workcell injected common instructions -->\n\n'
      cat "$(workcell_manifest_source_path "${common_rel}")"
    fi
    if [[ -n "${provider_rel}" ]]; then
      printf '\n\n<!-- Workcell injected %s instructions -->\n\n' "${provider_key}"
      cat "$(workcell_manifest_source_path "${provider_rel}")"
    fi
  } >"${target_path}"
  chmod 0444 "${target_path}"
}

workcell_seed_codex_rules() {
  local baseline_rules="${ADAPTER_ROOT}/codex/.codex/rules"
  local rules_target="${CODEX_HOME}/rules"
  local default_rules_target="${rules_target}/default.rules"
  local rules_mutability=""

  rules_mutability="$(workcell_current_effective_codex_rules_mutability)"
  case "${rules_mutability}" in
    readonly)
      workcell_link_control_plane_path "${baseline_rules}" "${rules_target}"
      ;;
    session)
      if [[ ! -d "${rules_target}" ]] || [[ -L "${rules_target}" ]] || [[ ! -f "${default_rules_target}" ]]; then
        workcell_copy_control_plane_tree "${baseline_rules}" "${rules_target}" 0600 0700
      fi
      workcell_assert_session_regular_writable_file "${default_rules_target}" "Codex execpolicy session rules"
      ;;
  esac
}

workcell_apply_manifest_copies() {
  local entry_json=""
  local source_rel=""
  local mount_path=""
  local target_path=""
  local kind=""
  local file_mode=""
  local dir_mode=""

  workcell_ensure_manifest || return 0
  mkdir -p /state/injected
  chmod 0755 /state/injected 2>/dev/null || true

  while IFS= read -r entry_json; do
    source_rel="$(jq -r 'if (.source | type) == "object" then (.source.source // "") else .source end' <<<"${entry_json}")"
    mount_path="$(jq -r 'if (.source | type) == "object" then (.source.mount_path // "") else "" end' <<<"${entry_json}")"
    target_path="$(jq -r '.target' <<<"${entry_json}")"
    kind="$(jq -r '.kind' <<<"${entry_json}")"
    file_mode="$(jq -r '.file_mode' <<<"${entry_json}")"
    dir_mode="$(jq -r '.dir_mode' <<<"${entry_json}")"
    [[ -n "${source_rel}${mount_path}" ]] || continue
    workcell_copy_manifest_entry \
      "$(workcell_resolve_manifest_input_path "${source_rel}" "${mount_path}")" \
      "${target_path}" \
      "${kind}" \
      "${file_mode}" \
      "${dir_mode}"
  done < <(jq -c '.copies[]?' "$(workcell_manifest_path)")
}

workcell_apply_manifest_ssh() {
  local config_source=""
  local config_mount_path=""
  local known_hosts_source=""
  local known_hosts_mount_path=""
  local identity_source=""
  local identity_mount_path=""
  local identity_name=""

  workcell_ensure_manifest || return 0
  config_source="$(workcell_manifest_string 'if (.ssh.config | type) == "object" then (.ssh.config.source // empty) else (.ssh.config // empty) end')"
  config_mount_path="$(workcell_manifest_string 'if (.ssh.config | type) == "object" then (.ssh.config.mount_path // empty) else empty end')"
  known_hosts_source="$(workcell_manifest_string 'if (.ssh.known_hosts | type) == "object" then (.ssh.known_hosts.source // empty) else (.ssh.known_hosts // empty) end')"
  known_hosts_mount_path="$(workcell_manifest_string 'if (.ssh.known_hosts | type) == "object" then (.ssh.known_hosts.mount_path // empty) else empty end')"
  if [[ -z "${config_source}" ]] && [[ -z "${known_hosts_source}" ]] &&
    [[ "$(workcell_manifest_string '(.ssh.identities // []) | length')" == "0" ]]; then
    return 0
  fi

  workcell_prepare_session_directory "${HOME}/.ssh" "SSH home"
  chmod 0700 "${HOME}/.ssh"

  if [[ -n "${config_source}${config_mount_path}" ]]; then
    workcell_reset_session_target "${HOME}/.ssh/config" "SSH config"
    cp "$(workcell_resolve_manifest_input_path "${config_source}" "${config_mount_path}")" "${HOME}/.ssh/config"
    chmod 0600 "${HOME}/.ssh/config"
  fi

  if [[ -n "${known_hosts_source}${known_hosts_mount_path}" ]]; then
    workcell_reset_session_target "${HOME}/.ssh/known_hosts" "known_hosts"
    cp "$(workcell_resolve_manifest_input_path "${known_hosts_source}" "${known_hosts_mount_path}")" "${HOME}/.ssh/known_hosts"
    chmod 0600 "${HOME}/.ssh/known_hosts"
  fi

  while IFS= read -r entry_json; do
    identity_source="$(jq -r '.source // ""' <<<"${entry_json}")"
    identity_mount_path="$(jq -r '.mount_path // ""' <<<"${entry_json}")"
    identity_name="$(jq -r '.target_name' <<<"${entry_json}")"
    [[ -n "${identity_source}${identity_mount_path}" ]] || continue
    workcell_reset_session_target "${HOME}/.ssh/${identity_name}" "SSH identity"
    cp "$(workcell_resolve_manifest_input_path "${identity_source}" "${identity_mount_path}")" "${HOME}/.ssh/${identity_name}"
    chmod 0600 "${HOME}/.ssh/${identity_name}"
  done < <(jq -c '.ssh.identities[]?' "$(workcell_manifest_path)")
}

seed_codex_home() {
  workcell_prepare_session_directory "${CODEX_HOME}" "Codex home"
  workcell_prepare_session_directory "${CODEX_HOME}/mcp" "Codex MCP directory"
  workcell_render_provider_doc "${ADAPTER_ROOT}/codex/.codex/AGENTS.md" "${CODEX_HOME}/AGENTS.md" codex
  workcell_link_control_plane_path "${ADAPTER_ROOT}/codex/.codex/config.toml" "${CODEX_HOME}/config.toml"
  workcell_link_control_plane_path "${ADAPTER_ROOT}/codex/managed_config.toml" "${CODEX_HOME}/managed_config.toml"
  workcell_link_control_plane_path "${ADAPTER_ROOT}/codex/requirements.toml" "${CODEX_HOME}/requirements.toml"
  workcell_link_control_plane_path "${ADAPTER_ROOT}/codex/.codex/agents" "${CODEX_HOME}/agents"
  workcell_seed_codex_rules
  workcell_link_control_plane_path "${ADAPTER_ROOT}/codex/mcp/config.toml" "${CODEX_HOME}/mcp/config.toml"
  workcell_copy_manifest_credential_file codex_auth "${CODEX_HOME}/auth.json" || true
}

seed_claude_home() {
  workcell_prepare_session_directory "${HOME}/.claude" "Claude home"
  workcell_render_claude_settings
  workcell_render_provider_doc "${ADAPTER_ROOT}/claude/CLAUDE.md" "${HOME}/.claude/CLAUDE.md" claude
  workcell_prepare_session_directory "${HOME}/.config/claude-code" "Claude auth directory"
  workcell_copy_manifest_credential_file claude_auth "${HOME}/.config/claude-code/auth.json" || true
  if ! workcell_copy_manifest_credential_file claude_mcp "${HOME}/.mcp.json"; then
    workcell_link_control_plane_path "${ADAPTER_ROOT}/claude/mcp-template.json" "${HOME}/.mcp.json"
  fi
}

seed_gemini_home() {
  workcell_prepare_session_directory "${HOME}/.gemini" "Gemini home"
  rm -f "${HOME}/.gemini/settings.json"
  cp "${ADAPTER_ROOT}/gemini/.gemini/settings.json" "${HOME}/.gemini/settings.json"
  chmod 0600 "${HOME}/.gemini/settings.json"
  workcell_render_provider_doc "${ADAPTER_ROOT}/gemini/GEMINI.md" "${HOME}/.gemini/GEMINI.md" gemini
  workcell_copy_manifest_credential_file gemini_env "${HOME}/.gemini/.env" || true
  workcell_copy_manifest_credential_file gemini_oauth "${HOME}/.gemini/oauth_creds.json" || true
  workcell_prepare_session_directory "${HOME}/.config/gcloud" "Google ADC directory"
  workcell_copy_manifest_credential_file gcloud_adc "${HOME}/.config/gcloud/application_default_credentials.json" || true
  if ! workcell_copy_manifest_credential_file gemini_projects "${HOME}/.gemini/projects.json"; then
    workcell_reset_session_target "${HOME}/.gemini/projects.json" "Gemini projects"
    printf '{\n  "projects": {}\n}\n' >"${HOME}/.gemini/projects.json"
    chmod 0600 "${HOME}/.gemini/projects.json"
  fi
}

workcell_seed_shared_credentials() {
  workcell_prepare_session_directory "${HOME}/.config/gh" "GitHub CLI config directory"
  workcell_copy_manifest_credential_file github_config "${HOME}/.config/gh/config.yml" || true
  workcell_copy_manifest_credential_file github_hosts "${HOME}/.config/gh/hosts.yml" || true
}

seed_agent_home() {
  case "$1" in
    codex)
      seed_codex_home
      ;;
    claude)
      seed_claude_home
      ;;
    gemini)
      seed_gemini_home
      ;;
    *)
      workcell_die "Unsupported agent: $1"
      ;;
  esac

  workcell_seed_shared_credentials
  workcell_apply_manifest_copies
  workcell_apply_manifest_ssh
}
