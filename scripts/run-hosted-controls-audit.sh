#!/bin/bash -p
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/run-hosted-controls-audit.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

HOSTED_TOKEN="${WORKCELL_HOSTED_CONTROLS_TOKEN:-}"
LOCAL_TOKEN="${GH_TOKEN:-${GITHUB_TOKEN:-}}"
export -n HOSTED_TOKEN LOCAL_TOKEN 2>/dev/null || true
unset WORKCELL_HOSTED_CONTROLS_TOKEN WORKCELL_GITHUB_API_TOKEN WORKCELL_GITHUB_API_TOKEN_FILE
unset GH_TOKEN GITHUB_TOKEN GH_ENTERPRISE_TOKEN GITHUB_ENTERPRISE_TOKEN
if [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then
  WORKFLOW_TOKEN="${HOSTED_TOKEN}"
else
  WORKFLOW_TOKEN="${LOCAL_TOKEN}"
fi
export -n WORKFLOW_TOKEN 2>/dev/null || true

readonly TRUSTED_PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
export PATH="${TRUSTED_PATH}"
ROOT_DIR="${workcell_entry_root}"
REPO="${1:-}"
REQUIRED_MODE="${WORKCELL_HOSTED_CONTROLS_REQUIRED:-0}"

if [[ $# -gt 1 ]]; then
  echo "run-hosted-controls-audit.sh accepts only one optional OWNER/REPO argument." >&2
  exit 2
fi
if [[ "${REQUIRED_MODE}" != "0" && "${REQUIRED_MODE}" != "1" ]]; then
  echo "WORKCELL_HOSTED_CONTROLS_REQUIRED must be 0 or 1." >&2
  exit 1
fi

if [[ "${GITHUB_ACTIONS:-}" == "true" && -z "${HOSTED_TOKEN}" ]]; then
  if [[ "${REQUIRED_MODE}" == "1" ]]; then
    echo "Hosted-controls verification requires WORKCELL_HOSTED_CONTROLS_TOKEN in GitHub Actions." >&2
    echo "Configure a fine-grained token or GitHub App token that can read repository administration metadata for this repository." >&2
    exit 1
  fi
  echo "Skipping hosted-controls verification in GitHub Actions because WORKCELL_HOSTED_CONTROLS_TOKEN is not configured." >&2
  echo "github.token cannot read the rulesets/collaborators/environment metadata this audit requires on this repository." >&2
  exit 0
fi

if [[ -n "${WORKFLOW_TOKEN}" ]]; then
  if printf '%s\n' "${WORKFLOW_TOKEN}" |
    "${ROOT_DIR}/scripts/verify-github-hosted-controls.sh" --token-stdin "${REPO}"; then
    exit 0
  else
    relay_status=("${PIPESTATUS[@]}")
    exit "${relay_status[1]}"
  fi
fi
"${ROOT_DIR}/scripts/verify-github-hosted-controls.sh" "${REPO}"
