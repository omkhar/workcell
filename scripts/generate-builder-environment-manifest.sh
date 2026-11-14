#!/bin/bash -p
# generated-artifact: none (records the live builder environment at release time)
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/generate-builder-environment-manifest.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  exec /usr/bin/env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME=/tmp \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_BUILDER_ENV_DOCKER_CONTEXT="${WORKCELL_BUILDER_ENV_DOCKER_CONTEXT-}" \
    WORKCELL_BUILDKIT_IMAGE="${WORKCELL_BUILDKIT_IMAGE-}" \
    WORKCELL_BUILDX_VERSION="${WORKCELL_BUILDX_VERSION-}" \
    WORKCELL_COSIGN_VERSION="${WORKCELL_COSIGN_VERSION-}" \
    WORKCELL_QEMU_IMAGE="${WORKCELL_QEMU_IMAGE-}" \
    WORKCELL_SANITIZED_ENTRYPOINT=1 \
    WORKCELL_SYFT_VERSION="${WORKCELL_SYFT_VERSION-}" \
    /bin/bash -p "${workcell_entry_root}/scripts/generate-builder-environment-manifest.sh" "$@"
fi
set -euo pipefail
export PATH="${TRUSTED_HOST_PATH}"

ROOT_DIR="${workcell_entry_root}"
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "${workcell_entry_root}/scripts/generate-builder-environment-manifest.sh" >/dev/null
  echo "generate-builder-environment-manifest-entrypoint-ok"
  exit 0
fi

OUTPUT_PATH="${1:-}"
DOCKER_CONTEXT_NAME="${WORKCELL_BUILDER_ENV_DOCKER_CONTEXT:-}"
BUILDKIT_IMAGE="${WORKCELL_BUILDKIT_IMAGE:-}"
BUILDX_VERSION_TARGET="${WORKCELL_BUILDX_VERSION:-}"
COSIGN_VERSION_TARGET="${WORKCELL_COSIGN_VERSION:-}"
QEMU_IMAGE="${WORKCELL_QEMU_IMAGE:-}"
SYFT_VERSION_TARGET="${WORKCELL_SYFT_VERSION:-}"

# A set image must name its reviewed repository with no registry host or other
# prefix, in the format policy/tool-pins.toml requires. The QEMU image runs
# privileged below. The bracket lists are spelled out so the match does not
# depend on the locale.
readonly IMAGE_TAG_DIGEST_RE=':[ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_][ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_.-]*@sha256:[0123456789abcdef]{64}$'
readonly BUILDKIT_IMAGE_RE="^moby/buildkit${IMAGE_TAG_DIGEST_RE}"
readonly QEMU_IMAGE_RE="^tonistiigi/binfmt${IMAGE_TAG_DIGEST_RE}"
if [[ -n "${BUILDKIT_IMAGE}" && ! "${BUILDKIT_IMAGE}" =~ ${BUILDKIT_IMAGE_RE} ]]; then
  echo "WORKCELL_BUILDKIT_IMAGE must be moby/buildkit:<tag>@sha256:<digest>, found: ${BUILDKIT_IMAGE}" >&2
  exit 1
fi
if [[ -n "${QEMU_IMAGE}" && ! "${QEMU_IMAGE}" =~ ${QEMU_IMAGE_RE} ]]; then
  echo "WORKCELL_QEMU_IMAGE must be tonistiigi/binfmt:<tag>@sha256:<digest>, found: ${QEMU_IMAGE}" >&2
  exit 1
fi

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

docker_cmd() {
  if [[ -n "${DOCKER_CONTEXT_NAME}" ]]; then
    docker --context "${DOCKER_CONTEXT_NAME}" "$@"
  else
    docker "$@"
  fi
}

cleanup() {
  cleanup_workcell_trusted_docker_client
}

trap cleanup EXIT

if [[ "${1:-}" == "--self-docker-probe" ]]; then
  require_tool docker
  setup_workcell_trusted_docker_client
  if [[ -n "${DOCKER_CONTEXT_NAME:-}" ]]; then
    select_workcell_docker_context "Requested Docker context" "No healthy Docker context found" colima default
  fi
  buildx_cmd version >/dev/null
  echo "generate-builder-environment-manifest-docker-probe-ok"
  exit 0
fi

[[ -n "${OUTPUT_PATH}" ]] || {
  echo "usage: $0 OUTPUT_PATH" >&2
  exit 64
}

require_tool docker
require_tool go
setup_workcell_trusted_docker_client
select_workcell_docker_context "Requested Docker context" "No healthy Docker context found" colima default

docker_version_json="$(docker_cmd version --format '{{json .}}')"
buildx_version="$(buildx_cmd version)"
buildx_inspect="$(buildx_cmd inspect --bootstrap)"
cosign_version=""
curl_version="$(curl --version 2>/dev/null | head -n1 || true)"
gzip_version="$(gzip --version 2>/dev/null | head -n1 || true)"
git_version="$(git --version 2>/dev/null || true)"
qemu_version=""
syft_version=""
tar_version="$(tar --version 2>/dev/null | head -n1 || true)"

if command -v cosign >/dev/null 2>&1; then
  cosign_version="$(cosign version 2>/dev/null || true)"
fi

if [[ -n "${QEMU_IMAGE}" ]]; then
  qemu_version="$(docker_cmd run --privileged --rm "${QEMU_IMAGE}" --version)"
fi

if command -v syft >/dev/null 2>&1; then
  syft_version="$(syft version 2>/dev/null || true)"
fi

(cd "${ROOT_DIR}" && go run ./cmd/workcell-citools generate-builder-environment-manifest "${OUTPUT_PATH}" "${BUILDKIT_IMAGE}" "${BUILDX_VERSION_TARGET}" "${COSIGN_VERSION_TARGET}" "${QEMU_IMAGE}" "${SYFT_VERSION_TARGET}" "${buildx_version}" "${buildx_inspect}" "${docker_version_json}" "${qemu_version}" "${cosign_version}" "${curl_version}" "${git_version}" "${gzip_version}" "${syft_version}" "${tar_version}")
