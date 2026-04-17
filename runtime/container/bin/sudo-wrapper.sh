#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

helper_command="/usr/local/libexec/workcell/apt-helper.sh"
broker_root="${WORKCELL_APT_BROKER_ROOT:-/run/workcell/apt-broker}"
broker_requests_dir="${broker_root}/requests"
broker_results_dir="${broker_root}/results"
broker_pid_file="${broker_root}/pid"
broker_wait_interval_seconds="${WORKCELL_APT_BROKER_WAIT_INTERVAL_SECONDS:-0.1}"
broker_wait_timeout_seconds="${WORKCELL_APT_BROKER_WAIT_TIMEOUT_SECONDS:-10}"
preserve_env_csv=""
sudo_wrapper_active_request_dir=""
sudo_wrapper_active_stdout_path=""
sudo_wrapper_active_stderr_path=""

sudo_wrapper_allowed_preserve_env_name() {
  local name="$1"

  case "${name}" in
    APT_LISTCHANGES_FRONTEND | DEBCONF_NONINTERACTIVE_SEEN | DEBIAN_FRONTEND) return 0 ;;
  esac
  return 1
}

sudo_wrapper_parse_env_names() {
  local csv="$1"
  local old_ifs="${IFS}"
  local name=""

  IFS=,
  for name in ${csv}; do
    [[ -n "${name}" ]] || continue
    printf '%s\n' "${name}"
  done
  IFS="${old_ifs}"
}

sudo_wrapper_broker_available() {
  local broker_pid=""
  local broker_cmdline=""

  [[ -d "${broker_requests_dir}" ]] || return 1
  [[ -d "${broker_results_dir}" ]] || return 1
  [[ -f "${broker_pid_file}" ]] || return 1
  broker_pid="$(head -n1 "${broker_pid_file}" 2>/dev/null || true)"
  [[ "${broker_pid}" =~ ^[1-9][0-9]*$ ]] || return 1
  [[ -r "/proc/${broker_pid}/cmdline" ]] || return 1
  broker_cmdline="$(tr '\0' ' ' <"/proc/${broker_pid}/cmdline" 2>/dev/null || true)"
  [[ "${broker_cmdline}" == *"/usr/local/libexec/workcell/apt-broker.sh"* ]]
}

sudo_wrapper_request_cancel() {
  local reason="$1"
  local cancel_path=""

  [[ -n "${sudo_wrapper_active_request_dir}" ]] || return 0
  [[ -d "${sudo_wrapper_active_request_dir}" ]] || return 0
  [[ ! -L "${sudo_wrapper_active_request_dir}" ]] || return 0
  cancel_path="${sudo_wrapper_active_request_dir}/cancel"
  printf '%s\n' "${reason}" >"${cancel_path}" 2>/dev/null || true
  chmod 0644 "${cancel_path}" 2>/dev/null || true
}

sudo_wrapper_request_cleanup() {
  local request_dir="$1"
  local stdout_path="$2"
  local stderr_path="$3"

  rm -rf "${request_dir}" >/dev/null 2>&1 || true
  rm -f "${stdout_path}" "${stderr_path}" >/dev/null 2>&1 || true
}

sudo_wrapper_request_signal_exit() {
  local reason="$1"
  local signal_status="$2"

  sudo_wrapper_request_cancel "${reason}"
  trap - INT TERM
  exit "${signal_status}"
}

sudo_wrapper_run_via_broker() {
  local request_dir=""
  local request_id=""
  local stdout_path=""
  local stderr_path=""
  local status_path=""
  local env_name=""
  local status=""
  local deadline=0

  request_dir="$(mktemp -d "${broker_requests_dir}/request.XXXXXX")"
  request_id="$(basename "${request_dir}")"
  stdout_path="${broker_results_dir}/${request_id}.stdout"
  stderr_path="${broker_results_dir}/${request_id}.stderr"
  status_path="${broker_results_dir}/${request_id}.status"
  sudo_wrapper_active_request_dir="${request_dir}"
  sudo_wrapper_active_stdout_path="${stdout_path}"
  sudo_wrapper_active_stderr_path="${stderr_path}"
  chmod 0711 "${request_dir}"
  trap 'sudo_wrapper_request_signal_exit INT 130' INT
  trap 'sudo_wrapper_request_signal_exit TERM 143' TERM

  printf '%s\0' "$@" >"${request_dir}/args"
  chmod 0644 "${request_dir}/args"
  if [[ -n "${preserve_env_csv}" ]]; then
    while IFS= read -r env_name; do
      [[ -n "${env_name}" ]] || continue
      if ! sudo_wrapper_allowed_preserve_env_name "${env_name}"; then
        sudo_wrapper_request_cleanup "${request_dir}" "${stdout_path}" "${stderr_path}"
        trap - INT TERM
        echo "Workcell blocked unsupported preserved environment variable: ${env_name}" >&2
        return 2
      fi
      if [[ -v "${env_name}" ]]; then
        printf '%s=%s\n' "${env_name}" "${!env_name}" >>"${request_dir}/env"
      fi
    done < <(sudo_wrapper_parse_env_names "${preserve_env_csv}")
    [[ ! -f "${request_dir}/env" ]] || chmod 0644 "${request_dir}/env"
  fi
  : >"${request_dir}/ready"
  chmod 0644 "${request_dir}/ready"

  deadline=$((SECONDS + broker_wait_timeout_seconds))
  while [[ ! -f "${status_path}" ]]; do
    if ! sudo_wrapper_broker_available; then
      sudo_wrapper_request_cleanup "${request_dir}" "${stdout_path}" "${stderr_path}"
      trap - INT TERM
      echo "Workcell apt broker is unavailable." >&2
      return 1
    fi
    if ((SECONDS >= deadline)); then
      sudo_wrapper_request_cancel TIMEOUT
      trap - INT TERM
      echo "Workcell apt broker timed out." >&2
      return 1
    fi
    sleep "${broker_wait_interval_seconds}"
  done

  if [[ -f "${stdout_path}" ]]; then
    cat "${stdout_path}"
  fi
  if [[ -f "${stderr_path}" ]]; then
    cat "${stderr_path}" >&2
  fi
  status="$(head -n1 "${status_path}")"
  sudo_wrapper_request_cleanup "${request_dir}" "${stdout_path}" "${stderr_path}"
  trap - INT TERM
  [[ "${status}" =~ ^[0-9]+$ ]] || status=1
  return "${status}"
}

if [[ "$(id -u)" == "0" ]]; then
  args=("$@")
  while ((${#args[@]} > 0)); do
    case "${args[0]}" in
      -n)
        args=("${args[@]:1}")
        ;;
      --preserve-env)
        args=("${args[@]:2}")
        ;;
      --preserve-env=*)
        args=("${args[@]:1}")
        ;;
      --)
        args=("${args[@]:1}")
        break
        ;;
      -*)
        break
        ;;
      *)
        break
        ;;
    esac
  done
  if ((${#args[@]} == 0)); then
    echo "Workcell blocked sudo without an explicit command." >&2
    exit 2
  fi
  exec "${args[@]}"
fi

original_args=("$@")
while (($# > 0)); do
  case "$1" in
    -n)
      shift
      ;;
    --preserve-env)
      preserve_env_csv="${2-}"
      shift 2
      ;;
    --preserve-env=*)
      preserve_env_csv="${1#*=}"
      shift
      ;;
    --)
      shift
      break
      ;;
    -*)
      echo "Workcell blocked unsupported sudo invocation." >&2
      exit 1
      ;;
    *)
      break
      ;;
  esac
done

command_path="${1-}"
if [[ -z "${command_path}" ]]; then
  echo "Workcell blocked sudo without an explicit command." >&2
  exit 2
fi
shift || true

if [[ "${command_path}" == "${helper_command}" ]] && sudo_wrapper_broker_available; then
  sudo_wrapper_run_via_broker "$@"
  exit "$?"
fi

if [[ "${command_path}" == "${helper_command}" ]]; then
  echo "Workcell apt broker is unavailable." >&2
  exit 1
fi

echo "Workcell blocked unsupported sudo invocation." >&2
exit 1
