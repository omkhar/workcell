#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

stdin_path="${WORKCELL_DETACHED_STDIN_PATH:-/state/tmp/workcell/session-stdin}"
rm -f "${stdin_path}"
mkdir -p "$(dirname "${stdin_path}")"
chmod 0700 "$(dirname "${stdin_path}")"
mkfifo "${stdin_path}"
chmod 0600 "${stdin_path}"
exec 3<>"${stdin_path}"
exec_path="$(mktemp "$(dirname "${stdin_path}")/session-exec.XXXXXX")"
{
  printf '#!/usr/bin/env bash\nexec'
  printf ' %q' "$@"
  printf '\n'
} >"${exec_path}"
chmod 0700 "${exec_path}"
child_pid=""
child_done=0
child_status=0
provider_pid=""

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
  rm -f "${stdin_path}" "${exec_path}"
}

forward_child_signal() {
  local signal="$1"

  if [[ -z "${provider_pid}" ]] &&
    [[ -n "${child_pid}" ]] &&
    kill -0 "${child_pid}" >/dev/null 2>&1; then
    discover_provider || true
  fi
  if [[ -n "${provider_pid}" ]] &&
    kill -0 "${provider_pid}" >/dev/null 2>&1; then
    kill "-${signal}" "${provider_pid}" >/dev/null 2>&1 || true
    return
  fi
  if [[ -n "${child_pid}" ]] &&
    kill -0 "${child_pid}" >/dev/null 2>&1; then
    kill "-${signal}" "${child_pid}" >/dev/null 2>&1 || true
  fi
}

discover_provider() {
  local attempt=0
  local pty_shell_pid=""
  local candidate_provider_pid=""

  for ((attempt = 0; attempt < 50; attempt++)); do
    pty_shell_pid=""
    if [[ -r "/proc/${child_pid}/task/${child_pid}/children" ]]; then
      read -r pty_shell_pid _ <"/proc/${child_pid}/task/${child_pid}/children" || true
    fi
    if [[ "${pty_shell_pid}" =~ ^[1-9][0-9]*$ ]] &&
      [[ -r "/proc/${pty_shell_pid}/task/${pty_shell_pid}/children" ]]; then
      candidate_provider_pid=""
      read -r candidate_provider_pid _ <"/proc/${pty_shell_pid}/task/${pty_shell_pid}/children" || true
      if [[ "${candidate_provider_pid}" =~ ^[1-9][0-9]*$ ]] &&
        kill -0 "${candidate_provider_pid}" >/dev/null 2>&1; then
        provider_pid="${candidate_provider_pid}"
        return 0
      fi
    fi
    kill -0 "${child_pid}" >/dev/null 2>&1 || return 1
    sleep 0.02
  done
  return 1
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
/usr/bin/script -qefc "${exec_path}" /dev/null <&3 &
child_pid="$!"
if ! discover_provider && kill -0 "${child_pid}" >/dev/null 2>&1; then
  echo "Workcell could not identify the detached provider process." >&2
  kill "${child_pid}" >/dev/null 2>&1 || true
  wait "${child_pid}" >/dev/null 2>&1 || true
  exit 1
fi
set +e
wait "${child_pid}"
status="$?"
child_status="${status}"
child_done=1
trap - EXIT INT TERM
set -e
cleanup
exit "${status}"
