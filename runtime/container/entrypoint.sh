#!/usr/bin/env bash
set -euo pipefail

AGENT_NAME="${AGENT_NAME:-codex}"
AGENT_UI="${AGENT_UI:-cli}"
HOME="${HOME:-/state/agent-home}"
CODEX_HOME="${CODEX_HOME:-${HOME}/.codex}"
CODEX_PROFILE="${CODEX_PROFILE:-strict}"
WORKSPACE="${WORKSPACE:-/workspace}"
ADAPTER_ROOT="/opt/workcell/adapters"

seed_codex() {
  mkdir -p "${CODEX_HOME}/agents" "${CODEX_HOME}/mcp" "${CODEX_HOME}/rules"
  cp "${ADAPTER_ROOT}/codex/.codex/AGENTS.md" "${CODEX_HOME}/AGENTS.md"
  cp "${ADAPTER_ROOT}/codex/.codex/config.toml" "${CODEX_HOME}/config.toml"
  cp -R "${ADAPTER_ROOT}/codex/.codex/agents/." "${CODEX_HOME}/agents/"
  cp -R "${ADAPTER_ROOT}/codex/.codex/rules/." "${CODEX_HOME}/rules/"
  cp "${ADAPTER_ROOT}/codex/mcp/config.toml" "${CODEX_HOME}/mcp/config.toml"
}

seed_claude() {
  mkdir -p "${HOME}/.claude"
  cp -R "${ADAPTER_ROOT}/claude/.claude/." "${HOME}/.claude/"
  cp "${ADAPTER_ROOT}/claude/CLAUDE.md" "${HOME}/.claude/CLAUDE.md"
  cp "${ADAPTER_ROOT}/claude/mcp-template.json" "${HOME}/.mcp.json"
}

seed_gemini() {
  mkdir -p "${HOME}/.gemini"
  cp -R "${ADAPTER_ROOT}/gemini/.gemini/." "${HOME}/.gemini/"
  cp "${ADAPTER_ROOT}/gemini/GEMINI.md" "${HOME}/.gemini/GEMINI.md"
}

mkdir -p "${HOME}"

case "${AGENT_NAME}" in
  codex)
    seed_codex
    ;;
  claude)
    seed_claude
    ;;
  gemini)
    seed_gemini
    ;;
  *)
    echo "Unsupported agent: ${AGENT_NAME}" >&2
    exit 2
    ;;
esac

if [[ $# -eq 0 ]]; then
  case "${AGENT_NAME}:${AGENT_UI}" in
    codex:cli)
      set -- codex --profile "${CODEX_PROFILE}" --cd "${WORKSPACE}"
      ;;
    codex:gui)
      set -- codex app-server
      ;;
    claude:cli)
      set -- claude
      ;;
    gemini:cli)
      set -- gemini
      ;;
    *)
      echo "Unsupported agent/ui combination: ${AGENT_NAME}:${AGENT_UI}" >&2
      exit 2
      ;;
  esac
fi

if [[ "${AGENT_NAME}" == "codex" ]] && [[ $# -gt 0 ]] && [[ "$1" == "codex" ]]; then
  for arg in "$@"; do
    case "${arg}" in
      -p | --profile)
        printf 'agent=%s ui=%s workspace=%s\n' "${AGENT_NAME}" "${AGENT_UI}" "${WORKSPACE}" >&2
        exec "$@"
        ;;
    esac
  done
  set -- codex --profile "${CODEX_PROFILE}" "${@:2}"
fi

printf 'agent=%s ui=%s workspace=%s\n' "${AGENT_NAME}" "${AGENT_UI}" "${WORKSPACE}" >&2
exec "$@"
