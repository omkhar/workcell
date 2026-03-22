#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CODEX_VERIFY_HOME="$(mktemp -d)"

cleanup() {
  rm -rf "${CODEX_VERIFY_HOME}"
}

trap cleanup EXIT

check_file() {
  [[ -f "$1" ]] || {
    echo "Missing required file: $1" >&2
    exit 1
  }
}

for file in \
  "${ROOT_DIR}/adapters/codex/.codex/config.toml" \
  "${ROOT_DIR}/adapters/claude/.claude/settings.json" \
  "${ROOT_DIR}/adapters/gemini/.gemini/settings.json" \
  "${ROOT_DIR}/runtime/container/Dockerfile" \
  "${ROOT_DIR}/scripts/workcell" \
  "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"; do
  check_file "${file}"
done

DRY_RUN_OUTPUT="$("${ROOT_DIR}/scripts/workcell" --agent codex --mode strict --dry-run 2>/dev/null)"

for forbidden in "docker.sock" "SSH_AUTH_SOCK" "/.ssh" "/.aws" "Library/Keychains" ".gnupg"; do
  if echo "${DRY_RUN_OUTPUT}" | grep -q "${forbidden}"; then
    echo "Unexpected host exposure in dry-run output: ${forbidden}" >&2
    exit 1
  fi
done

for required in "--user" "HOME=/state/agent-home" "CODEX_HOME=/state/agent-home/.codex" "WORKCELL_RUNTIME=1" "--tmpfs /state:"; do
  if ! echo "${DRY_RUN_OUTPUT}" | grep -q -- "${required}"; then
    echo "Missing runtime control in dry-run output: ${required}" >&2
    exit 1
  fi
done

if "${ROOT_DIR}/scripts/workcell" --agent codex --workspace "${HOME}" --dry-run >/dev/null 2>&1; then
  echo "Expected broad workspace rejection for ${HOME}" >&2
  exit 1
fi

if [[ -d "${HOME}/.ssh" ]] && "${ROOT_DIR}/scripts/workcell" --agent codex --allow-nongit-workspace --workspace "${HOME}/.ssh" --dry-run >/dev/null 2>&1; then
  echo "Expected sensitive workspace rejection for ${HOME}/.ssh" >&2
  exit 1
fi

if [[ -d "${HOME}/.config" ]] && "${ROOT_DIR}/scripts/workcell" --agent codex --allow-nongit-workspace --workspace "${HOME}/.config" --dry-run >/dev/null 2>&1; then
  echo "Expected sensitive workspace rejection for ${HOME}/.config" >&2
  exit 1
fi

cp -R "${ROOT_DIR}/adapters/codex/.codex/." "${CODEX_VERIFY_HOME}/"
CODEX_HOME="${CODEX_VERIFY_HOME}" codex features list >/dev/null
codex execpolicy check --rules "${ROOT_DIR}/adapters/codex/.codex/rules/default.rules" rm -rf build | jq -e '.decision == "forbidden"' >/dev/null
codex execpolicy check --rules "${ROOT_DIR}/adapters/codex/.codex/rules/default.rules" git push origin feature | jq -e '.decision == "prompt"' >/dev/null
codex execpolicy check --rules "${ROOT_DIR}/adapters/codex/.codex/rules/default.rules" git push origin main --force | jq -e '.decision == "forbidden"' >/dev/null
python3 -m json.tool "${ROOT_DIR}/adapters/claude/.claude/settings.json" >/dev/null
python3 -m json.tool "${ROOT_DIR}/adapters/gemini/.gemini/settings.json" >/dev/null

echo "Workcell invariant verification passed."
