set -euo pipefail

function_body="$(declare -f delete_verify_colima_profile)"
function_body="$(printf '%s\n' "${function_body}" | sed \
  -e 's|if \[\[ -x /opt/homebrew/bin/colima \]\]; then|if [[ "${COLIMA_VARIANT}" == "homebrew" ]]; then|' \
  -e 's|if \[\[ -x /usr/local/bin/colima \]\]; then|if [[ "${COLIMA_VARIANT}" == "local" ]]; then|' \
  -e 's|/opt/homebrew/bin/colima delete|fake_colima delete|' \
  -e 's|/usr/local/bin/colima delete|fake_colima delete|')"
eval "${function_body}"

profile_name="wcl-live-det-test"
REAL_HOME="/tmp/workcell-verify-profile-delete-reaper"
event_log=""
reaper_calls=0
remove_calls=0
REAPER_STATUS=0

fake_colima() {
  [[ "$*" == "delete --profile ${profile_name} --force" ]] || {
    echo "Unexpected Colima delete invocation: $*" >&2
    return 1
  }
  event_log="${event_log}D"
}

go_verify_hostutil() {
  [[ "$*" == "helper reap-colima-profile-processes ${profile_name}" ]] || {
    echo "Unexpected profile reaper invocation: $*" >&2
    return 1
  }
  reaper_calls=$((reaper_calls + 1))
  event_log="${event_log}R"
  return "${REAPER_STATUS}"
}

rm() {
  remove_calls=$((remove_calls + 1))
  event_log="${event_log}M"
}

assert_success() {
  local variant="$1"

  COLIMA_VARIANT="${variant}"
  event_log=""
  reaper_calls=0
  remove_calls=0
  REAPER_STATUS=0
  delete_verify_colima_profile "${profile_name}"
  [[ "${event_log}" == "DRMM" && "${reaper_calls}" -eq 1 && "${remove_calls}" -eq 2 ]] || {
    echo "Expected delete, one reaper call, and state removal for ${variant}, got ${event_log}" >&2
    exit 1
  }
}

assert_reaper_failure() {
  local status=0

  COLIMA_VARIANT="homebrew"
  event_log=""
  reaper_calls=0
  remove_calls=0
  REAPER_STATUS=23
  if delete_verify_colima_profile "${profile_name}"; then
    echo "Expected profile reaper failure to stop cleanup" >&2
    exit 1
  else
    status=$?
  fi
  [[ "${status}" -ne 0 && "${event_log}" == "DR" && "${reaper_calls}" -eq 1 && "${remove_calls}" -eq 0 ]] || {
    echo "Expected failed reaper to prevent state removal, got ${event_log}" >&2
    exit 1
  }
}

assert_success homebrew
assert_success local
assert_reaper_failure
