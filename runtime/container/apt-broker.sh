#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

WORKCELL_APT_BROKER_ROOT="${WORKCELL_APT_BROKER_ROOT:-/run/workcell/apt-broker}"
WORKCELL_APT_BROKER_REQUESTS_DIR="${WORKCELL_APT_BROKER_ROOT}/requests"
WORKCELL_APT_BROKER_RESULTS_DIR="${WORKCELL_APT_BROKER_ROOT}/results"
WORKCELL_APT_BROKER_PID_FILE="${WORKCELL_APT_BROKER_ROOT}/pid"
WORKCELL_APT_BROKER_SLEEP_SECONDS="${WORKCELL_APT_BROKER_SLEEP_SECONDS:-0.1}"
# Tests may override the helper path when they launch the broker directly.
# The production runtime-user path does not propagate this variable.
WORKCELL_APT_HELPER="${WORKCELL_APT_HELPER:-/usr/local/libexec/workcell/apt-helper.sh}"
WORKCELL_APT_BROKER_PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

apt_broker_allowed_env_name() {
  local name="$1"

  case "${name}" in
    APT_LISTCHANGES_FRONTEND | DEBCONF_NONINTERACTIVE_SEEN | DEBIAN_FRONTEND) return 0 ;;
  esac
  return 1
}

apt_broker_cancel_status() {
  local reason="$1"

  case "${reason}" in
    INT) printf '130\n' ;;
    TERM) printf '143\n' ;;
    TIMEOUT) printf '124\n' ;;
    *) printf '1\n' ;;
  esac
}

apt_broker_process_request() {
  local request_dir="$1"
  local request_id=""
  local args_file=""
  local env_file=""
  local ready_file=""
  local cancel_path=""
  local claim_path=""
  local stdout_path=""
  local stderr_path=""
  local status_path=""
  local stdout_tmp=""
  local stderr_tmp=""
  local status_tmp=""
  local -a command_argv=()
  local -a broker_env=()
  local status=0
  local helper_pid=0
  local arg=""
  local env_name=""
  local env_value=""
  local cancel_reason=""
  local setsid_bin=""
  local -a helper_command=()

  [[ -d "${request_dir}" ]] || return 0
  [[ ! -L "${request_dir}" ]] || return 0
  request_id="$(basename "${request_dir}")"
  case "${request_id}" in
    request.*) ;;
    *) return 0 ;;
  esac

  args_file="${request_dir}/args"
  env_file="${request_dir}/env"
  ready_file="${request_dir}/ready"
  cancel_path="${request_dir}/cancel"
  claim_path="${WORKCELL_APT_BROKER_RESULTS_DIR}/${request_id}.claim"
  stdout_path="${WORKCELL_APT_BROKER_RESULTS_DIR}/${request_id}.stdout"
  stderr_path="${WORKCELL_APT_BROKER_RESULTS_DIR}/${request_id}.stderr"
  status_path="${WORKCELL_APT_BROKER_RESULTS_DIR}/${request_id}.status"

  [[ -f "${ready_file}" ]] || return 0
  [[ ! -L "${ready_file}" ]] || return 0
  [[ -f "${args_file}" ]] || return 0
  [[ ! -L "${args_file}" ]] || return 0
  [[ ! -e "${claim_path}" ]] || return 0
  [[ ! -e "${status_path}" ]] || return 0

  while IFS= read -r -d '' arg; do
    command_argv+=("${arg}")
  done <"${args_file}"
  ((${#command_argv[@]} > 0)) || return 0

  stdout_tmp="$(mktemp "${WORKCELL_APT_BROKER_RESULTS_DIR}/${request_id}.stdout.tmp.XXXXXX")"
  stderr_tmp="$(mktemp "${WORKCELL_APT_BROKER_RESULTS_DIR}/${request_id}.stderr.tmp.XXXXXX")"
  status_tmp="$(mktemp "${WORKCELL_APT_BROKER_RESULTS_DIR}/${request_id}.status.tmp.XXXXXX")"
  : >"${claim_path}"
  chmod 0644 "${claim_path}"

  if [[ -f "${cancel_path}" ]] && [[ ! -L "${cancel_path}" ]]; then
    cancel_reason="$(head -n1 "${cancel_path}" 2>/dev/null || true)"
    : >"${stdout_tmp}"
    printf 'Workcell cancelled privileged package request.\n' >"${stderr_tmp}"
    status="$(apt_broker_cancel_status "${cancel_reason}")"
    printf '%s\n' "${status}" >"${status_tmp}"
    chmod 0644 "${stdout_tmp}" "${stderr_tmp}" "${status_tmp}"
    mv -f "${stdout_tmp}" "${stdout_path}"
    mv -f "${stderr_tmp}" "${stderr_path}"
    mv -f "${status_tmp}" "${status_path}"
    return 0
  fi

  broker_env=(
    "HOME=/root"
    "PATH=${WORKCELL_APT_BROKER_PATH}"
  )
  if [[ -f "${env_file}" ]] && [[ ! -L "${env_file}" ]]; then
    while IFS='=' read -r env_name env_value; do
      [[ -n "${env_name}" ]] || continue
      if ! apt_broker_allowed_env_name "${env_name}"; then
        printf 'Workcell blocked unsupported preserved environment variable: %s\n' "${env_name}" >"${stderr_tmp}"
        printf '%s\n' "2" >"${status_tmp}"
        chmod 0644 "${stderr_tmp}" "${status_tmp}"
        : >"${stdout_tmp}"
        chmod 0644 "${stdout_tmp}"
        mv -f "${stdout_tmp}" "${stdout_path}"
        mv -f "${stderr_tmp}" "${stderr_path}"
        mv -f "${status_tmp}" "${status_path}"
        return 0
      fi
      broker_env+=("${env_name}=${env_value}")
    done <"${env_file}"
  fi

  helper_command=(/usr/bin/env -i "${broker_env[@]}" "${WORKCELL_APT_HELPER}" "${command_argv[@]}")
  setsid_bin="$(command -v setsid 2>/dev/null || true)"
  set +e
  if [[ -n "${setsid_bin}" ]]; then
    "${setsid_bin}" "${helper_command[@]}" >"${stdout_tmp}" 2>"${stderr_tmp}" &
  else
    "${helper_command[@]}" >"${stdout_tmp}" 2>"${stderr_tmp}" &
  fi
  helper_pid="$!"
  while kill -0 "${helper_pid}" >/dev/null 2>&1; do
    if [[ -f "${cancel_path}" ]] && [[ ! -L "${cancel_path}" ]]; then
      cancel_reason="$(head -n1 "${cancel_path}" 2>/dev/null || true)"
      kill -TERM "-${helper_pid}" >/dev/null 2>&1 ||
        kill "${helper_pid}" >/dev/null 2>&1 || true
      wait "${helper_pid}" >/dev/null 2>&1 || true
      printf 'Workcell cancelled privileged package request.\n' >>"${stderr_tmp}"
      status="$(apt_broker_cancel_status "${cancel_reason}")"
      break
    fi
    sleep "${WORKCELL_APT_BROKER_SLEEP_SECONDS}" || true
  done
  if [[ -z "${cancel_reason}" ]]; then
    wait "${helper_pid}"
    status="$?"
  fi
  set -e

  printf '%s\n' "${status}" >"${status_tmp}"
  chmod 0644 "${stdout_tmp}" "${stderr_tmp}" "${status_tmp}"
  mv -f "${stdout_tmp}" "${stdout_path}"
  mv -f "${stderr_tmp}" "${stderr_path}"
  mv -f "${status_tmp}" "${status_path}"
}

mkdir -p "${WORKCELL_APT_BROKER_REQUESTS_DIR}" "${WORKCELL_APT_BROKER_RESULTS_DIR}"
printf '%s\n' "$$" >"${WORKCELL_APT_BROKER_PID_FILE}"
chmod 0644 "${WORKCELL_APT_BROKER_PID_FILE}"

shopt -s nullglob
while :; do
  for request_dir in "${WORKCELL_APT_BROKER_REQUESTS_DIR}"/request.*; do
    apt_broker_process_request "${request_dir}"
  done
  sleep "${WORKCELL_APT_BROKER_SLEEP_SECONDS}" || true
done
