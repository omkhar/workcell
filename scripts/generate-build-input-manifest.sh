#!/bin/bash -p
# generated-artifact: none (release artifact under dist/; scripts/verify-build-input-manifest.sh proves determinism)
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/generate-build-input-manifest.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
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
    /bin/bash -p "${workcell_entry_root}/scripts/generate-build-input-manifest.sh" "$@"
fi
set -euo pipefail
export PATH="${TRUSTED_HOST_PATH}"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "${workcell_entry_root}/scripts/generate-build-input-manifest.sh" >/dev/null
  echo "generate-build-input-manifest-entrypoint-ok"
  exit 0
fi

OUTPUT_PATH="${1:-}"

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

resolve_root_path() {
  local candidate="$1"
  (
    cd "${candidate}"
    pwd -P
  )
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

ROOT_DIR="$(resolve_root_path "${WORKCELL_BUILD_INPUT_ROOT:-${workcell_entry_root}}")"
DOCKERFILE_PATH="${ROOT_DIR}/runtime/container/Dockerfile"
PACKAGE_JSON_PATH="${ROOT_DIR}/runtime/container/providers/package.json"
PACKAGE_LOCK_PATH="${ROOT_DIR}/runtime/container/providers/package-lock.json"
BUILD_REF="${WORKCELL_BUILD_INPUT_REF:-$(git -C "${ROOT_DIR}" rev-parse HEAD 2>/dev/null || printf 'UNKNOWN')}"
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "${ROOT_DIR}" log -1 --pretty=%ct 2>/dev/null || printf '0')}"
REQUIRE_TRACKED="${WORKCELL_BUILD_INPUT_REQUIRE_TRACKED:-0}"
GO_BIN="$(resolve_go_bin)"
OUTPUT_PATH="$(resolve_output_path "${OUTPUT_PATH}")"

(cd "${ROOT_DIR}" && "${GO_BIN}" run ./cmd/workcell-citools generate-build-input-manifest "${DOCKERFILE_PATH}" "${PACKAGE_JSON_PATH}" "${PACKAGE_LOCK_PATH}" "${OUTPUT_PATH}" "${BUILD_REF}" "${SOURCE_DATE_EPOCH}" "${REQUIRE_TRACKED}")
