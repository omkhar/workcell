#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-claude-resolver-launcher.XXXXXX")"

cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

WORKSPACE="${TMP_DIR}/workspace"
POLICY="${TMP_DIR}/policy.toml"
EXPORT_FILE="${TMP_DIR}/claude-export.json"
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
grep -q 'Claude macOS login reuse is configured' <<<"${failure_output}"

printf '{"token":"claude"}\n' >"${EXPORT_FILE}"
chmod 0600 "${EXPORT_FILE}"

success_output="$("${ROOT_DIR}/scripts/workcell" \
  --self-staging-probe \
  claude \
  "${WORKSPACE}" \
  "${POLICY}" \
  strict \
  0 \
  0 \
  "${EXPORT_FILE}")"

grep -q '^injection_bundle_root=' <<<"${success_output}"
grep -q '^direct_mount=' <<<"${success_output}"

echo "Claude resolver launcher scenario passed"
