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
container_tty_state=""
container_tty_configured=0

restore_container_tty() {
  if [[ "${container_tty_configured}" == "1" ]] &&
    [[ -n "${container_tty_state}" ]]; then
    /usr/bin/stty "${container_tty_state}" </dev/tty >/dev/null 2>&1 || true
  fi
  container_tty_configured=0
  container_tty_state=""
}

configure_container_tty() {
  container_tty_state="$(/usr/bin/stty -g </dev/tty 2>/dev/null || true)"
  [[ -n "${container_tty_state}" ]] || return 1
  /usr/bin/stty raw -echo </dev/tty
  container_tty_configured=1
}

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
  restore_container_tty
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

first_child_of() {
  local parent_pid="$1"
  local candidate_pid=""

  while read -r candidate_pid; do
    if [[ "${candidate_pid}" =~ ^[1-9][0-9]*$ ]]; then
      printf '%s\n' "${candidate_pid}"
      return 0
    fi
  done < <(/usr/bin/ps -o pid= --ppid "${parent_pid}" 2>/dev/null)
  return 1
}

discover_provider() {
  local attempt=0
  local pty_shell_pid=""
  local candidate_provider_pid=""

  for ((attempt = 0; attempt < 50; attempt++)); do
    pty_shell_pid="$(first_child_of "${child_pid}" || true)"
    if [[ -n "${pty_shell_pid}" ]]; then
      candidate_provider_pid="$(first_child_of "${pty_shell_pid}" || true)"
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

sync_terminal_size() {
  local rows=""
  local columns=""

  [[ -n "${provider_pid}" ]] || return 0
  kill -0 "${provider_pid}" >/dev/null 2>&1 || return 0
  read -r rows columns < <(/usr/bin/stty size </dev/tty 2>/dev/null || true)
  if [[ "${rows}" =~ ^[1-9][0-9]*$ ]] &&
    [[ "${columns}" =~ ^[1-9][0-9]*$ ]]; then
    /usr/bin/stty rows "${rows}" cols "${columns}" \
      <"/proc/${provider_pid}/fd/0" >/dev/null 2>&1 || true
  fi
}

handle_terminal_resize() {
  # Docker Desktop can emit a burst of SIGWINCH events while the container
  # terminal settles. Let the burst coalesce before copying its final size to
  # the provider PTY so a signal handled mid-burst cannot leave it stale.
  /usr/bin/sleep 0.1
  sync_terminal_size
}

wait_for_child() {
  local status=0

  while :; do
    set +e
    wait "${child_pid}"
    status="$?"
    set -e
    if kill -0 "${child_pid}" >/dev/null 2>&1; then
      continue
    fi
    child_status="${status}"
    child_done=1
    return 0
  done
}

handle_signal() {
  local signal="$1"
  local status=0

  if [[ "${child_done}" == "1" ]]; then
    trap - EXIT INT TERM WINCH
    cleanup
    exit "${child_status}"
  fi
  forward_child_signal "${signal}"
  if [[ -n "${child_pid}" ]]; then
    wait_for_child >/dev/null 2>&1
    status="${child_status}"
  else
    case "${signal}" in
      INT) status=130 ;;
      TERM) status=143 ;;
      *) status=128 ;;
    esac
  fi
  trap - EXIT INT TERM WINCH
  cleanup
  exit "${status}"
}

trap cleanup EXIT
trap 'handle_signal INT' INT
trap 'handle_signal TERM' TERM
if ! configure_container_tty; then
  echo "Workcell could not configure the detached container terminal." >&2
  exit 1
fi
/usr/bin/script -qefc "${exec_path}" /dev/null <&3 &
child_pid="$!"
if ! discover_provider && kill -0 "${child_pid}" >/dev/null 2>&1; then
  echo "Workcell could not identify the detached provider process." >&2
  kill "${child_pid}" >/dev/null 2>&1 || true
  wait "${child_pid}" >/dev/null 2>&1 || true
  exit 1
fi
trap handle_terminal_resize WINCH
sync_terminal_size
wait_for_child
status="${child_status}"
trap - EXIT INT TERM WINCH
cleanup
exit "${status}"
