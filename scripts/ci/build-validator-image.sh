#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/ci/build-validator-image.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
source "${ROOT_DIR}/scripts/ci/lib/local-docker-parity.sh"
VALIDATOR_DOCKERFILE="${ROOT_DIR}/tools/validator/Dockerfile"
VALIDATOR_IMAGE_DEFAULT_TAG="$(workcell_validator_image_default_tag "${ROOT_DIR}")"
VALIDATOR_IMAGE="${WORKCELL_VALIDATOR_IMAGE:-${VALIDATOR_IMAGE_DEFAULT_TAG}}"
REBUILD_VALIDATOR="${WORKCELL_REBUILD_VALIDATOR_IMAGE:-0}"
CACHE_MODE="${WORKCELL_VALIDATOR_BUILDX_CACHE_MODE:-none}"
# CACHE_SCOPE segregates the buildx gha cache so PR runs cannot poison
# the main scope.  Callers supply WORKCELL_VALIDATOR_BUILDX_CACHE_SCOPE
# from workflow ref state (set to "validator-main" on push events;
# "validator-pr-${PR_NUMBER}" on pull_request events).  When unset the
# script falls back to a host-local scope so local invocations do not
# collide with CI scopes.
CACHE_SCOPE="${WORKCELL_VALIDATOR_BUILDX_CACHE_SCOPE:-validator-local}"
# CACHE_WRITE_MODE defaults to "min" to avoid storing every intermediate
# layer in the gha cache from untrusted PR sources.  CI sets this to
# "max" only on push-to-main runs where the cache is operator-trusted.
CACHE_WRITE_MODE="${WORKCELL_VALIDATOR_BUILDX_CACHE_WRITE_MODE:-min}"
BUILDKIT_IMAGE="${WORKCELL_BUILDKIT_IMAGE:-moby/buildkit:buildx-stable-1@sha256:baf6138eb8c37644800c7c913fba051e840ebb9ba13eaf92d92ef9a1712d0b4c}"
# The image must name the reviewed BuildKit repository with no registry host or
# other prefix, in the format policy/tool-pins.toml requires. The bracket lists
# are spelled out so the match does not depend on the locale.
readonly BUILDKIT_IMAGE_RE='^moby/buildkit:[ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_][ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_.-]*@sha256:[0123456789abcdef]{64}$'
if [[ ! "${BUILDKIT_IMAGE}" =~ ${BUILDKIT_IMAGE_RE} ]]; then
  echo "WORKCELL_BUILDKIT_IMAGE must be moby/buildkit:<tag>@sha256:<digest>, found: ${BUILDKIT_IMAGE}" >&2
  exit 1
fi

cleanup() {
  cleanup_workcell_ci_docker
}
trap cleanup EXIT

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

require_tool docker
setup_workcell_ci_docker
buildx_cmd version >/dev/null 2>&1 || {
  echo "docker buildx is required for validator-image parity" >&2
  exit 1
}

if [[ "${REBUILD_VALIDATOR}" -eq 0 ]] && workcell_ci_docker image inspect "${VALIDATOR_IMAGE}" >/dev/null 2>&1; then
  echo "${VALIDATOR_IMAGE}"
  exit 0
fi

build_cmd=(
  buildx_cmd build
  --build-arg "WORKCELL_BUILDKIT_IMAGE=${BUILDKIT_IMAGE}"
  -f "${VALIDATOR_DOCKERFILE}"
  -t "${VALIDATOR_IMAGE}"
  --load
)

case "${CACHE_MODE}" in
  gha)
    build_cmd+=(--cache-from "type=gha,scope=${CACHE_SCOPE}")
    # Read from main as a base when on a PR scope so PR builds still
    # benefit from the operator-trusted main cache without write access.
    if [[ "${CACHE_SCOPE}" != "validator-main" ]]; then
      build_cmd+=(--cache-from "type=gha,scope=validator-main")
    fi
    build_cmd+=(--cache-to "type=gha,scope=${CACHE_SCOPE},mode=${CACHE_WRITE_MODE}")
    ;;
  none) ;;
  *)
    echo "Unsupported validator buildx cache mode: ${CACHE_MODE}" >&2
    exit 2
    ;;
esac

build_cmd+=("${ROOT_DIR}")
"${build_cmd[@]}"
echo "${VALIDATOR_IMAGE}"
