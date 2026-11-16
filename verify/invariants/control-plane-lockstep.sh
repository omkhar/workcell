#!/usr/bin/env -S BASH_ENV= ENV= bash
# verify/invariants/control-plane-lockstep.sh
#
# Concrete lockstep invariant for AGENTS.md L157 ("runtime/, policy/,
# adapters/, verify/, workflows/ should evolve in lockstep").  Asserts that
# every policy/*.{toml,tsv,json} file is mentioned by name in at least one
# user-facing markdown surface, so a new policy file cannot land without an
# operator-visible doc pointer.
#
# This is the first concrete check living under verify/invariants/; the
# directory previously held only narrative markdown.
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=verify/invariants/control-plane-lockstep.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}

set -euo pipefail

ROOT_DIR="${workcell_entry_root}"
SEARCH_ROOTS=(
  "${ROOT_DIR}/docs"
  "${ROOT_DIR}/verify"
  "${ROOT_DIR}/README.md"
  "${ROOT_DIR}/SECURITY.md"
  "${ROOT_DIR}/AGENTS.md"
  "${ROOT_DIR}/CONTRIBUTING.md"
)

policy_name_is_documented() {
  local base="$1"
  local root=""
  local path=""

  for root in "${SEARCH_ROOTS[@]}"; do
    if [[ -f "${root}" ]]; then
      case "${root}" in
        *.md)
          if grep -qF "${base}" "${root}" 2>/dev/null; then
            return 0
          fi
          ;;
      esac
      continue
    fi

    [[ -d "${root}" ]] || continue
    while IFS= read -r -d '' path; do
      if grep -qF "${base}" "${path}" 2>/dev/null; then
        return 0
      fi
    done < <(find "${root}" -type f -name '*.md' -print0)
  done

  return 1
}

missing=()
for path in "${ROOT_DIR}"/policy/*; do
  base="$(basename "${path}")"
  case "${base}" in
    README.md) continue ;;
  esac
  if ! policy_name_is_documented "${base}"; then
    missing+=("${base}")
  fi
done

if [[ "${#missing[@]}" -gt 0 ]]; then
  echo "Control-plane lockstep invariant failed: policy/ files missing from user-facing docs:" >&2
  for name in "${missing[@]}"; do
    echo "  - policy/${name}" >&2
  done
  exit 1
fi

echo "Control-plane lockstep invariant passed: every policy/ file is documented."
