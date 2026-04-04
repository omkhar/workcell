#!/bin/bash -p
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  exec /usr/bin/env -i \
    GITHUB_REPOSITORY="${GITHUB_REPOSITORY-}" \
    GITHUB_TOKEN="${GITHUB_TOKEN-}" \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME="${HOME:-/tmp}" \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_SANITIZED_ENTRYPOINT=1 \
    /bin/bash -p "$0" "$@"
fi
set -euo pipefail
export PATH="${TRUSTED_HOST_PATH}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/go-run-env.sh"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "publish-github-release-entrypoint-ok"
  exit 0
fi

usage() {
  cat <<'EOF' >&2
Usage: publish-github-release.sh TAG FILE...
EOF
  exit 2
}

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

[[ $# -ge 2 ]] || usage

TAG_NAME="$1"
shift

[[ -n "${GITHUB_TOKEN:-}" ]] || {
  echo "GITHUB_TOKEN is required" >&2
  exit 1
}

[[ -n "${GITHUB_REPOSITORY:-}" ]] || {
  echo "GITHUB_REPOSITORY is required" >&2
  exit 1
}

require_tool curl

for path in "$@"; do
  [[ -f "${path}" ]] || {
    echo "Missing release asset: ${path}" >&2
    exit 1
  }
done

api() {
  local method="$1"
  local url="$2"
  local body_path="${3:-}"
  local response_path="$4"
  local status

  if [[ -n "${body_path}" ]]; then
    status="$(curl -sS \
      -o "${response_path}" \
      -w '%{http_code}' \
      -X "${method}" \
      -H "Authorization: Bearer ${GITHUB_TOKEN}" \
      -H 'Accept: application/vnd.github+json' \
      -H 'X-GitHub-Api-Version: 2022-11-28' \
      -H 'Content-Type: application/json' \
      --data @"${body_path}" \
      "${url}")"
  else
    status="$(curl -sS \
      -o "${response_path}" \
      -w '%{http_code}' \
      -X "${method}" \
      -H "Authorization: Bearer ${GITHUB_TOKEN}" \
      -H 'Accept: application/vnd.github+json' \
      -H 'X-GitHub-Api-Version: 2022-11-28' \
      "${url}")"
  fi

  printf '%s\n' "${status}"
}

TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/workcell-release-api.XXXXXX")"
HOSTUTIL_BIN="$(mktemp "${TMPDIR:-/tmp}/workcell-hostutil.XXXXXX")"

cleanup() {
  rm -rf "${TMP_ROOT}"
  rm -f "${HOSTUTIL_BIN}"
}

trap cleanup EXIT
build_go_tool_in_repo "${ROOT_DIR}" "${HOSTUTIL_BIN}" ./cmd/workcell-hostutil

release_url="https://api.github.com/repos/${GITHUB_REPOSITORY}/releases/tags/${TAG_NAME}"
release_json="${TMP_ROOT}/release.json"
create_payload="${TMP_ROOT}/create.json"

status="$(api GET "${release_url}" "" "${release_json}")"
case "${status}" in
  200) ;;
  404)
    "${HOSTUTIL_BIN}" release create-payload "${TAG_NAME}" "${create_payload}"
    status="$(api POST "https://api.github.com/repos/${GITHUB_REPOSITORY}/releases" "${create_payload}" "${release_json}")"
    [[ "${status}" == "201" ]] || {
      echo "GitHub release creation failed with status ${status}" >&2
      cat "${release_json}" >&2
      exit 1
    }
    ;;
  *)
    echo "GitHub release lookup failed with status ${status}" >&2
    cat "${release_json}" >&2
    exit 1
    ;;
esac

"${HOSTUTIL_BIN}" release metadata "${release_json}" "${TMP_ROOT}/release-metadata.txt" "$@"

release_metadata=()
while IFS= read -r -d '' item; do
  release_metadata+=("${item}")
done <"${TMP_ROOT}/release-metadata.txt"
release_id="${release_metadata[0]}"
upload_url="${release_metadata[1]}"

for ((i = 2; i < ${#release_metadata[@]}; i += 2)); do
  asset_name="${release_metadata[i]}"
  asset_id="${release_metadata[i+1]}"

  if [[ -n "${asset_id}" ]]; then
    delete_status="$(api DELETE "https://api.github.com/repos/${GITHUB_REPOSITORY}/releases/assets/${asset_id}" "" "${TMP_ROOT}/delete-${asset_id}.json")"
    [[ "${delete_status}" == "204" ]] || {
      echo "Failed to delete existing GitHub release asset ${asset_name} (${asset_id})" >&2
      cat "${TMP_ROOT}/delete-${asset_id}.json" >&2
      exit 1
    }
  fi
done

for path in "$@"; do
  asset_name="$(basename "${path}")"
  encoded_name="$("${HOSTUTIL_BIN}" release encode-name "${asset_name}")"
  upload_response="${TMP_ROOT}/upload-${asset_name}.json"
  upload_status="$(curl -sS \
    -o "${upload_response}" \
    -w '%{http_code}' \
    -X POST \
    -H "Authorization: Bearer ${GITHUB_TOKEN}" \
    -H 'Accept: application/vnd.github+json' \
    -H 'X-GitHub-Api-Version: 2022-11-28' \
    -H 'Content-Type: application/octet-stream' \
    --data-binary @"${path}" \
    "${upload_url}?name=${encoded_name}")"
  [[ "${upload_status}" == "201" ]] || {
    echo "Failed to upload GitHub release asset ${asset_name}" >&2
    cat "${upload_response}" >&2
    exit 1
  }
done

printf 'Published GitHub release assets for %s (release id %s)\n' "${TAG_NAME}" "${release_id}"
