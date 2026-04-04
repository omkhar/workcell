#!/bin/bash -p
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  exec /usr/bin/env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME="${HOME:-/tmp}" \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_SANITIZED_ENTRYPOINT=1 \
    /bin/bash -p "$0" "$@"
fi
set -euo pipefail
export PATH="${TRUSTED_HOST_PATH}"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "verify-upstream-claude-release-entrypoint-ok"
  exit 0
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DOCKERFILE_PATH="${ROOT_DIR}/runtime/container/Dockerfile"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/workcell-claude-release.XXXXXX")"
readonly CLAUDE_RELEASE_ROOT="https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases"
MANIFEST_PATH="${TMP_ROOT}/manifest.json"

cleanup() {
  rm -rf "${TMP_ROOT}"
}

trap cleanup EXIT

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

extract_claude_version() {
  (cd "${ROOT_DIR}" && go run ./cmd/workcell-metadatautil extract-dockerfile-arg "${DOCKERFILE_PATH}" CLAUDE_VERSION)
}

extract_claude_sha() {
  local target_arch="$1"
  (cd "${ROOT_DIR}" && go run ./cmd/workcell-metadatautil extract-claude-sha "${DOCKERFILE_PATH}" "${target_arch}")
}

verify_asset() {
  local target_arch="$1"
  local platform="$2"
  local expected_sha="$3"
  local work_dir="${TMP_ROOT}/${target_arch}"
  local binary_path="${work_dir}/claude"

  mkdir -p "${work_dir}"
  curl -fsSL "${CLAUDE_RELEASE_ROOT}/${CLAUDE_VERSION}/${platform}/claude" -o "${binary_path}"
  echo "${expected_sha}  ${binary_path}" | sha256sum -c - >/dev/null
}

manifest_sha() {
  local platform="$1"
  (cd "${ROOT_DIR}" && go run ./cmd/workcell-metadatautil manifest-checksum "${MANIFEST_PATH}" "${platform}")
}

require_tool curl
require_tool go
require_tool sha256sum

CLAUDE_VERSION="$(extract_claude_version)"
curl -fsSL "${CLAUDE_RELEASE_ROOT}/${CLAUDE_VERSION}/manifest.json" -o "${MANIFEST_PATH}"

(cd "${ROOT_DIR}" && go run ./cmd/workcell-metadatautil manifest-version "${MANIFEST_PATH}" "${CLAUDE_VERSION}")

arm64_sha="$(extract_claude_sha arm64)"
amd64_sha="$(extract_claude_sha amd64)"

if [[ "${arm64_sha}" != "$(manifest_sha linux-arm64)" ]]; then
  echo "Pinned arm64 Claude checksum does not match Anthropic's manifest" >&2
  exit 1
fi
if [[ "${amd64_sha}" != "$(manifest_sha linux-x64)" ]]; then
  echo "Pinned amd64 Claude checksum does not match Anthropic's manifest" >&2
  exit 1
fi

verify_asset arm64 linux-arm64 "${arm64_sha}"
verify_asset amd64 linux-x64 "${amd64_sha}"

echo "Workcell upstream Claude release verification passed."
