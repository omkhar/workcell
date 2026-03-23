#!/usr/bin/env -S BASH_ENV= ENV= bash

workcell_link_control_plane_path() {
  local source_path="$1"
  local target_path="$2"

  mkdir -p "$(dirname "${target_path}")"
  rm -rf "${target_path}"
  ln -s "${source_path}" "${target_path}"
}

seed_codex_home() {
  mkdir -p "${CODEX_HOME}" "${CODEX_HOME}/mcp"
  workcell_link_control_plane_path "${ADAPTER_ROOT}/codex/.codex/AGENTS.md" "${CODEX_HOME}/AGENTS.md"
  workcell_link_control_plane_path "${ADAPTER_ROOT}/codex/.codex/config.toml" "${CODEX_HOME}/config.toml"
  workcell_link_control_plane_path "${ADAPTER_ROOT}/codex/managed_config.toml" "${CODEX_HOME}/managed_config.toml"
  workcell_link_control_plane_path "${ADAPTER_ROOT}/codex/requirements.toml" "${CODEX_HOME}/requirements.toml"
  workcell_link_control_plane_path "${ADAPTER_ROOT}/codex/.codex/agents" "${CODEX_HOME}/agents"
  workcell_link_control_plane_path "${ADAPTER_ROOT}/codex/.codex/rules" "${CODEX_HOME}/rules"
  workcell_link_control_plane_path "${ADAPTER_ROOT}/codex/mcp/config.toml" "${CODEX_HOME}/mcp/config.toml"
}

seed_claude_home() {
  mkdir -p "${HOME}/.claude"
  workcell_link_control_plane_path "${ADAPTER_ROOT}/claude/.claude/settings.json" "${HOME}/.claude/settings.json"
  workcell_link_control_plane_path "${ADAPTER_ROOT}/claude/CLAUDE.md" "${HOME}/.claude/CLAUDE.md"
  workcell_link_control_plane_path "${ADAPTER_ROOT}/claude/mcp-template.json" "${HOME}/.mcp.json"
}

seed_gemini_home() {
  mkdir -p "${HOME}/.gemini"
  rm -f "${HOME}/.gemini/settings.json"
  cp "${ADAPTER_ROOT}/gemini/.gemini/settings.json" "${HOME}/.gemini/settings.json"
  chmod 0600 "${HOME}/.gemini/settings.json"
  workcell_link_control_plane_path "${ADAPTER_ROOT}/gemini/GEMINI.md" "${HOME}/.gemini/GEMINI.md"
  if [[ ! -f "${HOME}/.gemini/projects.json" ]]; then
    printf '{\n  "projects": {}\n}\n' >"${HOME}/.gemini/projects.json"
    chmod 0600 "${HOME}/.gemini/projects.json"
  fi
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
}
