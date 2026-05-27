# shellcheck shell=bash disable=SC2034
PROFILE_REFRESH_EXPECTED_IMAGE_ID=""
PROFILE_REFRESH_EXPECTED_SOURCE_DATE_EPOCH=""
MARKER_IMAGE="sha256:prepared"
MARKER_SOURCE_DATE_EPOCH="12345"
LIVE_IMAGE="${MARKER_IMAGE}"
CACHE_MATCHES=0
CACHE_CALLS=0
CACHE_STATUS=0

profile_image_marker_value() {
  case "$2" in
    image_id)
      printf '%s\n' "${MARKER_IMAGE}"
      ;;
    source_date_epoch)
      printf '%s\n' "${MARKER_SOURCE_DATE_EPOCH}"
      ;;
    *)
      return 1
      ;;
  esac
}

current_profile_image_id() {
  printf '%s\n' "${LIVE_IMAGE}"
}

profile_runtime_image_cache_matches() {
  [[ "$1" == "refresh-cache-fixture" ]]
  [[ "$2" == "${MARKER_IMAGE}" ]]
  [[ "$3" == "${MARKER_SOURCE_DATE_EPOCH}" ]]
  [[ "${CACHE_MATCHES}" -eq 1 ]]
}

cache_profile_runtime_image() {
  CACHE_CALLS=$((CACHE_CALLS + 1))
  [[ "$1" == "refresh-cache-fixture" ]]
  [[ "$2" == "${MARKER_IMAGE}" ]]
  return "${CACHE_STATUS}"
}

remember_profile_runtime_image_for_refresh "refresh-cache-fixture"
[[ "${CACHE_CALLS}" -eq 1 ]]
[[ "${PROFILE_REFRESH_EXPECTED_IMAGE_ID}" == "${MARKER_IMAGE}" ]]
[[ "${PROFILE_REFRESH_EXPECTED_SOURCE_DATE_EPOCH}" == "${MARKER_SOURCE_DATE_EPOCH}" ]]

CACHE_CALLS=0
CACHE_MATCHES=1
PROFILE_REFRESH_EXPECTED_IMAGE_ID=""
PROFILE_REFRESH_EXPECTED_SOURCE_DATE_EPOCH=""
remember_profile_runtime_image_for_refresh "refresh-cache-fixture"
[[ "${CACHE_CALLS}" -eq 0 ]]
[[ "${PROFILE_REFRESH_EXPECTED_IMAGE_ID}" == "${MARKER_IMAGE}" ]]
[[ "${PROFILE_REFRESH_EXPECTED_SOURCE_DATE_EPOCH}" == "${MARKER_SOURCE_DATE_EPOCH}" ]]

CACHE_CALLS=0
CACHE_MATCHES=0
LIVE_IMAGE=""
PROFILE_REFRESH_EXPECTED_IMAGE_ID=""
PROFILE_REFRESH_EXPECTED_SOURCE_DATE_EPOCH=""
remember_profile_runtime_image_for_refresh "refresh-cache-fixture"
[[ "${CACHE_CALLS}" -eq 0 ]]
[[ -z "${PROFILE_REFRESH_EXPECTED_IMAGE_ID}" ]]
[[ -z "${PROFILE_REFRESH_EXPECTED_SOURCE_DATE_EPOCH}" ]]

CACHE_CALLS=0
LIVE_IMAGE="sha256:other"
PROFILE_REFRESH_EXPECTED_IMAGE_ID=""
PROFILE_REFRESH_EXPECTED_SOURCE_DATE_EPOCH=""
remember_profile_runtime_image_for_refresh "refresh-cache-fixture"
[[ "${CACHE_CALLS}" -eq 0 ]]
[[ -z "${PROFILE_REFRESH_EXPECTED_IMAGE_ID}" ]]
[[ -z "${PROFILE_REFRESH_EXPECTED_SOURCE_DATE_EPOCH}" ]]
