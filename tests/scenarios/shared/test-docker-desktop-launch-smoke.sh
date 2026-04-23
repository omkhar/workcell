#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="${TMPDIR:-/tmp}/workcell-docker-desktop-launch-scenario"

REAL_HOME="$(cd "${HOME}" && pwd -P)"
AUTH_ROOT="${TMP_DIR}/auth"
WORKSPACE="${TMP_DIR}/workspace"
TARGET_STATE_DIR=""
PROFILE_NAME=""

rm -rf "${TMP_DIR}"
mkdir -p "${TMP_DIR}"

cleanup() {
  local status=$?
  local file=""
  local builder_name=""
  local builder_container_prefix=""
  local builder_resource=""
  local -a builder_containers=()
  local -a builder_volumes=()

  if [[ "${status}" -ne 0 ]]; then
    for file in \
      inspect.stdout inspect.stderr \
      doctor.stdout doctor.stderr \
      prepare.stdout prepare.stderr \
      codex.stdout codex.stderr codex.combined \
      claude.stdout claude.stderr claude.combined \
      gemini.stdout gemini.stderr gemini.combined; do
      [[ -f "${TMP_DIR}/${file}" ]] || continue
      printf -- '--- %s ---\n' "${file}" >&2
      cat "${TMP_DIR}/${file}" >&2
    done
  fi
  if [[ -n "${PROFILE_NAME}" ]]; then
    builder_name="workcell-runtime-${PROFILE_NAME//[^[:alnum:]_.-]/-}"
    builder_container_prefix="buildx_buildkit_${builder_name}"
    builder_containers=("${builder_container_prefix}" "${builder_container_prefix}0")
    builder_volumes=("${builder_container_prefix}_state" "${builder_container_prefix}0_state")
    for ((cleanup_attempt = 1; cleanup_attempt <= 5; cleanup_attempt++)); do
      docker --context desktop-linux buildx rm --force "${builder_name}" >/dev/null 2>&1 || true
      for builder_resource in "${builder_containers[@]}"; do
        docker --context desktop-linux rm -f "${builder_resource}" >/dev/null 2>&1 || true
      done
      for builder_resource in "${builder_volumes[@]}"; do
        docker --context desktop-linux volume rm "${builder_resource}" >/dev/null 2>&1 || true
      done
      if ! docker --context desktop-linux ps -a --format '{{.Names}}' | grep -Fxq "${builder_container_prefix}" &&
        ! docker --context desktop-linux ps -a --format '{{.Names}}' | grep -Fxq "${builder_container_prefix}0" &&
        ! docker --context desktop-linux volume ls --format '{{.Name}}' | grep -Fxq "${builder_container_prefix}_state" &&
        ! docker --context desktop-linux volume ls --format '{{.Name}}' | grep -Fxq "${builder_container_prefix}0_state"; then
        break
      fi
      sleep 1
    done
  fi
  if [[ -n "${TARGET_STATE_DIR}" ]] && [[ "${TARGET_STATE_DIR}" == "${REAL_HOME}/.local/state/workcell/targets/local_compat/docker-desktop/"* ]]; then
    chmod -R u+w "${TARGET_STATE_DIR}" 2>/dev/null || true
    rm -rf "${TARGET_STATE_DIR}"
  fi
  chmod -R u+w "${TMP_DIR}" 2>/dev/null || true
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

inspect_target_state() {
  "${ROOT_DIR}/scripts/workcell" \
    --target docker-desktop \
    --agent codex \
    --inspect \
    --workspace "${WORKSPACE}" \
    >"${TMP_DIR}/inspect.stdout" 2>"${TMP_DIR}/inspect.stderr"
}

doctor_target() {
  "${ROOT_DIR}/scripts/workcell" \
    --target docker-desktop \
    --agent codex \
    --doctor \
    --workspace "${WORKSPACE}" \
    >"${TMP_DIR}/doctor.stdout" 2>"${TMP_DIR}/doctor.stderr"
}

prepare_runtime() {
  "${ROOT_DIR}/scripts/workcell" \
    --target docker-desktop \
    --prepare-only \
    --agent codex \
    --workspace "${WORKSPACE}" \
    --injection-policy "${AUTH_ROOT}/policy.toml" \
    >"${TMP_DIR}/prepare.stdout" 2>"${TMP_DIR}/prepare.stderr"
}

expected_runtime_version() {
  local agent="$1"

  case "${agent}" in
    codex)
      sed -n 's/^ARG CODEX_VERSION=//p' "${ROOT_DIR}/runtime/container/Dockerfile"
      ;;
    claude)
      sed -n 's/^ARG CLAUDE_VERSION=//p' "${ROOT_DIR}/runtime/container/Dockerfile"
      ;;
    gemini)
      jq -r '.dependencies["@google/gemini-cli"]' "${ROOT_DIR}/runtime/container/providers/package.json"
      ;;
    *)
      return 1
      ;;
  esac
}

run_agent_version_smoke() {
  local agent="$1"
  local status=0

  set +e
  perl -e 'alarm shift @ARGV; exec @ARGV' 120 \
    "${ROOT_DIR}/scripts/workcell" \
    --target docker-desktop \
    --agent "${agent}" \
    --workspace "${WORKSPACE}" \
    --injection-policy "${AUTH_ROOT}/policy.toml" \
    -- "${agent}" --version \
    >"${TMP_DIR}/${agent}.stdout" 2>"${TMP_DIR}/${agent}.stderr"
  status=$?
  set -e
  return "${status}"
}

inspect_target_state
PROFILE_NAME="$(sed -n 's/^profile=//p' "${TMP_DIR}/inspect.stdout")"
[[ -n "${PROFILE_NAME}" ]]
TARGET_STATE_DIR="$(sed -n 's/^target_state_dir=//p' "${TMP_DIR}/inspect.stdout")"
[[ -n "${TARGET_STATE_DIR}" ]]
[[ "${TARGET_STATE_DIR}" == "${REAL_HOME}/.local/state/workcell/targets/local_compat/docker-desktop/"* ]]
grep -q '^target_kind=local_compat$' "${TMP_DIR}/inspect.stdout"
grep -q '^target_provider=docker-desktop$' "${TMP_DIR}/inspect.stdout"
grep -q '^target_id=desktop-linux$' "${TMP_DIR}/inspect.stdout"
grep -q '^target_assurance_class=compat$' "${TMP_DIR}/inspect.stdout"
grep -q '^support_matrix_status=supported$' "${TMP_DIR}/inspect.stdout"
grep -q '^support_matrix_launch=allowed$' "${TMP_DIR}/inspect.stdout"
grep -q '^support_matrix_evidence=certification-only$' "${TMP_DIR}/inspect.stdout"

doctor_target
DOCTOR_RECOMMENDED_NEXT="$(sed -n 's/^doctor_recommended_next=//p' "${TMP_DIR}/doctor.stdout")"
grep -q '^doctor_missing_host_tools=none$' "${TMP_DIR}/doctor.stdout"
[[ "${DOCTOR_RECOMMENDED_NEXT}" == *"workcell"* ]]
[[ "${DOCTOR_RECOMMENDED_NEXT}" == *"--prepare"* ]]
[[ "${DOCTOR_RECOMMENDED_NEXT}" == *"--agent codex"* ]]
[[ "${DOCTOR_RECOMMENDED_NEXT}" == *"--target docker-desktop"* ]]
[[ "${DOCTOR_RECOMMENDED_NEXT}" == *"--workspace "* ]]

prepare_runtime
grep -q '^target_kind=local_compat target_provider=docker-desktop target_id=desktop-linux target_assurance_class=compat runtime_api=docker workspace_transport=workspace-mount$' "${TMP_DIR}/prepare.stderr"
grep -q 'Prepared runtime image recorded for profile ' "${TMP_DIR}/prepare.stderr"
[[ -f "${TARGET_STATE_DIR}/workcell.image-ready" ]]
[[ -f "${TARGET_STATE_DIR}/workcell.managed" ]]

for agent in codex claude gemini; do
  expected_version="$(expected_runtime_version "${agent}")"
  run_agent_version_smoke "${agent}"
  grep -q '^target_kind=local_compat target_provider=docker-desktop target_id=desktop-linux target_assurance_class=compat runtime_api=docker workspace_transport=workspace-mount$' "${TMP_DIR}/${agent}.stderr"
  grep -q '^execution_path=managed-tier1 audit_log=' "${TMP_DIR}/${agent}.stderr"
  cat "${TMP_DIR}/${agent}.stdout" "${TMP_DIR}/${agent}.stderr" >"${TMP_DIR}/${agent}.combined"
  grep -q '[^[:space:]]' "${TMP_DIR}/${agent}.combined"
  grep -Fq "${expected_version}" "${TMP_DIR}/${agent}.combined"
done

echo "Docker Desktop provider launch smoke scenario passed"
