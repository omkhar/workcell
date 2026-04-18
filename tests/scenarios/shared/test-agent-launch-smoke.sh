#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-agent-launch-scenario.XXXXXX")"
# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"

REAL_HOME="$(resolve_workcell_real_home)"
PROFILE="wcl-als-$$"
AUTH_ROOT="${TMP_DIR}/auth"
WORKSPACE="${TMP_DIR}/workspace"

cleanup() {
  local status=$?
  local file=""

  if [[ "${status}" -ne 0 ]]; then
    for file in \
      prepare.stdout prepare.stderr \
      codex.stdout codex.stderr codex.combined \
      claude.stdout claude.stderr claude.combined \
      gemini.stdout gemini.stderr gemini.combined; do
      [[ -f "${TMP_DIR}/${file}" ]] || continue
      printf -- '--- %s ---\n' "${file}" >&2
      cat "${TMP_DIR}/${file}" >&2
    done
  fi
  if command -v colima >/dev/null 2>&1; then
    colima stop --profile "${PROFILE}" >/dev/null 2>&1 || true
    colima delete --profile "${PROFILE}" --force >/dev/null 2>&1 || true
  fi
  rm -rf "${REAL_HOME}/.colima/${PROFILE}"
  rm -rf "${REAL_HOME}/.colima/_lima/colima-${PROFILE}"
  rm -rf "${REAL_HOME}/.colima/_lima/_disks/colima-${PROFILE}"
  rm -rf "${TMP_DIR}"
  exit "${status}"
}
trap cleanup EXIT

mkdir -p "${AUTH_ROOT}" "${WORKSPACE}"
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
cat >"${AUTH_ROOT}/policy.toml" <<'EOF'
version = 1

[credentials]
codex_auth = "auth.json"
claude_auth = "claude-auth.json"
claude_api_key = "claude-api-key.txt"
gemini_env = "gemini.env"
EOF

prepare_runtime() {
  "${ROOT_DIR}/scripts/workcell" \
    --prepare-only \
    --agent codex \
    --workspace "${WORKSPACE}" \
    --colima-profile "${PROFILE}" \
    --injection-policy "${AUTH_ROOT}/policy.toml" \
    >"${TMP_DIR}/prepare.stdout" 2>"${TMP_DIR}/prepare.stderr"
}

run_agent_version_smoke() {
  local agent="$1"
  local status=0

  set +e
  perl -e 'alarm shift @ARGV; exec @ARGV' 120 \
    "${ROOT_DIR}/scripts/workcell" \
    --agent "${agent}" \
    --workspace "${WORKSPACE}" \
    --colima-profile "${PROFILE}" \
    --injection-policy "${AUTH_ROOT}/policy.toml" \
    -- "${agent}" --version \
    >"${TMP_DIR}/${agent}.stdout" 2>"${TMP_DIR}/${agent}.stderr"
  status=$?
  set -e
  return "${status}"
}

prepare_runtime
grep -q "^profile=${PROFILE} mode=strict agent=codex " "${TMP_DIR}/prepare.stderr"
grep -q "Prepared runtime image recorded for profile ${PROFILE}. No session launched because --prepare-only was requested." "${TMP_DIR}/prepare.stderr"

for agent in codex claude gemini; do
  run_agent_version_smoke "${agent}"
  grep -q "^profile=${PROFILE} mode=strict agent=${agent} " "${TMP_DIR}/${agent}.stderr"
  grep -q '^execution_path=managed-tier1 audit_log=' "${TMP_DIR}/${agent}.stderr"
  cat "${TMP_DIR}/${agent}.stdout" "${TMP_DIR}/${agent}.stderr" >"${TMP_DIR}/${agent}.combined"
  grep -q '[^[:space:]]' "${TMP_DIR}/${agent}.combined"
done

echo "Provider launch smoke scenario passed"
