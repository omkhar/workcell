#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-codex-resolver-launcher.XXXXXX")"

cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

WORKSPACE="${TMP_DIR}/workspace"
POLICY="${TMP_DIR}/policy.toml"
CODEX_AUTH="${TMP_DIR}/codex-auth.json"
mkdir -p "${WORKSPACE}"
printf '{"token":"codex"}\n' >"${CODEX_AUTH}"
chmod 0600 "${CODEX_AUTH}"

cat >"${POLICY}" <<'EOF'
version = 1

[credentials.codex_auth]
resolver = "codex-home-auth-file"
materialization = "ephemeral"
EOF

status_output="$(
  WORKCELL_TEST_CODEX_AUTH_FILE="${CODEX_AUTH}" \
    /bin/bash "${ROOT_DIR}/scripts/workcell" auth status \
    --injection-policy "${POLICY}" \
    --agent codex
)"
grep -q '^credential_resolvers=codex_auth:codex-home-auth-file$' <<<"${status_output}"
grep -q '^credential_resolution_states=codex_auth:host-source$' <<<"${status_output}"
grep -q '^provider_auth_ready_states=codex_auth:ready$' <<<"${status_output}"
grep -q '^provider_bootstrap_path=host-resolver$' <<<"${status_output}"

launcher_status="$(
  WORKCELL_TEST_CODEX_AUTH_FILE="${CODEX_AUTH}" \
    /bin/bash "${ROOT_DIR}/scripts/workcell" \
    --agent codex \
    --auth-status \
    --workspace "${WORKSPACE}" \
    --injection-policy "${POLICY}"
)"
grep -q '^credential_resolvers=codex_auth:codex-home-auth-file$' <<<"${launcher_status}"
grep -q '^credential_resolution_states=codex_auth:host-source$' <<<"${launcher_status}"
grep -q '^provider_auth_ready_states=codex_auth:ready$' <<<"${launcher_status}"
grep -q '^provider_bootstrap_path=host-resolver$' <<<"${launcher_status}"

probe_output="$(
  WORKCELL_TEST_CODEX_AUTH_FILE="${CODEX_AUTH}" \
    /bin/bash "${ROOT_DIR}/scripts/workcell" \
    --self-staging-probe \
    codex \
    "${WORKSPACE}" \
    "${POLICY}" \
    strict \
    0 \
    1
)"

bundle_root="$(sed -n 's/^injection_bundle_root=//p' <<<"${probe_output}")"
direct_mount="$(sed -n 's/^direct_mount=//p' <<<"${probe_output}")"
staged_source="${direct_mount%%:*}"
test -n "${bundle_root}"
test -n "${staged_source}"
cmp -s "${CODEX_AUTH}" "${staged_source}"

echo "Codex resolver launcher scenario passed"
