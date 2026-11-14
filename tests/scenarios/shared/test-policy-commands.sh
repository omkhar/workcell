#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=tests/scenarios/shared/test-policy-commands.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-policy-commands-scenario.XXXXXX")"

cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

AUTH_JSON="${TMP_DIR}/auth.json"
COPILOT_TOKEN="${TMP_DIR}/copilot-github-token.txt"
HOSTS_YML="${TMP_DIR}/hosts.yml"
SHARED_POLICY="${TMP_DIR}/shared.toml"
POLICY_PATH="${TMP_DIR}/policy.toml"
OUT_OF_SCOPE_POLICY_PATH="${TMP_DIR}/out-of-scope-policy.toml"
UNSUPPORTED_COPILOT_DOC_POLICY_PATH="${TMP_DIR}/unsupported-copilot-doc-policy.toml"

printf '{"token":"super-secret"}\n' >"${AUTH_JSON}"
chmod 0600 "${AUTH_JSON}"
printf 'copilot-token\n' >"${COPILOT_TOKEN}"
chmod 0600 "${COPILOT_TOKEN}"
cat >"${HOSTS_YML}" <<'EOF'
github.com:
  oauth_token: ghp-example
EOF
chmod 0600 "${HOSTS_YML}"

cat >"${SHARED_POLICY}" <<EOF
[credentials.github_hosts]
source = "${HOSTS_YML}"
providers = ["codex"]
EOF
chmod 0600 "${SHARED_POLICY}"

cat >"${POLICY_PATH}" <<EOF
version = 1
includes = ["${SHARED_POLICY}"]

[credentials.codex_auth]
source = "${AUTH_JSON}"
modes = ["strict"]

[credentials.copilot_github_token]
source = "${COPILOT_TOKEN}"
providers = ["copilot"]
EOF
chmod 0600 "${POLICY_PATH}"

cat >"${OUT_OF_SCOPE_POLICY_PATH}" <<EOF
version = 1

[credentials.claude_api_key]
source = "/no/such/file"
EOF
chmod 0600 "${OUT_OF_SCOPE_POLICY_PATH}"

cat >"${UNSUPPORTED_COPILOT_DOC_POLICY_PATH}" <<'EOF'
version = 1

[documents]
copilot = "copilot.md"
EOF
chmod 0600 "${UNSUPPORTED_COPILOT_DOC_POLICY_PATH}"

show_output="$("${ROOT_DIR}/scripts/workcell" policy show --injection-policy "${POLICY_PATH}")"
grep -q '^version = 1$' <<<"${show_output}"
grep -q '^\[credentials.codex_auth\]$' <<<"${show_output}"
grep -q '^\[credentials.github_hosts\]$' <<<"${show_output}"

validate_output="$("${ROOT_DIR}/scripts/workcell" policy validate --injection-policy "${POLICY_PATH}")"
grep -q '^policy_valid=1$' <<<"${validate_output}"

diff_output="$("${ROOT_DIR}/scripts/workcell" policy diff --injection-policy "${POLICY_PATH}")"
grep -q '^diff_status=changed$' <<<"${diff_output}"
grep -q '^--- current$' <<<"${diff_output}"
grep -q '^+++ canonical$' <<<"${diff_output}"
grep -q '^\+\[credentials.github_hosts\]$' <<<"${diff_output}"

why_selected="$("${ROOT_DIR}/scripts/workcell" why \
  --credential codex_auth \
  --agent codex \
  --mode strict \
  --injection-policy "${POLICY_PATH}")"
grep -q '^selected=1$' <<<"${why_selected}"
grep -q '^credential_readiness=ready$' <<<"${why_selected}"
grep -q '^credential_input_kind=source$' <<<"${why_selected}"
grep -q '^selection_reason=providers not restricted; mode matches modes$' <<<"${why_selected}"
grep -q '^bootstrap_path=direct-staged$' <<<"${why_selected}"
grep -q '^bootstrap_support=repo-required$' <<<"${why_selected}"
if grep -q 'super-secret' <<<"${why_selected}"; then
  echo "workcell why leaked credential material" >&2
  exit 1
fi

why_filtered="$("${ROOT_DIR}/scripts/workcell" why \
  --credential github_hosts \
  --agent claude \
  --mode strict \
  --injection-policy "${POLICY_PATH}")"
grep -q '^selected=0$' <<<"${why_filtered}"
grep -q '^credential_readiness=filtered-provider$' <<<"${why_filtered}"
grep -q '^credential_providers=codex$' <<<"${why_filtered}"
grep -q '^selection_reason=agent does not match providers; modes not restricted$' <<<"${why_filtered}"
grep -q '^bootstrap_path=direct-staged$' <<<"${why_filtered}"

why_copilot="$("${ROOT_DIR}/scripts/workcell" why \
  --credential copilot_github_token \
  --agent copilot \
  --mode strict \
  --injection-policy "${POLICY_PATH}")"
grep -q '^selected=1$' <<<"${why_copilot}"
grep -q '^credential_readiness=ready$' <<<"${why_copilot}"
grep -q '^credential_providers=copilot$' <<<"${why_copilot}"
grep -q '^selection_reason=agent matches providers; modes not restricted$' <<<"${why_copilot}"
grep -q '^bootstrap_path=direct-staged$' <<<"${why_copilot}"
grep -q '^bootstrap_next_step=none$' <<<"${why_copilot}"
if grep -q 'copilot-token' <<<"${why_copilot}"; then
  echo "workcell why leaked Copilot credential material" >&2
  exit 1
fi

why_out_of_scope="$("${ROOT_DIR}/scripts/workcell" why \
  --credential claude_api_key \
  --agent codex \
  --mode strict \
  --injection-policy "${OUT_OF_SCOPE_POLICY_PATH}")"
grep -q '^selected=0$' <<<"${why_out_of_scope}"
grep -q '^credential_readiness=out-of-scope$' <<<"${why_out_of_scope}"
grep -q '^selection_reason=credential is not in scope for agent codex$' <<<"${why_out_of_scope}"
grep -q '^bootstrap_path=direct-staged$' <<<"${why_out_of_scope}"

set +e
missing_output="$("${ROOT_DIR}/scripts/workcell" policy validate --injection-policy "${TMP_DIR}/missing.toml" 2>&1)"
missing_rc=$?
set -e
test "${missing_rc}" -eq 1
grep -Eq 'missing\.toml|no such file or directory|file does not exist' <<<"${missing_output}"

set +e
unsupported_doc_output="$("${ROOT_DIR}/scripts/workcell" policy validate --injection-policy "${UNSUPPORTED_COPILOT_DOC_POLICY_PATH}" 2>&1)"
unsupported_doc_rc=$?
set -e
test "${unsupported_doc_rc}" -eq 1
grep -q 'documents contains unsupported keys: copilot' <<<"${unsupported_doc_output}"

echo "Policy command scenario passed"
