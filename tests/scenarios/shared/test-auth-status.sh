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

codex_output="$(run_auth_status codex)"
grep -Eq '^credential_keys=(codex_auth,github_hosts|github_hosts,codex_auth)$' <<<"${codex_output}"
grep -q '^provider_auth_mode=codex_auth$' <<<"${codex_output}"
grep -q '^provider_auth_modes=codex_auth$' <<<"${codex_output}"
grep -q '^shared_auth_modes=github_hosts$' <<<"${codex_output}"
grep -q '^github_auth_present=1$' <<<"${codex_output}"
grep -q '^ssh_injected=1$' <<<"${codex_output}"
grep -q '^ssh_config_assurance=lower-assurance-unsafe-config$' <<<"${codex_output}"

codex_alias_output="$("${ROOT_DIR}/scripts/workcell" \
  auth-status \
  --agent codex \
  --workspace "${WORKSPACE}" \
  --injection-policy "${AUTH_ROOT}/policy.toml")"
grep -q '^provider_auth_mode=codex_auth$' <<<"${codex_alias_output}"
grep -q '^shared_auth_modes=github_hosts$' <<<"${codex_alias_output}"

claude_output="$(run_auth_status claude)"
grep -q '^provider_auth_mode=claude_api_key$' <<<"${claude_output}"
grep -q '^provider_auth_modes=claude_api_key,claude_auth$' <<<"${claude_output}"
grep -q '^shared_auth_modes=github_hosts$' <<<"${claude_output}"
grep -q '^github_auth_present=1$' <<<"${claude_output}"

gemini_output="$(run_auth_status gemini)"
grep -q '^provider_auth_mode=gemini_env$' <<<"${gemini_output}"
grep -q '^provider_auth_modes=gemini_env$' <<<"${gemini_output}"
grep -q '^shared_auth_modes=github_hosts$' <<<"${gemini_output}"
grep -q '^github_auth_present=1$' <<<"${gemini_output}"

echo "Auth-status scenario passed"
