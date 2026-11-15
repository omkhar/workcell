COLIMA_PROFILE="start-timeout-cleanup-fixture"
WORKSPACE="/tmp/workspace"
REAL_HOME="$(mktemp -d)"
COLIMA_CPU=4
COLIMA_MEMORY=8
COLIMA_DISK=60
PROFILE_RUNNING=0
RUN_COUNT=0
REFRESH_COUNT=0
START_ARGS=""
FINAL_STATUS=0

trap 'rm -rf "${REAL_HOME}"' EXIT

# The macOS host: colima_vm_type and colima_mount_type pick vz and virtiofs.
detected_host_os() { printf 'macos\n'; }
maybe_reap_stale_profile_processes() { :; }
reap_stale_profile_processes() { :; }
run_command_with_debug_log() {
  RUN_COUNT=$((RUN_COUNT + 1))
  START_ARGS="$*"
  return 124
}
refresh_managed_profile() {
  REFRESH_COUNT=$((REFRESH_COUNT + 1))
  return 0
}

if start_managed_profile; then
  echo "Expected repeated Colima start timeouts to fail" >&2
  exit 1
else
  FINAL_STATUS=$?
fi
[[ "${RUN_COUNT}" -eq 2 ]]
[[ "${REFRESH_COUNT}" -eq 2 ]]
[[ "${FINAL_STATUS}" -eq 124 ]]
[[ "${PROFILE_RUNNING}" -eq 0 ]]
[[ "${START_ARGS}" == *" --vm-type vz "* ]]
[[ "${START_ARGS}" == *" --mount-type virtiofs "* ]]
