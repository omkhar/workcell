#!/bin/bash -p
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  exec /usr/bin/env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME="${HOME:-/tmp}" \
    SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH-}" \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_BUILD_INPUT_REF="${WORKCELL_BUILD_INPUT_REF-}" \
    WORKCELL_BUILD_INPUT_REQUIRE_TRACKED="${WORKCELL_BUILD_INPUT_REQUIRE_TRACKED-}" \
    WORKCELL_BUILD_INPUT_ROOT="${WORKCELL_BUILD_INPUT_ROOT-}" \
    WORKCELL_SANITIZED_ENTRYPOINT=1 \
    /bin/bash -p "$0" "$@"
fi
set -euo pipefail
export PATH="${TRUSTED_HOST_PATH}"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "generate-build-input-manifest-entrypoint-ok"
  exit 0
fi

ROOT_DIR="${WORKCELL_BUILD_INPUT_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
OUTPUT_PATH="${1:-}"
DOCKERFILE_PATH="${ROOT_DIR}/runtime/container/Dockerfile"
PACKAGE_JSON_PATH="${ROOT_DIR}/runtime/container/providers/package.json"
PACKAGE_LOCK_PATH="${ROOT_DIR}/runtime/container/providers/package-lock.json"
BUILD_REF="${WORKCELL_BUILD_INPUT_REF:-$(git -C "${ROOT_DIR}" rev-parse HEAD 2>/dev/null || printf 'UNKNOWN')}"
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "${ROOT_DIR}" log -1 --pretty=%ct 2>/dev/null || printf '0')}"
REQUIRE_TRACKED="${WORKCELL_BUILD_INPUT_REQUIRE_TRACKED:-0}"

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

resolve_go_bin() {
  local candidate

  if candidate="$(command -v go 2>/dev/null)"; then
    printf '%s\n' "${candidate}"
    return 0
  fi

  for candidate in \
    /opt/homebrew/bin/go \
    /usr/local/go/bin/go \
    /usr/local/bin/go \
    /usr/bin/go; do
    if [[ -x "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done

  echo "Missing required tool: go" >&2
  exit 1
}

resolve_output_path() {
  local candidate="$1"

  case "${candidate}" in
    /*) printf '%s\n' "${candidate}" ;;
    ./*) printf '%s/%s\n' "$(pwd -P)" "${candidate#./}" ;;
    *) printf '%s/%s\n' "$(pwd -P)" "${candidate}" ;;
  esac
}

[[ -n "${OUTPUT_PATH}" ]] || {
  echo "usage: $0 OUTPUT_PATH" >&2
  exit 64
}

GO_BIN="$(resolve_go_bin)"
OUTPUT_PATH="$(resolve_output_path "${OUTPUT_PATH}")"

(cd "${ROOT_DIR}" && "${GO_BIN}" run ./cmd/workcell-metadatautil generate-build-input-manifest "${DOCKERFILE_PATH}" "${PACKAGE_JSON_PATH}" "${PACKAGE_LOCK_PATH}" "${OUTPUT_PATH}" "${BUILD_REF}" "${SOURCE_DATE_EPOCH}" "${REQUIRE_TRACKED}")
