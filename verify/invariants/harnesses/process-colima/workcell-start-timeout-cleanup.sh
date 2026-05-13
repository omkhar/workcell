COLIMA_PROFILE="start-timeout-cleanup-fixture"
WORKSPACE="/tmp/workspace"
COLIMA_CPU=4
COLIMA_MEMORY=8
COLIMA_DISK=60
PROFILE_RUNNING=0
RUN_COUNT=0
REFRESH_COUNT=0
FINAL_STATUS=0

maybe_reap_stale_profile_processes() { :; }
reap_stale_profile_processes() { :; }
run_command_with_debug_log() {
  RUN_COUNT=$((RUN_COUNT + 1))
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
