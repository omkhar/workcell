#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

stdin_path="${WORKCELL_DETACHED_STDIN_PATH:-/state/tmp/workcell/session-stdin}"
rm -f "${stdin_path}"
mkdir -p "$(dirname "${stdin_path}")"
chmod 0700 "$(dirname "${stdin_path}")"
mkfifo "${stdin_path}"
chmod 0600 "${stdin_path}"
exec 3<>"${stdin_path}"
child_pid=""
child_done=0
child_status=0

forward_container_tty_input() {
  while :; do
    cat /dev/tty >"${stdin_path}" 2>/dev/null || sleep 1
  done
}

forward_container_tty_input &
forwarder_pid=$!

cleanup() {
  kill "${forwarder_pid}" >/dev/null 2>&1 || true
  wait "${forwarder_pid}" >/dev/null 2>&1 || true
  rm -f "${stdin_path}"
}

forward_child_signal() {
  local signal="$1"

  if [[ -n "${child_pid}" ]] &&
    kill -0 "${child_pid}" >/dev/null 2>&1; then
    kill "-${signal}" "${child_pid}" >/dev/null 2>&1 ||
      kill "${child_pid}" >/dev/null 2>&1 || true
  fi
}

handle_signal() {
  local signal="$1"
  local status=0

  if [[ "${child_done}" == "1" ]]; then
    trap - EXIT INT TERM
    cleanup
    exit "${child_status}"
  fi
  forward_child_signal "${signal}"
  if [[ -n "${child_pid}" ]]; then
    set +e
    wait "${child_pid}" >/dev/null 2>&1
    status="$?"
    set -e
  else
    case "${signal}" in
      INT) status=130 ;;
      TERM) status=143 ;;
      *) status=128 ;;
    esac
  fi
  trap - EXIT INT TERM
  cleanup
  exit "${status}"
}

trap cleanup EXIT
trap 'handle_signal INT' INT
trap 'handle_signal TERM' TERM
"$@" <&3 &
child_pid="$!"
set +e
wait "${child_pid}"
status="$?"
child_status="${status}"
child_done=1
trap - EXIT INT TERM
set -e
cleanup
exit "${status}"
