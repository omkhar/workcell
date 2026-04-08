#!/bin/bash -p
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/local/go/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  exec /usr/bin/env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME="${HOME:-/tmp}" \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_MAX_DEBIAN_SNAPSHOT_AGE_DAYS="${WORKCELL_MAX_DEBIAN_SNAPSHOT_AGE_DAYS-}" \
    WORKCELL_SANITIZED_ENTRYPOINT=1 \
    /bin/bash -p "$0" "$@"
fi
set -euo pipefail
export PATH="${TRUSTED_HOST_PATH}"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "check-pinned-inputs-entrypoint-ok"
  exit 0
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DOCKERFILE_PATH="${ROOT_DIR}/runtime/container/Dockerfile"
VALIDATOR_DOCKERFILE_PATH="${ROOT_DIR}/tools/validator/Dockerfile"
REMOTE_VALIDATOR_DOCKERFILE_PATH="${ROOT_DIR}/tools/remote-validator/Dockerfile"
PROVIDERS_PACKAGE_JSON_PATH="${ROOT_DIR}/runtime/container/providers/package.json"
PROVIDERS_PACKAGE_LOCK_PATH="${ROOT_DIR}/runtime/container/providers/package-lock.json"
WORKFLOWS_DIR="${ROOT_DIR}/.github/workflows"
CI_WORKFLOW_PATH="${ROOT_DIR}/.github/workflows/ci.yml"
RELEASE_WORKFLOW_PATH="${ROOT_DIR}/.github/workflows/release.yml"
PIN_HYGIENE_WORKFLOW_PATH="${ROOT_DIR}/.github/workflows/pin-hygiene.yml"
CODEOWNERS_PATH="${ROOT_DIR}/.github/CODEOWNERS"
CODEX_REQUIREMENTS_PATH="${ROOT_DIR}/adapters/codex/requirements.toml"
CODEX_MCP_CONFIG_PATH="${ROOT_DIR}/adapters/codex/mcp/config.toml"
HOSTED_CONTROLS_POLICY_PATH="${ROOT_DIR}/policy/github-hosted-controls.toml"
HOSTED_CONTROLS_SCRIPT_PATH="${ROOT_DIR}/scripts/verify-github-hosted-controls.sh"
PROVIDER_BUMP_POLICY_PATH="${ROOT_DIR}/policy/provider-bumps.toml"
MAX_DEBIAN_SNAPSHOT_AGE_DAYS="${WORKCELL_MAX_DEBIAN_SNAPSHOT_AGE_DAYS:-45}"

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

GO_BIN="${WORKCELL_GO_BIN:-}"

resolve_go_bin() {
  if [[ -n "${GO_BIN}" && -x "${GO_BIN}" ]]; then
    return 0
  fi
  if GO_BIN="$(command -v go 2>/dev/null)"; then
    return 0
  fi
  for candidate in \
    /opt/homebrew/bin/go \
    /usr/local/go/bin/go \
    /usr/local/bin/go \
    /usr/bin/go; do
    if [[ -x "${candidate}" ]]; then
      GO_BIN="${candidate}"
      return 0
    fi
  done
  echo "Missing required tool: go" >&2
  exit 1
}

resolve_go_bin

(cd "${ROOT_DIR}" && "${GO_BIN}" run ./cmd/workcell-metadatautil check-pinned-inputs "${DOCKERFILE_PATH}" "${VALIDATOR_DOCKERFILE_PATH}" "${REMOTE_VALIDATOR_DOCKERFILE_PATH}" "${PROVIDERS_PACKAGE_JSON_PATH}" "${PROVIDERS_PACKAGE_LOCK_PATH}" "${WORKFLOWS_DIR}" "${CI_WORKFLOW_PATH}" "${RELEASE_WORKFLOW_PATH}" "${PIN_HYGIENE_WORKFLOW_PATH}" "${CODEOWNERS_PATH}" "${CODEX_REQUIREMENTS_PATH}" "${CODEX_MCP_CONFIG_PATH}" "${HOSTED_CONTROLS_POLICY_PATH}" "${HOSTED_CONTROLS_SCRIPT_PATH}" "${PROVIDER_BUMP_POLICY_PATH}" "${MAX_DEBIAN_SNAPSHOT_AGE_DAYS}")
