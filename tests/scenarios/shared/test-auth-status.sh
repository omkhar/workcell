#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
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

[credentials.github_hosts]
source = "hosts.yml"
providers = ["codex", "claude", "gemini"]

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

run_launch_dry_run() {
  local agent="$1"

  "${ROOT_DIR}/scripts/workcell" \
    --agent "${agent}" \
    --workspace "${WORKSPACE}" \
    --injection-policy "${AUTH_ROOT}/policy.toml" \
    --dry-run \
    >"${TMP_DIR}/launch-${agent}.stdout" 2>"${TMP_DIR}/launch-${agent}.stderr"
}

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

for agent in codex claude gemini; do
  run_launch_dry_run "${agent}"
  grep -q "^profile=.* mode=strict agent=${agent} " "${TMP_DIR}/launch-${agent}.stderr"
  grep -q '^workspace_control_plane=masked$' "${TMP_DIR}/launch-${agent}.stderr"
  grep -q '^session_assurance_initial=managed-mutable$' "${TMP_DIR}/launch-${agent}.stderr"
  grep -q '^execution_path=managed-tier1 audit_log=' "${TMP_DIR}/launch-${agent}.stderr"
done

grep -Eq '^injection_policy_sha256=sha256:[0-9a-f]+ credential_keys=(codex_auth,github_hosts|github_hosts,codex_auth) ssh_injected=1$' "${TMP_DIR}/launch-codex.stderr"
grep -Eq '^injection_policy_sha256=sha256:[0-9a-f]+ credential_keys=claude_api_key,claude_auth,github_hosts ssh_injected=1$' "${TMP_DIR}/launch-claude.stderr"
grep -Eq '^injection_policy_sha256=sha256:[0-9a-f]+ credential_keys=(gemini_env,github_hosts|github_hosts,gemini_env) ssh_injected=1$' "${TMP_DIR}/launch-gemini.stderr"

echo "Auth-status and launch scenario passed"
