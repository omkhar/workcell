#!/bin/bash -p
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
entrypoint_environment_is_sanitized() {
  local name=""
  local expected_path="${TRUSTED_HOST_PATH}"

  [[ "${PATH:-}" == "${expected_path}" ]] || return 1

  while IFS= read -r name; do
    case "${name}" in
      GITHUB_REPOSITORY | GITHUB_TOKEN | HOME | PATH | PWD | SHLVL | TMPDIR | WORKCELL_SANITIZED_ENTRYPOINT) ;;
      *) return 1 ;;
    esac
  done < <(compgen -e)
}

if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]] || ! entrypoint_environment_is_sanitized; then
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
unset WORKCELL_SANITIZED_ENTRYPOINT
export CGO_ENABLED=0 GOENV=off GOTOOLCHAIN=local GOWORK=off
PUBLISH_TOKEN="${GITHUB_TOKEN:-}"
export -n PUBLISH_TOKEN 2>/dev/null || true
unset GITHUB_TOKEN
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/go-run-env.sh"

resolve_trusted_go_bin() {
  local expected_toolchain=""
  local toolchain_version=""
  local toolcache_arch=""
  local candidate=""

  expected_toolchain="$(awk '$1 == "toolchain" { print $2; exit }' "${ROOT_DIR}/go.mod")"
  if [[ ! "${expected_toolchain}" =~ ^go[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "go.mod must declare one exact Go toolchain." >&2
    return 2
  fi
  toolchain_version="${expected_toolchain#go}"
  case "$(uname -m)" in
    arm64 | aarch64) toolcache_arch="arm64" ;;
    x86_64 | amd64) toolcache_arch="x64" ;;
    *)
      echo "GitHub release publication does not support this Go toolchain architecture." >&2
      return 2
      ;;
  esac
  for candidate in \
    "/opt/hostedtoolcache/go/${toolchain_version}/${toolcache_arch}/bin/go" \
    /opt/homebrew/bin/go \
    /usr/local/go/bin/go \
    /usr/local/bin/go \
    /usr/bin/go; do
    [[ -x "${candidate}" ]] || continue
    if GOTOOLCHAIN=local GOENV=off "${candidate}" env GOVERSION 2>/dev/null | grep -Fx "${expected_toolchain}" >/dev/null; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done
  echo "Go toolchain ${expected_toolchain} is unavailable at a trusted absolute path." >&2
  return 1
}

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "publish-github-release-entrypoint-ok"
  exit 0
fi

usage() {
  cat <<'EOF' >&2
Usage: publish-github-release.sh TAG [--expected-tag-object SHA] [--immutable-releases-preverified-by-hosted-controls] FILE...
EOF
  exit 2
}

[[ $# -ge 2 ]] || usage

TAG_NAME="$1"
shift

EXPECTED_TAG_OBJECT=""
if [[ "${1:-}" == "--expected-tag-object" ]]; then
  [[ "${2:-}" =~ ^[0-9a-f]{40}$ ]] || usage
  EXPECTED_TAG_OBJECT="$2"
  shift 2
fi

IMMUTABLE_RELEASES_PREVERIFIED="false"
if [[ "${1:-}" == "--immutable-releases-preverified-by-hosted-controls" ]]; then
  IMMUTABLE_RELEASES_PREVERIFIED="true"
  shift
fi
[[ $# -ge 1 ]] || usage
if [[ -z "${PUBLISH_TOKEN}" || "${#PUBLISH_TOKEN}" -gt 4096 || "${PUBLISH_TOKEN}" =~ [[:space:][:cntrl:]] ]]; then
  echo "GITHUB_TOKEN must be a bounded credential without whitespace or control characters." >&2
  exit 2
fi

WORKCELL_GO_BIN="$(resolve_trusted_go_bin)"
readonly WORKCELL_GO_BIN
export -n WORKCELL_GO_BIN 2>/dev/null || true

HOSTUTIL_BIN="$(mktemp "${TMPDIR:-/tmp}/workcell-hostutil.XXXXXX")"

cleanup() {
  rm -f "${HOSTUTIL_BIN}"
}

trap cleanup EXIT
build_go_tool_in_repo "${ROOT_DIR}" "${HOSTUTIL_BIN}" ./cmd/workcell-hostutil
"${HOSTUTIL_BIN}" release classify-tag "${TAG_NAME}" >/dev/null

TAG_REF="refs/tags/${TAG_NAME}"
TAG_OBJECT_SHA="$(git -C "${ROOT_DIR}" --no-replace-objects rev-parse --verify "${TAG_REF}^{tag}")" || {
  echo "Release tag ${TAG_NAME} must resolve locally to an annotated tag object." >&2
  exit 2
}
if [[ -n "${EXPECTED_TAG_OBJECT}" && "${TAG_OBJECT_SHA}" != "${EXPECTED_TAG_OBJECT}" ]]; then
  echo "Release tag ${TAG_NAME} changed from expected object ${EXPECTED_TAG_OBJECT}." >&2
  exit 2
fi
TAG_HEADERS="$(
  git -C "${ROOT_DIR}" --no-replace-objects cat-file tag "${TAG_OBJECT_SHA}" |
    /usr/bin/awk '
      $0 == "" { exit }
      index($0, "object ") == 1 { object_count++; object = substr($0, 8) }
      index($0, "type ") == 1 { type_count++; object_type = substr($0, 6) }
      index($0, "tag ") == 1 { tag_count++; name = substr($0, 5) }
      END {
        if (object_count != 1 || type_count != 1 || tag_count != 1 || object_type != "commit") exit 2
        print object
        print name
      }
    '
)" || {
  echo "Release tag ${TAG_NAME} must directly target one commit and contain canonical annotated tag headers." >&2
  exit 2
}
PEELED_COMMIT_SHA="${TAG_HEADERS%%$'\n'*}"
EMBEDDED_TAG_NAME="${TAG_HEADERS#*$'\n'}"
if [[ "${#PEELED_COMMIT_SHA}" -ne 40 || "${PEELED_COMMIT_SHA}" == *[!0-9a-f]* ]]; then
  echo "Release tag ${TAG_NAME} must directly target one lowercase 40-character commit ID." >&2
  exit 2
fi
if [[ "${EMBEDDED_TAG_NAME}" != "${TAG_NAME}" ]]; then
  echo "Release tag ${TAG_NAME} does not match embedded tag name ${EMBEDDED_TAG_NAME}." >&2
  exit 2
fi

GITHUB_TOKEN="${PUBLISH_TOKEN}" \
  "${HOSTUTIL_BIN}" release publish "${TAG_NAME}" "${TAG_OBJECT_SHA}" "${PEELED_COMMIT_SHA}" "--immutable-releases-preverified-by-hosted-controls=${IMMUTABLE_RELEASES_PREVERIFIED}" "$@"
