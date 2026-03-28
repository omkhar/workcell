#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

FIXTURE="${ROOT_DIR}/tests/scenarios/claude-swe/blocked-git-commands.txt"
HOOK="${ROOT_DIR}/adapters/claude/hooks/guard-bash.sh"

blocked=0
failures=0

while IFS= read -r cmd; do
  [[ -z "${cmd}" ]] && continue
  payload="$(jq -cn --arg c "${cmd}" '{"tool":"Bash","tool_input":{"command":$c}}')"
  exit_code=0
  printf '%s' "${payload}" | bash "${HOOK}" >/dev/null 2>&1 || exit_code=$?
  if [[ "${exit_code}" -eq 2 ]]; then
    blocked=$((blocked + 1))
  else
    echo "FAIL: command not blocked (exit ${exit_code}): ${cmd}" >&2
    failures=$((failures + 1))
  fi
done <"${FIXTURE}"

if [[ "${failures}" -gt 0 ]]; then
  echo "${failures} command(s) were not blocked as expected." >&2
  exit 1
fi

PERMITTED_FIXTURE="${ROOT_DIR}/tests/scenarios/claude-swe/permitted-git-commands.txt"

not_blocked=0
false_blocks=0

while IFS= read -r cmd; do
  [[ -z "${cmd}" ]] && continue
  payload="$(jq -cn --arg c "${cmd}" '{"tool":"Bash","tool_input":{"command":$c}}')"
  exit_code=0
  printf '%s' "${payload}" | bash "${HOOK}" >/dev/null 2>&1 || exit_code=$?
  if [[ "${exit_code}" -ne 2 ]]; then
    not_blocked=$((not_blocked + 1))
  else
    echo "FAIL: legitimate command was blocked: ${cmd}" >&2
    false_blocks=$((false_blocks + 1))
  fi
done <"${PERMITTED_FIXTURE}"

if [[ "${false_blocks}" -gt 0 ]]; then
  echo "${false_blocks} legitimate command(s) were incorrectly blocked." >&2
  exit 1
fi

echo "Hook negative test passed: ${not_blocked} legitimate commands correctly permitted"

echo "Hook parametric tests passed: ${blocked} commands blocked, ${not_blocked} commands permitted"
