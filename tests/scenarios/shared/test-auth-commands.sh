#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=tests/scenarios/shared/test-auth-commands.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-auth-commands-scenario.XXXXXX")"

cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

POLICY_PATH="${TMP_DIR}/injection-policy.toml"
MANAGED_ROOT="${TMP_DIR}/managed-credentials"
SOURCE_AUTH="${TMP_DIR}/codex-auth.json"
SOURCE_COPILOT_TOKEN="${TMP_DIR}/copilot-github-token.txt"

printf '{}\n' >"${SOURCE_AUTH}"
chmod 0600 "${SOURCE_AUTH}"
printf 'copilot-token\n' >"${SOURCE_COPILOT_TOKEN}"
chmod 0600 "${SOURCE_COPILOT_TOKEN}"

help_output="$("${ROOT_DIR}/scripts/workcell" auth --help)"
grep -q '^Usage: workcell auth init \[options\]$' <<<"${help_output}"

init_output="$(
  cd "${TMP_DIR}"
  "${ROOT_DIR}/scripts/workcell" auth init \
    --injection-policy ./injection-policy.toml \
    --managed-root ./managed-credentials
)"
grep -q '^policy_path=.*injection-policy\.toml$' <<<"${init_output}"

set +e
missing_policy_output="$("${ROOT_DIR}/scripts/workcell" auth status \
  --injection-policy "${TMP_DIR}/missing-policy.toml" \
  --agent codex 2>&1)"
missing_policy_rc=$?
set -e
test "${missing_policy_rc}" -eq 2
grep -q '^Injection policy file does not exist:' <<<"${missing_policy_output}"

set_output="$(
  cd "${TMP_DIR}"
  "${ROOT_DIR}/scripts/workcell" auth set \
    --injection-policy ./injection-policy.toml \
    --managed-root ./managed-credentials \
    --agent codex \
    --credential codex_auth \
    --source ./codex-auth.json
)"
grep -q "^credential=codex_auth$" <<<"${set_output}"
test -f "${MANAGED_ROOT}/codex/auth.json"

status_output="$("${ROOT_DIR}/scripts/workcell" auth status \
  --injection-policy "${POLICY_PATH}" \
  --agent codex)"
grep -q '^credential_keys=codex_auth$' <<<"${status_output}"
grep -q '^credential_input_kinds=codex_auth:source$' <<<"${status_output}"
grep -q '^provider_auth_ready_states=codex_auth:ready$' <<<"${status_output}"
grep -q '^shared_auth_ready_states=none$' <<<"${status_output}"
grep -q '^provider_auth_mode=codex_auth$' <<<"${status_output}"
grep -q '^provider_bootstrap_state=ready$' <<<"${status_output}"
grep -q '^provider_bootstrap_path=direct-staged$' <<<"${status_output}"
grep -q '^provider_bootstrap_support=repo-required$' <<<"${status_output}"

copilot_set_output="$(
  cd "${TMP_DIR}"
  "${ROOT_DIR}/scripts/workcell" auth set \
    --injection-policy ./injection-policy.toml \
    --managed-root ./managed-credentials \
    --agent copilot \
    --credential copilot_github_token \
    --source ./copilot-github-token.txt
)"
grep -q "^credential=copilot_github_token$" <<<"${copilot_set_output}"
test -f "${MANAGED_ROOT}/copilot/github-token.txt"

copilot_status="$("${ROOT_DIR}/scripts/workcell" auth status \
  --injection-policy "${POLICY_PATH}" \
  --agent copilot)"
grep -q '^credential_keys=copilot_github_token$' <<<"${copilot_status}"
grep -q '^credential_input_kinds=copilot_github_token:source$' <<<"${copilot_status}"
grep -q '^provider_auth_ready_states=copilot_github_token:ready$' <<<"${copilot_status}"
grep -q '^shared_auth_ready_states=none$' <<<"${copilot_status}"
grep -q '^provider_auth_mode=copilot_github_token$' <<<"${copilot_status}"
grep -q '^provider_auth_modes=copilot_github_token$' <<<"${copilot_status}"
grep -q '^provider_bootstrap_state=ready$' <<<"${copilot_status}"
grep -q '^provider_bootstrap_path=direct-staged$' <<<"${copilot_status}"
grep -q '^provider_bootstrap_support=repo-required$' <<<"${copilot_status}"

resolver_output="$("${ROOT_DIR}/scripts/workcell" auth set \
  --injection-policy "${POLICY_PATH}" \
  --managed-root "${MANAGED_ROOT}" \
  --agent claude \
  --credential claude_auth \
  --resolver claude-macos-keychain \
  --ack-host-resolver)"
grep -q '^resolver=claude-macos-keychain$' <<<"${resolver_output}"

claude_status="$("${ROOT_DIR}/scripts/workcell" auth status \
  --injection-policy "${POLICY_PATH}" \
  --agent claude)"
grep -q '^credential_resolvers=claude_auth:claude-macos-keychain$' <<<"${claude_status}"
grep -q '^credential_resolution_states=claude_auth:configured-only$' <<<"${claude_status}"
grep -q '^provider_auth_ready_states=claude_auth:configured-only$' <<<"${claude_status}"
grep -q '^shared_auth_ready_states=none$' <<<"${claude_status}"
grep -q '^provider_auth_mode=none$' <<<"${claude_status}"
grep -q '^provider_auth_modes=none$' <<<"${claude_status}"
grep -q '^provider_bootstrap_state=configured-only$' <<<"${claude_status}"
grep -q '^provider_bootstrap_path=host-export-scaffold$' <<<"${claude_status}"
grep -q '^provider_bootstrap_support=manual$' <<<"${claude_status}"

claude_launcher_status="$("${ROOT_DIR}/scripts/workcell" \
  --agent claude \
  --auth-status \
  --workspace "${TMP_DIR}" \
  --injection-policy "${POLICY_PATH}")"
grep -q '^credential_resolution_states=claude_auth:configured-only$' <<<"${claude_launcher_status}"
grep -q '^provider_auth_ready_states=claude_auth:configured-only$' <<<"${claude_launcher_status}"
grep -q '^shared_auth_ready_states=none$' <<<"${claude_launcher_status}"
grep -q '^provider_auth_mode=none$' <<<"${claude_launcher_status}"
grep -q '^provider_auth_modes=none$' <<<"${claude_launcher_status}"
grep -q '^provider_bootstrap_state=configured-only$' <<<"${claude_launcher_status}"
grep -q '^provider_bootstrap_path=host-export-scaffold$' <<<"${claude_launcher_status}"
grep -q '^provider_bootstrap_support=manual$' <<<"${claude_launcher_status}"

unset_output="$("${ROOT_DIR}/scripts/workcell" auth unset \
  --injection-policy "${POLICY_PATH}" \
  --managed-root "${MANAGED_ROOT}" \
  --credential codex_auth)"
grep -q '^removed=1$' <<<"${unset_output}"
test ! -f "${MANAGED_ROOT}/codex/auth.json"

copilot_unset_output="$("${ROOT_DIR}/scripts/workcell" auth unset \
  --injection-policy "${POLICY_PATH}" \
  --managed-root "${MANAGED_ROOT}" \
  --credential copilot_github_token)"
grep -q '^removed=1$' <<<"${copilot_unset_output}"
test ! -f "${MANAGED_ROOT}/copilot/github-token.txt"

echo "Auth command scenario passed"
