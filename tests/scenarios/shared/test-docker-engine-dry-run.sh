#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-docker-engine-scenario.XXXXXX")"
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

# run_host <expected_rc> <label> <host_os> <host_arch> <args...>
run_host() {
  local expected_rc="$1"
  local label="$2"
  local host_os="$3"
  local host_arch="$4"
  local host_distro="none" host_distro_version="none"
  local rc=0
  shift 4
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
    /bin/bash -p "${ROOT_DIR}/scripts/workcell" \
    --target docker-engine \
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

# Doctor on a Linux host names the preview boundary: the rootful socket and
# whether the daemon answers there.  This host has no rootful socket unless
# /var/run/docker.sock exists, so the probe must report unreachable.
run_host 0 doctor linux amd64 --doctor
expect doctor stdout '^target_kind=local_compat$'
expect doctor stdout '^target_provider=docker-engine$'
expect doctor stdout '^target_assurance_class=compat$'
expect doctor stdout '^runtime_api=docker$'
expect doctor stdout '^workspace_transport=workspace-mount$'
expect doctor stdout '^support_matrix_status=preview-only$'
expect doctor stdout '^support_matrix_launch=blocked$'
expect doctor stdout '^support_matrix_reason=linux-amd64-docker-engine-compat-preview-certification-only$'
expect doctor stdout '^doctor_docker_engine_socket=unix:///var/run/docker\.sock$'
if [[ -S /var/run/docker.sock ]]; then
  expect doctor stdout '^doctor_docker_engine_daemon=(reachable|unreachable)$'
else
  expect doctor stdout '^doctor_docker_engine_daemon=unreachable$'
fi
if grep -q '^doctor_missing_host_tools=none$' "${TMP_DIR}/doctor.stdout"; then
  expect doctor stdout '^doctor_recommended_next=review-docker-engine-preview$'
else
  expect doctor stdout '^doctor_recommended_next=install-host-tools$'
fi

# The dry-run prints the reviewed plan on Linux amd64 and arm64 and launches
# nothing.  No egress control is wired yet, so the label is none.
for arch in amd64 arm64; do
  run_host 0 "dry-run-${arch}" linux "${arch}" --dry-run
  expect "dry-run-${arch}" stderr '^target_kind=local_compat target_provider=docker-engine target_id=[^ ]+ target_assurance_class=compat runtime_api=docker workspace_transport=workspace-mount$'
  expect "dry-run-${arch}" stderr '^egress_enforcement=none$'
  expect "dry-run-${arch}" stderr '^local_access_model=direct docker_engine_socket=unix:///var/run/docker\.sock rootful=1 live_smoke=certification-only$'
  expect "dry-run-${arch}" stderr '^execution_path=docker-engine-preview-plan audit_log=.*/targets/local_compat/docker-engine/[^/]+/workcell\.audit\.log rollout_doc=docs/runtime-target-expansion-plan\.md$'
  expect "dry-run-${arch}" stdout '^docker-engine-preview-plan target=docker-engine target_id=[^ ]+ launch_gate=certification-only workspace=.*/workspace$'
  refute "dry-run-${arch}" stdout 'run '
done

# macOS and Windows hosts are unsupported: no plan and no daemon probe.
run_host 2 dry-run-macos macos arm64 --dry-run
expect dry-run-macos stderr 'Workcell launch is not supported for macos/arm64/none/none on target local_compat/docker-engine/compat'
expect dry-run-macos stderr 'The Docker Engine target is a preview-only lower-assurance compat path'
refute dry-run-macos stdout 'docker-engine-preview-plan'

run_host 0 doctor-macos macos arm64 --doctor
expect doctor-macos stdout '^support_matrix_status=unsupported$'
expect doctor-macos stdout '^doctor_recommended_next=use-supported-host$'
refute doctor-macos stdout '^doctor_docker_engine_'

run_host 2 dry-run-windows windows amd64 --dry-run
expect dry-run-windows stderr 'Workcell launch is not supported for windows/amd64/none/none'
refute dry-run-windows stdout 'docker-engine-preview-plan'

# A live launch stays blocked on the preview-only row.
run_host 2 live-blocked linux amd64
expect live-blocked stderr 'The Docker Engine target is a preview-only lower-assurance compat path'
expect live-blocked stderr 'docs/runtime-target-expansion-plan\.md'
refute live-blocked stdout 'docker-engine-preview-plan'

run_host 2 prepare-refused linux amd64 --prepare --dry-run
expect prepare-refused stderr 'do not apply to the docker-engine preview target'
refute prepare-refused stdout 'docker-engine-preview-plan'

run_host 2 target-id-refused linux amd64 --target-id other --dry-run
expect target-id-refused stderr '^--target-id only supports'

run_host 2 egress-proxy-refused linux amd64 --egress-proxy --dry-run
expect egress-proxy-refused stderr '^--egress-proxy supports only --target colima'

echo "Docker Engine dry-run scenario passed"
