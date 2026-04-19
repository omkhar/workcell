#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-codex-resolver-launcher.XXXXXX")"

cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

expect_line() {
  local label="$1"
  local expected="$2"
  local haystack="$3"

  if ! grep -Fxq "${expected}" <<<"${haystack}"; then
    echo "Missing ${label}: ${expected}" >&2
    printf '%s\n' "${haystack}" >&2
    exit 1
  fi
}

expect_file_contains() {
  local label="$1"
  local path="$2"
  local needle="$3"

  if [[ ! -f "${path}" ]]; then
    echo "Missing ${label} file: ${path}" >&2
    exit 1
  fi
  if ! grep -Fq "${needle}" "${path}"; then
    echo "Unexpected ${label} contents in ${path}" >&2
    cat "${path}" >&2
    exit 1
  fi
}

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
expect_line "status resolver" "credential_resolvers=codex_auth:codex-home-auth-file" "${status_output}"
expect_line "status resolution state" "credential_resolution_states=codex_auth:host-source" "${status_output}"
expect_line "status ready state" "provider_auth_ready_states=codex_auth:ready" "${status_output}"
expect_line "status bootstrap path" "provider_bootstrap_path=host-resolver" "${status_output}"

launcher_status="$(
  WORKCELL_TEST_CODEX_AUTH_FILE="${CODEX_AUTH}" \
    /bin/bash "${ROOT_DIR}/scripts/workcell" \
    --agent codex \
    --auth-status \
    --workspace "${WORKSPACE}" \
    --injection-policy "${POLICY}"
)"
expect_line "launcher resolver" "credential_resolvers=codex_auth:codex-home-auth-file" "${launcher_status}"
expect_line "launcher resolution state" "credential_resolution_states=codex_auth:host-source" "${launcher_status}"
expect_line "launcher ready state" "provider_auth_ready_states=codex_auth:ready" "${launcher_status}"
expect_line "launcher bootstrap path" "provider_bootstrap_path=host-resolver" "${launcher_status}"

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
expect_file_contains "staged codex auth" "${staged_source}" '"token":"codex"'

echo "Codex resolver launcher scenario passed"
