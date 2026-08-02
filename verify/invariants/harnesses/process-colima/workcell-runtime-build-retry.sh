# shellcheck shell=bash disable=SC2034
ROOT="$(mktemp -d)"
COLIMA_PROFILE="runtime-build-retry-fixture"
TARGET_BACKEND="colima"
NETWORK_POLICY="disabled"
BOOTSTRAP_ENDPOINTS=""
SOURCE_DATE_EPOCH=1
IMAGE_TAG="workcell-runtime:test"
ROOT_DIR="${ROOT}/repo"
WORKSPACE="${ROOT}/workspace"
PROFILE_WORKSPACE_ROOT="${WORKSPACE}"
PROFILE_MARKER="${ROOT}/state-${COLIMA_PROFILE}/workspace-root"
DEBUG_LOG_PATH="${ROOT}/debug.log"
FILE_TRACE_LOG_PATH="${ROOT}/file-trace.log"
AUDIT_TRANSCRIPT_PATH="${ROOT}/transcript.log"
SESSION_AUDIT_DIR="${ROOT}/session-audit"
SESSION_AUDIT_STATE_FILE="${SESSION_AUDIT_DIR}/session-assurance"
PROFILE_VALIDATED=1
PROFILE_RUNNING=1
REFRESH_COUNT=0
START_COUNT=0
VALIDATE_COUNT=0
BUILD_COUNT=0
SECURITY_COUNT=0
SLEEP_COUNT=0
RESTORE_COUNT=0
ENSURE_AUDIT_COUNT=0
CLAIM_COUNT=0 CLEANUP_COUNT=0 CLEANUP_FAIL=0
CREATE_COUNT=0 CREATE_FAILURES_REMAINING=0 BUILD_FAILURES_REMAINING=1

mkdir -p "$(dirname "${PROFILE_MARKER}")" "${ROOT_DIR}" "${WORKSPACE}" "${SESSION_AUDIT_DIR}"
printf 'stale\n' >"${PROFILE_MARKER}"

profile_target_state_dir() { printf '%s/state-%s\n' "${ROOT}" "$1"; }
profile_latest_log_pointer_path() { printf '%s/pointers/%s-%s\n' "${ROOT}" "$1" "$2"; }
resolve_host_output_candidate() { printf '%s\n' "$1"; }
restore_profile_audit_log() {
  RESTORE_COUNT=$((RESTORE_COUNT + 1))
}
ensure_session_audit_state() {
  ENSURE_AUDIT_COUNT=$((ENSURE_AUDIT_COUNT + 1))
  mkdir -p "${SESSION_AUDIT_DIR}"
  rm -f "${SESSION_AUDIT_STATE_FILE}"
}
resolve_codex_release_url() { return 0; }
resolve_copilot_release_url() { return 0; }
refresh_managed_profile() {
  REFRESH_COUNT=$((REFRESH_COUNT + 1))
  PROFILE_VALIDATED=0
  rm -f "${PROFILE_MARKER}"
}
start_managed_profile() {
  START_COUNT=$((START_COUNT + 1))
  PROFILE_RUNNING=1
}
validate_colima_profile() {
  VALIDATE_COUNT=$((VALIDATE_COUNT + 1))
  return 0
}
validate_runtime_security_posture() {
  SECURITY_COUNT=$((SECURITY_COUNT + 1))
}
begin_runtime_builder() { CLAIM_COUNT=$((CLAIM_COUNT + 1)); BUILDX_BUILDER="workcell-runtime-${COLIMA_PROFILE}"; }
cleanup_runtime_builder() { CLEANUP_COUNT=$((CLEANUP_COUNT + 1)); [[ "${CLEANUP_FAIL}" -eq 0 ]]; }
create_runtime_builder() {
  CREATE_COUNT=$((CREATE_COUNT + 1))
  [[ "${CREATE_FAILURES_REMAINING}" -eq 0 ]] || { CREATE_FAILURES_REMAINING=$((CREATE_FAILURES_REMAINING - 1)); return 1; }
}
buildx_cmd() { :; }
run_command_with_debug_log() {
  BUILD_COUNT=$((BUILD_COUNT + 1))
  BUILD_ARGS="$*"
  if [[ "${BUILD_FAILURES_REMAINING}" -gt 0 ]]; then
    BUILD_FAILURES_REMAINING=$((BUILD_FAILURES_REMAINING - 1))
    return 37
  fi
  return 0
}
sleep() {
  SLEEP_COUNT=$((SLEEP_COUNT + 1))
}

run_runtime_image_build_with_retries "${SOURCE_DATE_EPOCH}"
((BUILD_COUNT == 2 && REFRESH_COUNT == 1 && START_COUNT == 1))
((VALIDATE_COUNT == 1 && RESTORE_COUNT == 1 && ENSURE_AUDIT_COUNT == 1))
((CLAIM_COUNT == 2 && CREATE_COUNT == 2 && CLEANUP_COUNT == 2))
((SECURITY_COUNT == 1 && SLEEP_COUNT == 1))
[[ "${BUILD_ARGS}" == *"buildx_cmd build --builder workcell-runtime-${COLIMA_PROFILE}"* ]]
[[ "${PROFILE_VALIDATED}" -eq 1 ]]
[[ "$(cat "${PROFILE_MARKER}")" == "${PROFILE_WORKSPACE_ROOT}" ]]
[[ -f "${ROOT}/pointers/${COLIMA_PROFILE}-debug" ]]
[[ -f "${ROOT}/pointers/${COLIMA_PROFILE}-file-trace" ]]
[[ -f "${ROOT}/pointers/${COLIMA_PROFILE}-transcript" ]]

BUILD_COUNT=0 CLAIM_COUNT=0 CLEANUP_COUNT=0 CLEANUP_FAIL=1
CREATE_COUNT=0 BUILD_FAILURES_REMAINING=1 REFRESH_COUNT=0 SLEEP_COUNT=0
if run_runtime_image_build_with_retries "${SOURCE_DATE_EPOCH}"; then
  echo "Expected runtime build retries to abort when exact builder cleanup fails." >&2
  exit 1
else
  BUILD_STATUS=$?
fi
((BUILD_STATUS == 2 && BUILD_COUNT == 1 && CLAIM_COUNT == 1))
((CREATE_COUNT == 1 && CLEANUP_COUNT == 1 && REFRESH_COUNT == 0 && SLEEP_COUNT == 0))

BUILD_COUNT=0 BUILD_FAILURES_REMAINING=0 CLAIM_COUNT=0 CLEANUP_COUNT=0
CLEANUP_FAIL=0 CREATE_COUNT=0 CREATE_FAILURES_REMAINING=1 REFRESH_COUNT=0 SLEEP_COUNT=0
run_runtime_image_build_with_retries "${SOURCE_DATE_EPOCH}"
((BUILD_COUNT == 1 && CLAIM_COUNT == 2 && CREATE_COUNT == 2))
((CLEANUP_COUNT == 2 && REFRESH_COUNT == 1 && SLEEP_COUNT == 1))
