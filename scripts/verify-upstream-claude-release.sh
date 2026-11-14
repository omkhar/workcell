#!/bin/bash -p
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/verify-upstream-claude-release.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
# shellcheck source=scripts/lib/trusted-entrypoint.sh
source "${workcell_entry_root}/scripts/lib/trusted-entrypoint.sh"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "${workcell_entry_root}/scripts/verify-upstream-claude-release.sh" >/dev/null
  echo "verify-upstream-claude-release-entrypoint-ok"
  exit 0
fi

ROOT_DIR="${workcell_entry_root}"
DOCKERFILE_PATH="${ROOT_DIR}/runtime/container/Dockerfile"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/workcell-claude-release.XXXXXX")"
readonly CLAUDE_RELEASE_ROOT="https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases"
MANIFEST_PATH="${TMP_ROOT}/manifest.json"

cleanup() {
  rm -rf "${TMP_ROOT}"
}

trap cleanup EXIT

extract_claude_version() {
  (cd "${ROOT_DIR}" && go run ./cmd/workcell-citools extract-dockerfile-arg "${DOCKERFILE_PATH}" CLAUDE_VERSION)
}

extract_claude_sha() {
  local target_arch="$1"
  (cd "${ROOT_DIR}" && go run ./cmd/workcell-citools extract-claude-sha "${DOCKERFILE_PATH}" "${target_arch}")
}

download_large_asset() {
  local url="$1"
  local output="$2"

  curl -fsSL \
    --retry 5 \
    --retry-all-errors \
    --retry-delay 5 \
    --connect-timeout 20 \
    --speed-limit 1024 \
    --speed-time 60 \
    "${url}" -o "${output}"
}

verify_asset() {
  local target_arch="$1"
  local platform="$2"
  local expected_sha="$3"
  local work_dir="${TMP_ROOT}/${target_arch}"
  local binary_path="${work_dir}/claude"

  mkdir -p "${work_dir}"
  download_large_asset "${CLAUDE_RELEASE_ROOT}/${CLAUDE_VERSION}/${platform}/claude" "${binary_path}"
  echo "${expected_sha}  ${binary_path}" | sha256sum -c - >/dev/null # portability-exempt: require_tool sha256sum fails closed when it is absent
}

manifest_sha() {
  local platform="$1"
  (cd "${ROOT_DIR}" && go run ./cmd/workcell-citools manifest-checksum "${MANIFEST_PATH}" "${platform}")
}

require_tool curl
require_tool go
require_tool sha256sum

CLAUDE_VERSION="$(extract_claude_version)"
curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 --connect-timeout 20 \
  "${CLAUDE_RELEASE_ROOT}/${CLAUDE_VERSION}/manifest.json" -o "${MANIFEST_PATH}"

(cd "${ROOT_DIR}" && go run ./cmd/workcell-citools manifest-version "${MANIFEST_PATH}" "${CLAUDE_VERSION}")

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
