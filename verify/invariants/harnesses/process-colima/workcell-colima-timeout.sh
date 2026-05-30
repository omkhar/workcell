# shellcheck shell=bash
# shellcheck disable=SC2034 # Harness globals are consumed by extracted scripts/workcell functions.
# Now that run_host_colima_with_timeout delegates the actual timeout
# enforcement to the Go helper subcommand
# (workcell-hostutil helper run-host-colima-with-timeout), this harness
# verifies the bash shim correctly forwards args and env-derived flags to
# go_hostutil. The end-to-end timeout behaviour is covered by
# TestRunHostColimaWithTimeoutKillsRunawayChild /
# TestRunHostColimaWithTimeoutReturnsExitCodeWhenFastEnough in
# internal/host/launcher/colima_test.go.

captured_args=""
captured_stderr=""
go_hostutil() {
  captured_args="$*"
  if [[ -n "${captured_stderr}" ]]; then
    printf '%s\n' "${captured_stderr}" >&2
  fi
  return "${captured_rc:-124}"
}

assert_contains() {
  local haystack="$1" needle="$2" message="$3"
  case "${haystack}" in
    *"${needle}"*) ;;
    *)
      echo "${message}: ${haystack}" >&2
      exit 1
      ;;
  esac
}

HOST_COLIMA_BIN="/fake/colima-binary"
REAL_HOME="/fake/real-home"
COLIMA_STATE_ROOT="/fake/colima-home"

captured_rc=124
status=0
run_host_colima_with_timeout 1 delete --profile timeout-fixture || status=$?
first_args="${captured_args}"

if [[ "${status}" -ne 124 ]]; then
  echo "Expected run_host_colima_with_timeout to propagate the Go-side timeout exit code 124, got ${status}" >&2
  exit 1
fi

assert_contains "${first_args}" "helper run-host-colima-with-timeout 1 " "Expected captured args to include 'helper run-host-colima-with-timeout 1'"
assert_contains "${first_args}" "--colima-bin=/fake/colima-binary" "Expected --colima-bin to forward HOST_COLIMA_BIN"
assert_contains "${first_args}" "--real-home=/fake/real-home" "Expected --real-home to forward REAL_HOME"
assert_contains "${first_args}" "--colima-home=/fake/colima-home" "Expected --colima-home to forward COLIMA_STATE_ROOT"

captured_rc=1
captured_stderr=$'timeout diagnostic\nexit status 124'
status=0
run_host_colima_with_timeout 1 stop --profile signal-fixture >/tmp/workcell-colima-timeout.stdout 2>/tmp/workcell-colima-timeout.stderr || status=$?

if [[ "${status}" -ne 124 ]]; then
  echo "Expected run_host_colima_with_timeout to recover the Go wrapper trailer exit code 124, got ${status}" >&2
  exit 1
fi

grep -q 'timeout diagnostic' /tmp/workcell-colima-timeout.stderr
if grep -q 'exit status 124' /tmp/workcell-colima-timeout.stderr; then
  echo "Expected run_host_colima_with_timeout to strip the go run exit trailer" >&2
  exit 1
fi

assert_contains "${first_args}" " -- delete --profile timeout-fixture" "Expected '-- delete --profile timeout-fixture' payload after the flag separator"
