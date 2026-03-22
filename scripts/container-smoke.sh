#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE_TAG="${WORKCELL_IMAGE_TAG:-workcell:smoke}"
DOCKER_CONTEXT_NAME="${WORKCELL_CONTAINER_SMOKE_DOCKER_CONTEXT:-}"

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

select_docker_context() {
  if [[ -n "${DOCKER_CONTEXT_NAME}" ]]; then
    return
  fi

  if docker context inspect colima >/dev/null 2>&1; then
    DOCKER_CONTEXT_NAME="colima"
    return
  fi

  if docker context inspect default >/dev/null 2>&1; then
    DOCKER_CONTEXT_NAME="default"
  fi
}

docker_cmd() {
  if [[ -n "${DOCKER_CONTEXT_NAME}" ]]; then
    docker --context "${DOCKER_CONTEXT_NAME}" "$@"
  else
    docker "$@"
  fi
}

run_container() {
  local agent="$1"
  shift

  docker_cmd run --rm \
    --read-only \
    --tmpfs "/tmp:exec,nosuid,nodev,size=1g,mode=1777" \
    --tmpfs "/run:nosuid,nodev,size=64m,mode=755" \
    --tmpfs "/state:exec,nosuid,nodev,size=1g,mode=1777" \
    -e AGENT_NAME="${agent}" \
    -e AGENT_UI=cli \
    -e CODEX_PROFILE=strict \
    -e HOME=/state/agent-home \
    -e CODEX_HOME=/state/agent-home/.codex \
    -e WORKCELL_RUNTIME=1 \
    -e WORKSPACE=/workspace \
    -v "${ROOT_DIR}:/workspace" \
    "${IMAGE_TAG}" "$@"
}

require_tool docker
select_docker_context

docker_cmd build \
  -t "${IMAGE_TAG}" \
  -f "${ROOT_DIR}/runtime/container/Dockerfile" \
  "${ROOT_DIR}" >/dev/null

# shellcheck disable=SC2016
run_container codex bash -lc '
  test "$(id -u)" != 0
  test "$WORKCELL_RUNTIME" = "1"
  codex --version | grep -q "codex-cli"
  test -f "$CODEX_HOME/config.toml"
  codex features list >/dev/null
  codex execpolicy check --rules /workspace/adapters/codex/.codex/rules/default.rules rm -rf build \
    | jq -e ".decision == \"forbidden\"" >/dev/null
  codex execpolicy check --rules /workspace/adapters/codex/.codex/rules/default.rules git commit --no-verify \
    | jq -e ".decision == \"forbidden\"" >/dev/null
  mkdir -p /tmp/git-guard && cd /tmp/git-guard
  git init -q
  git config user.name "Workcell Smoke"
  git config user.email "workcell-smoke@example.com"
  touch smoke.txt
  git add smoke.txt
  if git commit --no-verify -m smoke >/tmp/git-guard.out 2>&1; then
    echo "expected Workcell git guard to reject --no-verify" >&2
    exit 1
  fi
  grep -q "Workcell blocked git hook bypass" /tmp/git-guard.out
  if /usr/bin/git commit -n -m smoke >/tmp/git-guard-short.out 2>&1; then
    echo "expected Workcell git guard to reject git commit -n" >&2
    exit 1
  fi
  grep -q "Workcell blocked git hook bypass" /tmp/git-guard-short.out
  if /usr/local/libexec/workcell/git commit -n -m smoke >/tmp/git-guard-real.out 2>&1; then
    echo "expected Workcell git guard to reject direct real git execution" >&2
    exit 1
  fi
  grep -q "Workcell blocked git hook bypass" /tmp/git-guard-real.out
  if git -c core.hooksPath=/dev/null commit -m smoke >/tmp/git-guard-hooks.out 2>&1; then
    echo "expected Workcell git guard to reject inline core.hooksPath override" >&2
    exit 1
  fi
  grep -q "Workcell blocked git hook bypass" /tmp/git-guard-hooks.out
  git config alias.ci "commit -n"
  if git ci -m smoke >/tmp/git-guard-alias.out 2>&1; then
    echo "expected Workcell git guard to reject alias-expanded git commit -n" >&2
    exit 1
  fi
  grep -q "Workcell blocked git hook bypass" /tmp/git-guard-alias.out
'

# shellcheck disable=SC2016
run_container claude bash -lc '
  claude --version 2>&1 | grep -q "Claude Code"
  test -f "$HOME/.claude/settings.json"
  test -f "$HOME/.mcp.json"
  jq -r ".hooks.PreToolUse[0].hooks[].command" "$HOME/.claude/settings.json" | grep -q "no-verify"
'

# shellcheck disable=SC2016
run_container gemini bash -lc '
  out="$(gemini --version 2>&1)"
  echo "$out"
  echo "$out" | grep -Eq "([0-9]+\\.){2}[0-9]+"
  test -f "$HOME/.gemini/settings.json"
  test -f "$HOME/.gemini/GEMINI.md"
'

echo "Workcell container smoke passed."
