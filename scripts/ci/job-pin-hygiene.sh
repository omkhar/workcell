#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/ci/job-pin-hygiene.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"
unset GITHUB_TOKEN GH_TOKEN WORKCELL_GITHUB_API_TOKEN

echo "[ci/pin-hygiene] pinned input policy"
"${ROOT_DIR}/scripts/check-pinned-inputs.sh"

echo "[ci/pin-hygiene] GitHub macOS release test runners"
"${ROOT_DIR}/scripts/verify-github-macos-release-test-runners.sh" macos-26 macos-15

echo "[ci/pin-hygiene] upstream Codex release"
"${ROOT_DIR}/scripts/verify-upstream-codex-release.sh"

echo "[ci/pin-hygiene] upstream Claude release"
"${ROOT_DIR}/scripts/verify-upstream-claude-release.sh"

echo "[ci/pin-hygiene] upstream Copilot release"
WORKCELL_COPILOT_RELEASE_HELP_MODE=checksum \
  "${ROOT_DIR}/scripts/verify-upstream-copilot-release.sh"

echo "[ci/pin-hygiene] upstream Gemini release"
"${ROOT_DIR}/scripts/verify-upstream-gemini-release.sh"

echo "[ci/pin-hygiene] upstream OpenCode release"
"${ROOT_DIR}/scripts/verify-upstream-opencode-release.sh"

echo "[ci/pin-hygiene] upstream Amp release"
"${ROOT_DIR}/scripts/verify-upstream-amp-release.sh"

echo "[ci/pin-hygiene] upstream goose release"
"${ROOT_DIR}/scripts/verify-upstream-goose-release.sh"

# The report quotes upstream release names, so the summary escapes it as HTML
# inside <pre>: no backtick fence or tag in a name can end the block.
append_pin_hygiene_summary() {
  local report="$1"
  local summary="$2"
  {
    printf '%s\n\n%s\n' '## Pin hygiene' '<pre>'
    sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g' "${report}"
    printf '%s\n' '</pre>'
  } >>"${summary}"
}

# The daily upstream refresh opens its PRs shortly before this lane runs, so
# pending drift alone is normal. --hygiene fails only on drift that outlived
# its grace with no fresh refresh PR to carry it, and on a release held that
# long on an unclassified CLI surface. The job summary shows the expected
# drift too.
echo "[ci/pin-hygiene] pinned upstream refresh status"
hygiene_report="$(mktemp "${TMPDIR:-/tmp}/workcell-pin-hygiene.XXXXXX")"
hygiene_status=0
WORKCELL_PIN_HYGIENE_REPOSITORY="${WORKCELL_PIN_HYGIENE_REPOSITORY:-${GITHUB_REPOSITORY:-omkhar/workcell}}" \
  "${ROOT_DIR}/scripts/update-upstream-pins.sh" --hygiene | tee "${hygiene_report}" || hygiene_status=$?
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  append_pin_hygiene_summary "${hygiene_report}" "${GITHUB_STEP_SUMMARY}"
fi
rm -f "${hygiene_report}"
if [[ "${hygiene_status}" -ne 0 ]]; then
  echo "Pin hygiene found pinned upstream drift that the refresh pipeline did not handle. See the PROBLEM lines above." >&2
  exit "${hygiene_status}"
fi

echo "Workcell shared pin-hygiene job passed."
