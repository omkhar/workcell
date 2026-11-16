#!/bin/bash -p
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/publish-github-release.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
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
    /bin/bash -p "${workcell_entry_root}/scripts/publish-github-release.sh" "$@"
fi
set -euo pipefail
export PATH="${TRUSTED_HOST_PATH}"
unset WORKCELL_SANITIZED_ENTRYPOINT
export CGO_ENABLED=0 GOENV=off GOTOOLCHAIN=local GOWORK=off
PUBLISH_TOKEN="${GITHUB_TOKEN:-}"
export -n PUBLISH_TOKEN 2>/dev/null || true
unset GITHUB_TOKEN
ROOT_DIR="${workcell_entry_root}"
# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/go-run-env.sh"
# A library may change shell options, so pipefail is set again for the
# pipelines below.
set -o pipefail

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "${workcell_entry_root}/scripts/publish-github-release.sh" >/dev/null
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

WORKCELL_GO_BIN="$(resolve_exact_toolchain_go_bin "${ROOT_DIR}")" || {
  echo "The exact Go toolchain from go.mod is unavailable at a trusted absolute path." >&2
  exit 1
}
readonly WORKCELL_GO_BIN
export -n WORKCELL_GO_BIN 2>/dev/null || true

HOSTUTIL_BIN="$(mktemp "${TMPDIR:-/tmp}/workcell-hostutil.XXXXXX")"

cleanup() {
  rm -f "${HOSTUTIL_BIN}"
}

trap cleanup EXIT
build_go_tool_in_repo "${ROOT_DIR}" "${HOSTUTIL_BIN}" ./cmd/workcell-hostutil
"${HOSTUTIL_BIN}" release classify-tag "${TAG_NAME}" >/dev/null
# A command a variable names may change shell options, so pipefail is set
# again for the pipelines below.
set -o pipefail

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
