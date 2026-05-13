ROOT="$(mktemp -d)"
COLIMA_PROFILE="refresh-fixture"
PROFILE_WAS_REFRESHED=0
PROFILE_PREEXISTED=1
PROFILE_MARKER_WORKSPACE="bound"
PROFILE_RUNNING=1

stash_profile_audit_log() { :; }
remember_profile_runtime_image_for_refresh() { :; }
reap_stale_profile_processes() { :; }
run_host_colima_with_timeout() { return 124; }
validate_colima_profile_name() { :; }
target_provider_for_profile_state() { printf 'colima\n'; }
profile_target_state_dir() { printf '%s/state-%s\n' "${ROOT}" "$1"; }
profile_dir() { printf '%s/profile-%s\n' "${ROOT}" "$1"; }
profile_lima_dir() { printf '%s/lima-%s\n' "${ROOT}" "$1"; }
profile_disk_dir() { printf '%s/disk-%s\n' "${ROOT}" "$1"; }
profile_process_pids() { return 1; }

PROFILE_DIR="$(profile_dir "${COLIMA_PROFILE}")"
PROFILE_STATE_DIR="$(profile_target_state_dir "${COLIMA_PROFILE}")"
mkdir -p "${PROFILE_STATE_DIR}" "${PROFILE_DIR}" "$(profile_lima_dir "${COLIMA_PROFILE}")" "$(profile_disk_dir "${COLIMA_PROFILE}")"
refresh_managed_profile "refreshing fixture profile"
[[ ! -e "${PROFILE_STATE_DIR}" ]]
[[ ! -e "${PROFILE_DIR}" ]]
[[ ! -e "$(profile_lima_dir "${COLIMA_PROFILE}")" ]]
[[ ! -e "$(profile_disk_dir "${COLIMA_PROFILE}")" ]]
[[ "${PROFILE_WAS_REFRESHED}" -eq 1 ]]
[[ "${PROFILE_PREEXISTED}" -eq 0 ]]
[[ -z "${PROFILE_MARKER_WORKSPACE}" ]]
[[ "${PROFILE_RUNNING}" -eq 0 ]]
