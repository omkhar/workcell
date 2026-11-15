COLIMA_PROFILE="start-retry-fixture"
WORKSPACE="/tmp/workspace"
REAL_HOME="$(mktemp -d)"
COLIMA_CPU=4
COLIMA_MEMORY=8
COLIMA_DISK=60
PROFILE_RUNNING=0
RUN_COUNT=0
REFRESH_COUNT=0
START_ARGS=""

trap 'rm -rf "${REAL_HOME}"' EXIT

# The macOS host: colima_vm_type and colima_mount_type pick vz and virtiofs.
detected_host_os() { printf 'macos\n'; }
maybe_reap_stale_profile_processes() { :; }
reap_stale_profile_processes() { :; }
run_command_with_debug_log() {
  RUN_COUNT=$((RUN_COUNT + 1))
  START_ARGS="$*"
  if [[ "${RUN_COUNT}" -eq 1 ]]; then
    return 124
  fi
  return 0
}
refresh_managed_profile() {
  REFRESH_COUNT=$((REFRESH_COUNT + 1))
  return 0
}

start_managed_profile
[[ "${RUN_COUNT}" -eq 2 ]]
[[ "${REFRESH_COUNT}" -eq 1 ]]
[[ "${PROFILE_RUNNING}" -eq 1 ]]
[[ "${START_ARGS}" == *" --vm-type vz "* ]]
[[ "${START_ARGS}" == *" --mount-type virtiofs "* ]]
