#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=tests/scenarios/shared/test-claude-resolver-launcher.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-claude-resolver-launcher.XXXXXX")"

cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

WORKSPACE="${TMP_DIR}/workspace"
POLICY="${TMP_DIR}/policy.toml"
mkdir -p "${WORKSPACE}"

cat >"${POLICY}" <<'EOF'
version = 1

[credentials.claude_auth]
resolver = "claude-macos-keychain"
materialization = "ephemeral"
EOF

set +e
failure_output="$("${ROOT_DIR}/scripts/workcell" \
  --self-staging-probe \
  claude \
  "${WORKSPACE}" \
  "${POLICY}" \
  strict \
  0 \
  0 2>&1)"
failure_rc=$?
set -e

test "${failure_rc}" -ne 0
grep -q 'claude macOS login reuse is configured' <<<"${failure_output}"

success_output="$("${ROOT_DIR}/scripts/workcell" \
  --self-staging-probe \
  claude \
  "${WORKSPACE}" \
  "${POLICY}" \
  strict \
  0 \
  0 \
  1)"

grep -q '^injection_bundle_root=' <<<"${success_output}"
grep -q '^direct_mount=' <<<"${success_output}"

echo "Claude resolver launcher scenario passed"
