#!/bin/bash -p
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/verify-upstream-gemini-release.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
# shellcheck source=scripts/lib/trusted-entrypoint.sh
source "${workcell_entry_root}/scripts/lib/trusted-entrypoint.sh"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "${workcell_entry_root}/scripts/verify-upstream-gemini-release.sh" >/dev/null
  echo "verify-upstream-gemini-release-entrypoint-ok"
  exit 0
fi

ROOT_DIR="${workcell_entry_root}"
PACKAGE_JSON_PATH="${ROOT_DIR}/runtime/container/providers/package.json"
PACKAGE_LOCK_PATH="${ROOT_DIR}/runtime/container/providers/package-lock.json"

require_tool curl
require_tool jq
# shellcheck source=scripts/lib/npm-registry-verify.sh
source "${ROOT_DIR}/scripts/lib/npm-registry-verify.sh"

gemini_version="$(jq -r '.dependencies["@google/gemini-cli"] // empty' "${PACKAGE_JSON_PATH}")"
if [[ -z "${gemini_version}" ]]; then
  echo "package.json must pin @google/gemini-cli" >&2
  exit 1
fi

lock_gemini_version="$(extract_lock_field "${PACKAGE_LOCK_PATH}" "node_modules/@google/gemini-cli" "version")"
lock_gemini_resolved="$(extract_lock_field "${PACKAGE_LOCK_PATH}" "node_modules/@google/gemini-cli" "resolved")"
lock_gemini_integrity="$(extract_lock_field "${PACKAGE_LOCK_PATH}" "node_modules/@google/gemini-cli" "integrity")"
if [[ "${lock_gemini_version}" != "${gemini_version}" ]]; then
  echo "Gemini package-lock version mismatch: package.json=${gemini_version}, package-lock=${lock_gemini_version}" >&2
  exit 1
fi
verify_registry_package "@google/gemini-cli" "node_modules/@google/gemini-cli" "@google%2fgemini-cli" "${lock_gemini_version}" "${lock_gemini_resolved}" "${lock_gemini_integrity}"

core_version="$(extract_lock_field "${PACKAGE_LOCK_PATH}" "node_modules/@google/gemini-cli-core" "version")"
core_resolved="$(extract_lock_field "${PACKAGE_LOCK_PATH}" "node_modules/@google/gemini-cli-core" "resolved")"
core_integrity="$(extract_lock_field "${PACKAGE_LOCK_PATH}" "node_modules/@google/gemini-cli-core" "integrity")"
if [[ -n "${core_version}" || -n "${core_resolved}" || -n "${core_integrity}" ]]; then
  if [[ -z "${core_version}" || -z "${core_resolved}" || -z "${core_integrity}" ]]; then
    echo "Gemini package-lock must either omit @google/gemini-cli-core or pin it completely" >&2
    exit 1
  fi
  verify_registry_package "@google/gemini-cli-core" "node_modules/@google/gemini-cli-core" "@google%2fgemini-cli-core" "${core_version}" "${core_resolved}" "${core_integrity}"
fi

echo "Workcell upstream Gemini release verification passed."
