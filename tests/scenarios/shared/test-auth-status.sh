#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=tests/scenarios/shared/test-auth-status.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-auth-status-scenario.XXXXXX")"

cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

WORKSPACE="${TMP_DIR}/workspace"
AUTH_ROOT="${TMP_DIR}/auth-status"
mkdir -p "${WORKSPACE}" "${AUTH_ROOT}"
git -C "${WORKSPACE}" init -q
printf 'scenario workspace\n' >"${WORKSPACE}/README.md"

printf '{}\n' >"${AUTH_ROOT}/auth.json"
chmod 0600 "${AUTH_ROOT}/auth.json"
printf '{"token":"claude-auth"}\n' >"${AUTH_ROOT}/claude-auth.json"
chmod 0600 "${AUTH_ROOT}/claude-auth.json"
printf 'claude-key\n' >"${AUTH_ROOT}/claude-api-key.txt"
chmod 0600 "${AUTH_ROOT}/claude-api-key.txt"
printf 'copilot-token\n' >"${AUTH_ROOT}/copilot-github-token.txt"
chmod 0600 "${AUTH_ROOT}/copilot-github-token.txt"
cat >"${AUTH_ROOT}/gemini.env" <<'EOF'
GOOGLE_GENAI_USE_VERTEXAI=true
GOOGLE_API_KEY=verify-google-key
EOF
chmod 0600 "${AUTH_ROOT}/gemini.env"
cat >"${AUTH_ROOT}/hosts.yml" <<'EOF'
github.com:
  oauth_token: test-token
EOF
chmod 0600 "${AUTH_ROOT}/hosts.yml"
cat >"${AUTH_ROOT}/ssh-config" <<'EOF'
ProxyCommand nc %h %p
EOF
chmod 0600 "${AUTH_ROOT}/ssh-config"
cat >"${AUTH_ROOT}/policy.toml" <<'EOF'
version = 1

[credentials]
codex_auth = "auth.json"
claude_auth = "claude-auth.json"
claude_api_key = "claude-api-key.txt"
gemini_env = "gemini.env"

[credentials.copilot_github_token]
source = "copilot-github-token.txt"

[credentials.github_hosts]
source = "hosts.yml"
providers = ["codex", "claude", "copilot", "gemini"]

[ssh]
enabled = true
config = "ssh-config"
allow_unsafe_config = true
EOF

run_auth_status() {
  local agent="$1"

  "${ROOT_DIR}/scripts/workcell" \
    --agent "${agent}" \
    --workspace "${WORKSPACE}" \
    --injection-policy "${AUTH_ROOT}/policy.toml" \
    --auth-status
}

# The policy stages API keys from source, which the egress proxy refuses, so
# these launch rows keep the allowlist session that they prove.
run_launch_dry_run() {
  local agent="$1"

  "${ROOT_DIR}/scripts/workcell" \
    --agent "${agent}" \
    --workspace "${WORKSPACE}" \
    --injection-policy "${AUTH_ROOT}/policy.toml" \
    --egress-proxy=off \
    --dry-run \
    >"${TMP_DIR}/launch-${agent}.stdout" 2>"${TMP_DIR}/launch-${agent}.stderr"
}

support_doctor_output="$("${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --workspace "${WORKSPACE}" \
  --no-default-injection-policy \
  --doctor)"
launch_blocked=0
if grep -q '^support_matrix_launch=blocked$' <<<"${support_doctor_output}"; then
  launch_blocked=1
fi

codex_output="$(run_auth_status codex)"
grep -Eq '^credential_keys=(codex_auth,github_hosts|github_hosts,codex_auth)$' <<<"${codex_output}"
grep -q '^provider_auth_ready_states=codex_auth:ready$' <<<"${codex_output}"
grep -q '^shared_auth_ready_states=github_hosts:ready$' <<<"${codex_output}"
grep -q '^provider_auth_mode=codex_auth$' <<<"${codex_output}"
grep -q '^provider_auth_modes=codex_auth$' <<<"${codex_output}"
grep -q '^shared_auth_modes=github_hosts$' <<<"${codex_output}"
grep -q '^github_auth_present=1$' <<<"${codex_output}"
grep -q '^ssh_injected=1$' <<<"${codex_output}"
grep -q '^ssh_config_assurance=lower-assurance-unsafe-config$' <<<"${codex_output}"
grep -q '^provider_bootstrap_state=ready$' <<<"${codex_output}"
grep -q '^provider_bootstrap_path=direct-staged$' <<<"${codex_output}"
grep -q '^provider_bootstrap_support=repo-required$' <<<"${codex_output}"

codex_alias_output="$("${ROOT_DIR}/scripts/workcell" \
  auth-status \
  --agent codex \
  --workspace "${WORKSPACE}" \
  --injection-policy "${AUTH_ROOT}/policy.toml")"
grep -q '^provider_auth_mode=codex_auth$' <<<"${codex_alias_output}"
grep -q '^provider_auth_ready_states=codex_auth:ready$' <<<"${codex_alias_output}"
grep -q '^shared_auth_modes=github_hosts$' <<<"${codex_alias_output}"
grep -q '^provider_bootstrap_path=direct-staged$' <<<"${codex_alias_output}"

claude_output="$(run_auth_status claude)"
grep -q '^provider_auth_ready_states=claude_api_key:ready,claude_auth:ready$' <<<"${claude_output}"
grep -q '^shared_auth_ready_states=github_hosts:ready$' <<<"${claude_output}"
grep -q '^provider_auth_mode=claude_api_key$' <<<"${claude_output}"
grep -q '^provider_auth_modes=claude_api_key,claude_auth$' <<<"${claude_output}"
grep -q '^shared_auth_modes=github_hosts$' <<<"${claude_output}"
grep -q '^github_auth_present=1$' <<<"${claude_output}"
grep -q '^provider_bootstrap_state=ready$' <<<"${claude_output}"
grep -q '^provider_bootstrap_path=direct-staged$' <<<"${claude_output}"
grep -q '^provider_bootstrap_support=repo-required$' <<<"${claude_output}"

copilot_output="$(run_auth_status copilot)"
grep -q '^credential_keys=copilot_github_token$' <<<"${copilot_output}"
grep -q '^provider_auth_ready_states=copilot_github_token:ready$' <<<"${copilot_output}"
grep -q '^shared_auth_ready_states=none$' <<<"${copilot_output}"
grep -q '^provider_auth_mode=copilot_github_token$' <<<"${copilot_output}"
grep -q '^provider_auth_modes=copilot_github_token$' <<<"${copilot_output}"
grep -q '^shared_auth_modes=none$' <<<"${copilot_output}"
grep -q '^github_auth_present=0$' <<<"${copilot_output}"
grep -q '^provider_bootstrap_state=ready$' <<<"${copilot_output}"
grep -q '^provider_bootstrap_path=direct-staged$' <<<"${copilot_output}"
grep -q '^provider_bootstrap_support=repo-required$' <<<"${copilot_output}"

gemini_output="$(run_auth_status gemini)"
grep -q '^provider_auth_ready_states=gemini_env:ready$' <<<"${gemini_output}"
grep -q '^shared_auth_ready_states=github_hosts:ready$' <<<"${gemini_output}"
grep -q '^provider_auth_mode=gemini_env$' <<<"${gemini_output}"
grep -q '^provider_auth_modes=gemini_env$' <<<"${gemini_output}"
grep -q '^shared_auth_modes=github_hosts$' <<<"${gemini_output}"
grep -q '^github_auth_present=1$' <<<"${gemini_output}"
grep -q '^provider_bootstrap_state=ready$' <<<"${gemini_output}"
grep -q '^provider_bootstrap_path=direct-staged$' <<<"${gemini_output}"
grep -q '^provider_bootstrap_support=repo-required$' <<<"${gemini_output}"

for agent in codex claude copilot gemini; do
  if [[ "${launch_blocked}" -eq 1 ]]; then
    set +e
    run_launch_dry_run "${agent}"
    launch_rc=$?
    set -e
    test "${launch_rc}" -eq 2
    grep -q 'Workcell launch is not supported' "${TMP_DIR}/launch-${agent}.stderr"
    grep -q 'Supported launch hosts today remain Apple Silicon macOS' "${TMP_DIR}/launch-${agent}.stderr"
  else
    run_launch_dry_run "${agent}"
    grep -q "^profile=.* mode=strict agent=${agent} " "${TMP_DIR}/launch-${agent}.stderr"
    grep -q '^workspace_control_plane=masked$' "${TMP_DIR}/launch-${agent}.stderr"
    grep -q '^session_assurance_initial=managed-mutable$' "${TMP_DIR}/launch-${agent}.stderr"
    grep -q '^execution_path=managed-tier1 audit_log=' "${TMP_DIR}/launch-${agent}.stderr"
  fi
done

if [[ "${launch_blocked}" -eq 0 ]]; then
  grep -Eq '^injection_policy_sha256=sha256:[0-9a-f]+ credential_keys=(codex_auth,github_hosts|github_hosts,codex_auth) ssh_injected=1$' "${TMP_DIR}/launch-codex.stderr"
  grep -Eq '^injection_policy_sha256=sha256:[0-9a-f]+ credential_keys=claude_api_key,claude_auth,github_hosts ssh_injected=1$' "${TMP_DIR}/launch-claude.stderr"
  grep -Eq '^injection_policy_sha256=sha256:[0-9a-f]+ credential_keys=copilot_github_token ssh_injected=1$' "${TMP_DIR}/launch-copilot.stderr"
  grep -Eq '^injection_policy_sha256=sha256:[0-9a-f]+ credential_keys=(gemini_env,github_hosts|github_hosts,gemini_env) ssh_injected=1$' "${TMP_DIR}/launch-gemini.stderr"
fi

# Without --egress-proxy=off, strict Colima uses the proxy, which refuses the
# Claude API key from source and names both ways out.
if [[ "${launch_blocked}" -eq 0 ]]; then
  set +e
  "${ROOT_DIR}/scripts/workcell" --dry-run --agent claude --workspace "${WORKSPACE}" \
    --injection-policy "${AUTH_ROOT}/policy.toml" \
    >"${TMP_DIR}/launch-claude-proxy.stdout" 2>"${TMP_DIR}/launch-claude-proxy.stderr"
  proxy_rc=$?
  set -e
  test "${proxy_rc}" -eq 1
  grep -q 'the egress proxy brokers credentials.claude_api_key: .* or launch with --egress-proxy=off$' "${TMP_DIR}/launch-claude-proxy.stderr"
fi

echo "Auth-status and launch scenario passed"
