#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-auth-commands-scenario.XXXXXX")"

cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

POLICY_PATH="${TMP_DIR}/injection-policy.toml"
MANAGED_ROOT="${TMP_DIR}/managed-credentials"
SOURCE_AUTH="${TMP_DIR}/codex-auth.json"

printf '{}\n' >"${SOURCE_AUTH}"
chmod 0600 "${SOURCE_AUTH}"

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

echo "Auth command scenario passed"
