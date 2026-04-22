#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-compat-target-scenario.XXXXXX")"
cleanup() {
  chmod -R u+w "${TMP_DIR}" 2>/dev/null || true
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

HOME_DIR="${TMP_DIR}/home"
WORKSPACE="${TMP_DIR}/workspace"
mkdir -p "${HOME_DIR}/.config" "${WORKSPACE}"
git -C "${WORKSPACE}" init -q
printf 'scenario workspace\n' >"${WORKSPACE}/README.md"

resolve_host_docker_optional() {
  local candidate=""
  for candidate in \
    /opt/homebrew/bin/docker \
    /usr/local/bin/docker \
    /Applications/Docker.app/Contents/Resources/bin/docker \
    "$(command -v docker 2>/dev/null || true)"; do
    [[ -n "${candidate}" ]] || continue
    [[ -x "${candidate}" ]] || continue
    printf '%s\n' "${candidate}"
    return 0
  done
  return 1
}

docker_desktop_missing_state() {
  local docker_bin=""
  if ! docker_bin="$(resolve_host_docker_optional)"; then
    printf 'docker\n'
    return 0
  fi
  if "${docker_bin}" context inspect desktop-linux >/dev/null 2>&1 &&
    "${docker_bin}" --context desktop-linux info >/dev/null 2>&1; then
    printf 'none\n'
  else
    printf 'docker-desktop-context\n'
  fi
}

run_with_support_override() {
  local label="$1"
  local host_os="$2"
  local host_arch="$3"
  shift 3
  HOME="${HOME_DIR}" XDG_CONFIG_HOME="${HOME_DIR}/.config" \
    WORKCELL_VERIFY_INVARIANTS_SANITIZED_ENTRYPOINT=1 \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_OS="${host_os}" \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_ARCH="${host_arch}" \
    /bin/bash -p "${ROOT_DIR}/scripts/workcell" \
    "$@" \
    --workspace "${WORKSPACE}" \
    --no-default-injection-policy \
    >"${TMP_DIR}/${label}.stdout" 2>"${TMP_DIR}/${label}.stderr"
}

run_with_support_override_expect_failure() {
  local expected_rc="$1"
  local label="$2"
  local host_os="$3"
  local host_arch="$4"
  shift 4
  set +e
  HOME="${HOME_DIR}" XDG_CONFIG_HOME="${HOME_DIR}/.config" \
    WORKCELL_VERIFY_INVARIANTS_SANITIZED_ENTRYPOINT=1 \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_OS="${host_os}" \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_ARCH="${host_arch}" \
    /bin/bash -p "${ROOT_DIR}/scripts/workcell" \
    "$@" \
    --workspace "${WORKSPACE}" \
    --no-default-injection-policy \
    >"${TMP_DIR}/${label}.stdout" 2>"${TMP_DIR}/${label}.stderr"
  local rc=$?
  set -e
  if [[ "${rc}" -ne "${expected_rc}" ]]; then
    echo "Unexpected exit code for ${label}: ${rc} (expected ${expected_rc})" >&2
    cat "${TMP_DIR}/${label}.stderr" >&2
    exit 1
  fi
}

EXPECTED_ALLOWED_MISSING="$(docker_desktop_missing_state)"

run_with_support_override \
  "compat-doctor-supported" \
  macos \
  arm64 \
  --target docker-desktop \
  --agent codex \
  --doctor
grep -q '^target_kind=local_compat$' "${TMP_DIR}/compat-doctor-supported.stdout"
grep -q '^target_provider=docker-desktop$' "${TMP_DIR}/compat-doctor-supported.stdout"
grep -q '^target_id=desktop-linux$' "${TMP_DIR}/compat-doctor-supported.stdout"
grep -q '^target_assurance_class=compat$' "${TMP_DIR}/compat-doctor-supported.stdout"
grep -q '^support_matrix_status=supported$' "${TMP_DIR}/compat-doctor-supported.stdout"
grep -q '^support_matrix_launch=allowed$' "${TMP_DIR}/compat-doctor-supported.stdout"
grep -q '^support_matrix_reason=apple-silicon-macos-docker-desktop-compat-reviewed-launch-host$' "${TMP_DIR}/compat-doctor-supported.stdout"
grep -q '^profile_dir=none$' "${TMP_DIR}/compat-doctor-supported.stdout"
grep -Eq '^target_state_dir=.*/targets/local_compat/docker-desktop/wcl-workspace-[a-f0-9]+$' "${TMP_DIR}/compat-doctor-supported.stdout"
grep -Eq '^profile_marker=.*/targets/local_compat/docker-desktop/wcl-workspace-[a-f0-9]+/workcell\.managed$' "${TMP_DIR}/compat-doctor-supported.stdout"
grep -Eq '^image_marker=.*/targets/local_compat/docker-desktop/wcl-workspace-[a-f0-9]+/workcell\.image-ready$' "${TMP_DIR}/compat-doctor-supported.stdout"
if [[ "${EXPECTED_ALLOWED_MISSING}" == "none" ]]; then
  DOCTOR_RECOMMENDED_NEXT="$(sed -n 's/^doctor_recommended_next=//p' "${TMP_DIR}/compat-doctor-supported.stdout")"
  grep -q '^doctor_missing_host_tools=none$' "${TMP_DIR}/compat-doctor-supported.stdout"
  [[ "${DOCTOR_RECOMMENDED_NEXT}" == *"workcell"* ]]
  [[ "${DOCTOR_RECOMMENDED_NEXT}" == *"--prepare"* ]]
  [[ "${DOCTOR_RECOMMENDED_NEXT}" == *"--agent codex"* ]]
  [[ "${DOCTOR_RECOMMENDED_NEXT}" == *"--target docker-desktop"* ]]
  [[ "${DOCTOR_RECOMMENDED_NEXT}" == *"--workspace "* ]]
else
  grep -q "^doctor_missing_host_tools=${EXPECTED_ALLOWED_MISSING}\$" "${TMP_DIR}/compat-doctor-supported.stdout"
  grep -q '^doctor_recommended_next=install-host-tools$' "${TMP_DIR}/compat-doctor-supported.stdout"
fi

run_with_support_override \
  "compat-dry-run-supported" \
  macos \
  arm64 \
  --target docker-desktop \
  --agent codex \
  --dry-run
grep -q '^target_kind=local_compat target_provider=docker-desktop target_id=desktop-linux target_assurance_class=compat runtime_api=docker workspace_transport=workspace-mount$' "${TMP_DIR}/compat-dry-run-supported.stderr"
grep -Eq '^execution_path=managed-tier1 audit_log=.*/targets/local_compat/docker-desktop/wcl-workspace-[a-f0-9]+/workcell\.audit\.log$' "${TMP_DIR}/compat-dry-run-supported.stderr"

if [[ "${EXPECTED_ALLOWED_MISSING}" == "none" ]]; then
  PATH="/usr/bin:/bin:/usr/sbin:/sbin" run_with_support_override \
    "compat-dry-run-supported-no-path-docker" \
    macos \
    arm64 \
    --target docker-desktop \
    --agent codex \
    --dry-run
  grep -q '^target_kind=local_compat target_provider=docker-desktop target_id=desktop-linux target_assurance_class=compat runtime_api=docker workspace_transport=workspace-mount$' "${TMP_DIR}/compat-dry-run-supported-no-path-docker.stderr"
  grep -Eq '^execution_path=managed-tier1 audit_log=.*/targets/local_compat/docker-desktop/wcl-workspace-[a-f0-9]+/workcell\.audit\.log$' "${TMP_DIR}/compat-dry-run-supported-no-path-docker.stderr"
fi

run_with_support_override \
  "compat-doctor-blocked" \
  linux \
  arm64 \
  --target docker-desktop \
  --agent codex \
  --doctor
grep -q '^support_matrix_status=unsupported$' "${TMP_DIR}/compat-doctor-blocked.stdout"
grep -q '^support_matrix_launch=blocked$' "${TMP_DIR}/compat-doctor-blocked.stdout"
grep -q '^doctor_recommended_next=use-supported-host$' "${TMP_DIR}/compat-doctor-blocked.stdout"

run_with_support_override_expect_failure \
  2 \
  "compat-dry-run-blocked" \
  linux \
  arm64 \
  --target docker-desktop \
  --agent codex \
  --dry-run
grep -q 'Workcell launch is not supported' "${TMP_DIR}/compat-dry-run-blocked.stderr"
grep -q 'local_compat/docker-desktop/compat' "${TMP_DIR}/compat-dry-run-blocked.stderr"
grep -q 'Docker Desktop target remains a lower-assurance compat path' "${TMP_DIR}/compat-dry-run-blocked.stderr"

echo "Compat target dry-run scenario passed"
