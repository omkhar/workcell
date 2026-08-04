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
Usage: publish-github-release.sh TAG [--immutable-releases-preverified-by-hosted-controls] FILE...
EOF
  exit 2
}

[[ $# -ge 2 ]] || usage

TAG_NAME="$1"
shift

IMMUTABLE_RELEASES_PREVERIFIED="false"
if [[ "${1:-}" == "--immutable-releases-preverified-by-hosted-controls" ]]; then
  IMMUTABLE_RELEASES_PREVERIFIED="true"
  shift
fi
[[ $# -ge 1 ]] || usage

HOSTUTIL_BIN="$(mktemp "${TMPDIR:-/tmp}/workcell-hostutil.XXXXXX")"

cleanup() {
  rm -f "${HOSTUTIL_BIN}"
}

trap cleanup EXIT
build_go_tool_in_repo "${ROOT_DIR}" "${HOSTUTIL_BIN}" ./cmd/workcell-hostutil

TAG_REF="refs/tags/${TAG_NAME}"
TAG_OBJECT_SHA="$(git -C "${ROOT_DIR}" --no-replace-objects rev-parse --verify "${TAG_REF}^{tag}")" || {
  echo "Release tag ${TAG_NAME} must resolve locally to an annotated tag object." >&2
  exit 2
}
PEELED_COMMIT_SHA="$(git -C "${ROOT_DIR}" --no-replace-objects rev-parse --verify "${TAG_REF}^{commit}")" || {
  echo "Release tag ${TAG_NAME} must peel locally to exactly one commit." >&2
  exit 2
}

"${HOSTUTIL_BIN}" release publish "${TAG_NAME}" "${TAG_OBJECT_SHA}" "${PEELED_COMMIT_SHA}" "--immutable-releases-preverified-by-hosted-controls=${IMMUTABLE_RELEASES_PREVERIFIED}" "$@"
