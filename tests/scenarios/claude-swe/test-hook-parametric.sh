#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=tests/scenarios/claude-swe/test-hook-parametric.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"

FIXTURE="${ROOT_DIR}/tests/scenarios/claude-swe/blocked-git-commands.txt"
HOOK="${ROOT_DIR}/adapters/claude/hooks/guard-bash.sh"

blocked=0
failures=0

while IFS= read -r cmd; do
  [[ -z "${cmd}" ]] && continue
  payload="$(jq -cn --arg c "${cmd}" '{"tool":"Bash","tool_input":{"command":$c}}')"
  exit_code=0
  printf '%s' "${payload}" | bash "${HOOK}" >/dev/null 2>&1 || exit_code=$?
  if [[ "${exit_code}" -eq 2 ]]; then
    blocked=$((blocked + 1))
  else
    echo "FAIL: command not blocked (exit ${exit_code}): ${cmd}" >&2
    failures=$((failures + 1))
  fi
done <"${FIXTURE}"

if [[ "${failures}" -gt 0 ]]; then
  echo "${failures} command(s) were not blocked as expected." >&2
  exit 1
fi

PERMITTED_FIXTURE="${ROOT_DIR}/tests/scenarios/claude-swe/permitted-git-commands.txt"

not_blocked=0
false_blocks=0

while IFS= read -r cmd; do
  [[ -z "${cmd}" ]] && continue
  payload="$(jq -cn --arg c "${cmd}" '{"tool":"Bash","tool_input":{"command":$c}}')"
  exit_code=0
  printf '%s' "${payload}" | bash "${HOOK}" >/dev/null 2>&1 || exit_code=$?
  if [[ "${exit_code}" -ne 2 ]]; then
    not_blocked=$((not_blocked + 1))
  else
    echo "FAIL: legitimate command was blocked: ${cmd}" >&2
    false_blocks=$((false_blocks + 1))
  fi
done <"${PERMITTED_FIXTURE}"

if [[ "${false_blocks}" -gt 0 ]]; then
  echo "${false_blocks} legitimate command(s) were incorrectly blocked." >&2
  exit 1
fi

echo "Hook negative test passed: ${not_blocked} legitimate commands correctly permitted"

echo "Hook parametric tests passed: ${blocked} commands blocked, ${not_blocked} commands permitted"
