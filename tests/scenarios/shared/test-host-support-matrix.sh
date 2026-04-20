#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-host-support-matrix.XXXXXX")"
# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/go-run-env.sh"

cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

WORKSPACE="${TMP_DIR}/workspace"
PROFILE="wcl-host-support-$$"
mkdir -p "${WORKSPACE}"
git -C "${WORKSPACE}" init -q
printf 'support matrix fixture\n' >"${WORKSPACE}/README.md"

detected_host_os() {
  local host_os=""

  host_os="$(uname -s 2>/dev/null || true)"
  case "${host_os}" in
    Darwin)
      printf 'macos\n'
      ;;
    Linux)
      printf 'linux\n'
      ;;
    MINGW* | MSYS* | CYGWIN* | Windows_NT)
      printf 'windows\n'
      ;;
    *)
      printf '%s\n' "$(printf '%s' "${host_os}" | tr '[:upper:]' '[:lower:]')"
      ;;
  esac
}

detected_host_arch() {
  local host_arch=""

  host_arch="$(uname -m 2>/dev/null || true)"
  case "${host_arch}" in
    arm64 | aarch64)
      printf 'arm64\n'
      ;;
    x86_64 | amd64)
      printf 'amd64\n'
      ;;
    *)
      printf '%s\n' "$(printf '%s' "${host_arch}" | tr '[:upper:]' '[:lower:]')"
      ;;
  esac
}

run_support_matrix_eval() {
  local host_os="$1"
  local host_arch="$2"

  run_go_in_repo "${ROOT_DIR}" run ./cmd/workcell-hostutil launcher support-matrix-eval \
    "${ROOT_DIR}/policy/host-support-matrix.tsv" \
    "${host_os}" \
    "${host_arch}" \
    local_vm \
    colima \
    strict
}

CURRENT_HOST_OS="$(detected_host_os)"
CURRENT_HOST_ARCH="$(detected_host_arch)"
CURRENT_SUPPORT_OUTPUT="$(run_support_matrix_eval "${CURRENT_HOST_OS}" "${CURRENT_HOST_ARCH}")"

inspect_output="$("${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --no-default-injection-policy \
  --allow-nongit-workspace \
  --workspace "${WORKSPACE}" \
  --colima-profile "${PROFILE}" \
  --inspect)"
doctor_output="$("${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --no-default-injection-policy \
  --allow-nongit-workspace \
  --workspace "${WORKSPACE}" \
  --colima-profile "${PROFILE}" \
  --doctor)"

while IFS= read -r expected_line; do
  [[ -n "${expected_line}" ]] || continue
  grep -Fqx "${expected_line}" <<<"${inspect_output}"
  grep -Fqx "${expected_line}" <<<"${doctor_output}"
done <<<"${CURRENT_SUPPORT_OUTPUT}"

if grep -q '^support_matrix_launch=allowed$' <<<"${CURRENT_SUPPORT_OUTPUT}"; then
  dry_run_output="$("${ROOT_DIR}/scripts/workcell" \
    --agent codex \
    --no-default-injection-policy \
    --allow-nongit-workspace \
    --workspace "${WORKSPACE}" \
    --colima-profile "${PROFILE}" \
    --dry-run 2>&1)"
  grep -q 'docker run' <<<"${dry_run_output}"
else
  set +e
  dry_run_output="$("${ROOT_DIR}/scripts/workcell" \
    --agent codex \
    --no-default-injection-policy \
    --allow-nongit-workspace \
    --workspace "${WORKSPACE}" \
    --colima-profile "${PROFILE}" \
    --dry-run 2>&1)"
  dry_run_rc=$?
  set -e
  test "${dry_run_rc}" -eq 2
  grep -q 'Workcell launch is not supported' <<<"${dry_run_output}"
fi

macos_support_output="$(run_support_matrix_eval macos arm64)"
grep -q '^support_matrix_status=supported$' <<<"${macos_support_output}"
grep -q '^support_matrix_launch=allowed$' <<<"${macos_support_output}"
grep -q '^support_matrix_evidence=certification-only$' <<<"${macos_support_output}"

linux_validation_output="$(run_support_matrix_eval linux amd64)"
grep -q '^support_matrix_status=validation-host-only$' <<<"${linux_validation_output}"
grep -q '^support_matrix_launch=blocked$' <<<"${linux_validation_output}"
grep -q '^support_matrix_evidence=repo-required$' <<<"${linux_validation_output}"
grep -q '^support_matrix_validation_lane=trusted-linux-amd64-validator$' <<<"${linux_validation_output}"

windows_support_output="$(run_support_matrix_eval windows amd64)"
grep -q '^support_matrix_status=unsupported$' <<<"${windows_support_output}"
grep -q '^support_matrix_launch=blocked$' <<<"${windows_support_output}"
grep -q '^support_matrix_evidence=none$' <<<"${windows_support_output}"
grep -q '^support_matrix_validation_lane=none$' <<<"${windows_support_output}"

echo "Host support matrix scenario passed"
