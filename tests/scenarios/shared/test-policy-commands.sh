#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-policy-commands-scenario.XXXXXX")"

cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

AUTH_JSON="${TMP_DIR}/auth.json"
HOSTS_YML="${TMP_DIR}/hosts.yml"
SHARED_POLICY="${TMP_DIR}/shared.toml"
POLICY_PATH="${TMP_DIR}/policy.toml"
OUT_OF_SCOPE_POLICY_PATH="${TMP_DIR}/out-of-scope-policy.toml"

printf '{"token":"super-secret"}\n' >"${AUTH_JSON}"
chmod 0600 "${AUTH_JSON}"
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
EOF
chmod 0600 "${POLICY_PATH}"

cat >"${OUT_OF_SCOPE_POLICY_PATH}" <<EOF
version = 1

[credentials.claude_api_key]
source = "/no/such/file"
EOF
chmod 0600 "${OUT_OF_SCOPE_POLICY_PATH}"

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

echo "Policy command scenario passed"
