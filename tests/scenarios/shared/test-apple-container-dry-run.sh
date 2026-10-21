#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-apple-container-scenario.XXXXXX")"
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

# run_host <expected_rc> <label> <host_os> <host_arch> <macos_version> <args...>
run_host() {
  local expected_rc="$1"
  local label="$2"
  local host_os="$3"
  local host_arch="$4"
  local macos_version="$5"
  local host_distro="none" host_distro_version="none"
  local rc=0
  shift 5
  if [[ "${host_os}" == "linux" ]]; then
    host_distro="ubuntu" host_distro_version="24.04"
  fi
  set +e
  HOME="${HOME_DIR}" XDG_CONFIG_HOME="${HOME_DIR}/.config" \
    WORKCELL_VERIFY_INVARIANTS_SANITIZED_ENTRYPOINT=1 \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_OS="${host_os}" \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_ARCH="${host_arch}" \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_DISTRO="${host_distro}" \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_DISTRO_VERSION="${host_distro_version}" \
    WORKCELL_TEST_MACOS_PRODUCT_VERSION="${macos_version}" \
    /bin/bash -p "${ROOT_DIR}/scripts/workcell" \
    --target apple-container \
    --agent codex \
    "$@" \
    --workspace "${WORKSPACE}" \
    --no-default-injection-policy \
    >"${TMP_DIR}/${label}.stdout" 2>"${TMP_DIR}/${label}.stderr"
  rc=$?
  set -e
  if [[ "${rc}" -ne "${expected_rc}" ]]; then
    echo "Unexpected exit code for ${label}: ${rc} (expected ${expected_rc})" >&2
    cat "${TMP_DIR}/${label}.stderr" >&2
    exit 1
  fi
}

expect() {
  local label="$1" stream="$2" pattern="$3"
  if ! grep -Eq -- "${pattern}" "${TMP_DIR}/${label}.${stream}"; then
    echo "Expected ${label} ${stream} to match: ${pattern}" >&2
    cat "${TMP_DIR}/${label}.${stream}" >&2
    exit 1
  fi
}

refute() {
  local label="$1" stream="$2" pattern="$3"
  if grep -Eq -- "${pattern}" "${TMP_DIR}/${label}.${stream}"; then
    echo "Expected ${label} ${stream} not to match: ${pattern}" >&2
    cat "${TMP_DIR}/${label}.${stream}" >&2
    exit 1
  fi
}

expected_missing_tools() {
  local candidate=""
  for candidate in /usr/local/bin/container /opt/homebrew/bin/container; do
    if [[ -x "${candidate}" ]]; then
      printf 'none\n'
      return 0
    fi
  done
  printf 'container\n'
}

# Doctor on an Apple Silicon macOS 27 host names the preview boundary.
run_host 0 doctor-ready macos arm64 27.0.1 --doctor
expect doctor-ready stdout '^target_kind=local_vm$'
expect doctor-ready stdout '^target_provider=apple-container$'
expect doctor-ready stdout '^target_assurance_class=per-session-vm$'
expect doctor-ready stdout '^runtime_api=local-cli$'
expect doctor-ready stdout '^workspace_transport=local-materialization$'
expect doctor-ready stdout '^support_matrix_status=preview-only$'
expect doctor-ready stdout '^support_matrix_launch=blocked$'
expect doctor-ready stdout '^support_matrix_reason=apple-silicon-macos-27-apple-container-evaluation-preview$'
expect doctor-ready stdout '^doctor_apple_container_host=ready$'
if [[ "$(expected_missing_tools)" == "none" ]]; then
  expect doctor-ready stdout '^doctor_missing_host_tools=none$'
  expect doctor-ready stdout '^doctor_recommended_next=review-apple-container-preview$'
else
  expect doctor-ready stdout '^doctor_missing_host_tools=container$'
  expect doctor-ready stdout '^doctor_recommended_next=install-host-tools$'
fi

# The dry-run prints the reviewed plan and launches nothing.
run_host 0 dry-run macos arm64 27.0.1 --dry-run
expect dry-run stderr '^target_kind=local_vm target_provider=apple-container target_id=[^ ]+ target_assurance_class=per-session-vm runtime_api=local-cli workspace_transport=local-materialization$'
expect dry-run stderr '^egress_enforcement=none$'
expect dry-run stderr '^local_access_model=direct per_session_vm=1 live_smoke=certification-only$'
expect dry-run stderr '^execution_path=apple-container-preview-plan audit_log=.*/targets/local_vm/apple-container/[^/]+/workcell\.audit\.log rollout_doc=docs/apple-container-evaluation\.md$'
expect dry-run stdout '^apple-container-preview-plan target=apple-container target_id=[^ ]+ launch_gate=certification-only workspace=.*/workspace$'
refute dry-run stdout 'run '

# The guard refuses macOS 26, Intel and Linux hosts; no plan is printed.
run_host 2 dry-run-macos26 macos arm64 26.5.1 --dry-run
expect dry-run-macos26 stderr 'apple container requires macOS 27\+, host is macOS 26\.5\.1'
expect dry-run-macos26 stderr 'Workcell launch is not supported for macos/arm64/none/none on target local_vm/apple-container/per-session-vm'
refute dry-run-macos26 stdout 'apple-container-preview-plan'

run_host 0 doctor-macos26 macos arm64 26.5.1 --doctor
expect doctor-macos26 stdout '^doctor_apple_container_host=blocked$'
expect doctor-macos26 stdout '^doctor_recommended_next=use-supported-host$'
expect doctor-macos26 stderr 'apple container requires macOS 27\+, host is macOS 26\.5\.1'

run_host 2 dry-run-intel macos amd64 27.0.1 --dry-run
expect dry-run-intel stderr 'apple container requires Apple Silicon \(arm64\), host arch is "amd64"'
refute dry-run-intel stdout 'apple-container-preview-plan'

run_host 2 dry-run-linux linux arm64 "" --dry-run
expect dry-run-linux stderr 'apple container requires macOS 27\+, host OS is "linux"'
expect dry-run-linux stderr 'Workcell launch is not supported for linux/arm64/ubuntu/24.04'
refute dry-run-linux stdout 'apple-container-preview-plan'

# A live launch stays blocked on the preview-only row, even on a ready host.
run_host 2 live-blocked macos arm64 27.0.1
expect live-blocked stderr 'The Apple container target is a preview-only per-session VM path'
expect live-blocked stderr 'docs/apple-container-evaluation\.md'
refute live-blocked stdout 'apple-container-preview-plan'

run_host 2 prepare-refused macos arm64 27.0.1 --prepare --dry-run
expect prepare-refused stderr 'do not apply to the apple-container preview target'

run_host 2 target-id-refused macos arm64 27.0.1 --target-id other --dry-run
expect target-id-refused stderr '^--target-id only supports'

echo "Apple container dry-run scenario passed"
