# Now that run_host_colima_with_timeout delegates the actual timeout
# enforcement to the Go helper subcommand
# (workcell-hostutil helper run-host-colima-with-timeout), this harness
# verifies the bash shim correctly forwards args and env-derived flags to
# go_hostutil. The end-to-end timeout behaviour is covered by
# TestRunHostColimaWithTimeoutKillsRunawayChild /
# TestRunHostColimaWithTimeoutReturnsExitCodeWhenFastEnough in
# internal/host/launcher/colima_test.go.

captured_args=""
go_hostutil() {
  captured_args="$*"
  return 124
}

HOST_COLIMA_BIN="/fake/colima-binary"
REAL_HOME="/fake/real-home"
COLIMA_STATE_ROOT="/fake/colima-home"

status=0
run_host_colima_with_timeout 1 delete --profile timeout-fixture || status=$?

if [[ "${status}" -ne 124 ]]; then
  echo "Expected run_host_colima_with_timeout to propagate the Go-side timeout exit code 124, got ${status}" >&2
  exit 1
fi

case "${captured_args}" in
  *"helper run-host-colima-with-timeout 1 "*) ;;
  *)
    echo "Expected captured args to include 'helper run-host-colima-with-timeout 1': ${captured_args}" >&2
    exit 1
    ;;
esac

case "${captured_args}" in
  *"--colima-bin=/fake/colima-binary"*) ;;
  *)
    echo "Expected --colima-bin to forward HOST_COLIMA_BIN: ${captured_args}" >&2
    exit 1
    ;;
esac

case "${captured_args}" in
  *"--real-home=/fake/real-home"*) ;;
  *)
    echo "Expected --real-home to forward REAL_HOME: ${captured_args}" >&2
    exit 1
    ;;
esac

case "${captured_args}" in
  *"--colima-home=/fake/colima-home"*) ;;
  *)
    echo "Expected --colima-home to forward COLIMA_STATE_ROOT: ${captured_args}" >&2
    exit 1
    ;;
esac

case "${captured_args}" in
  *" -- delete --profile timeout-fixture"*) ;;
  *)
    echo "Expected '-- delete --profile timeout-fixture' payload after the flag separator: ${captured_args}" >&2
    exit 1
    ;;
esac
