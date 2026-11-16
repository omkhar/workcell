#!/usr/bin/env -S BASH_ENV= ENV= bash
# Scope guards for the upstream-refresh candidates.
#
# Usage: upstream-refresh-scope-guard.sh PATCH_FILE
#        upstream-refresh-scope-guard.sh --toolchain PATCH_FILE OUTPUT_FILE
#
# Thin dispatch. The policies live in metadatautil.
#
# With PATCH_FILE alone, the provider guard (CheckUpstreamRefreshScope) exits 0
# only when every change in the git patch is inside the agent-bump surface. A
# patch that is out of scope is still a valid PR, but a human must review it.
#
# With --toolchain, the toolchain guard (CheckUpstreamRefreshToolchainScope)
# applies the patch to HEAD of this checkout in a private index. It exits 0
# only when the tree changes nothing but toolchain pin lines, and then appends
# verdict=passed:<base commit>:<tree> to OUTPUT_FILE, the step output file. The
# publish script publishes that tree on that base and no other.
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/ci/upstream-refresh-scope-guard.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

usage() {
  echo "Usage: $0 PATCH_FILE" >&2
  echo "       $0 --toolchain PATCH_FILE OUTPUT_FILE" >&2
  exit 2
}

toolchain=0
if [[ "${1:-}" == --toolchain ]]; then
  toolchain=1
  shift
  [[ $# -eq 2 ]] || usage
  # The output file is resolved like the patch.
  [[ $2 == /* ]] || set -- "$1" "${PWD}/$2"
else
  [[ $# -eq 1 ]] || usage
fi

# The Go command runs from the repo root, so resolve the patch path first.
[[ $1 == /* ]] || set -- "${PWD}/$1" "${@:2}"

# Hygiene, not a boundary: children do not inherit the runner command files,
# but one could still find them through /proc. The verdict holds because the
# job reads it only on a step that succeeded, and the publisher checks its base
# and tree against the candidate.
unset GITHUB_OUTPUT GITHUB_ENV GITHUB_PATH GITHUB_STATE GITHUB_STEP_SUMMARY

ROOT_DIR="${workcell_entry_root}"
# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/go-run-env.sh"

if [[ "${toolchain}" == 0 ]]; then
  exec_go_run_in_repo "${ROOT_DIR}" ./cmd/workcell-citools upstream-refresh-scope-guard "$1"
fi

# Only a well-formed verdict reaches the step output, and only after the
# guard passed, so a failed guard leaves no verdict for the publish job.
verdict="$(run_go_in_repo "${ROOT_DIR}" run ./cmd/workcell-citools upstream-refresh-toolchain-scope-guard "$1")" || exit 1
if [[ ! "${verdict}" =~ ^passed:[0-9a-f]{40}:[0-9a-f]{40}$ ]]; then
  echo "upstream-refresh-scope-guard: the toolchain guard printed no verdict" >&2
  exit 1
fi
printf 'verdict=%s\n' "${verdict}" >>"$2"
