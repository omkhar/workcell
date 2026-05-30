#!/usr/bin/env -S BASH_ENV= ENV= bash

# shellcheck source=runtime/container/assurance.sh
source /usr/local/libexec/workcell/assurance.sh

WORKCELL_RUNTIME_STATE_DIR="/run/workcell"
WORKCELL_RUNTIME_MUTABILITY_FILE="${WORKCELL_RUNTIME_STATE_DIR}/container-mutability"
WORKCELL_RUNTIME_MODE_FILE="${WORKCELL_RUNTIME_STATE_DIR}/mode"
WORKCELL_RUNTIME_PROFILE_FILE="${WORKCELL_RUNTIME_STATE_DIR}/profile"
WORKCELL_RUNTIME_AUTONOMY_FILE="${WORKCELL_RUNTIME_STATE_DIR}/autonomy"
WORKCELL_RUNTIME_ASSURANCE_FILE="${WORKCELL_RUNTIME_STATE_DIR}/session-assurance"
WORKCELL_APT_BROKER_ROOT="${WORKCELL_RUNTIME_STATE_DIR}/apt-broker"
WORKCELL_APT_BROKER_REQUESTS_DIR="${WORKCELL_APT_BROKER_ROOT}/requests"
WORKCELL_APT_BROKER_RESULTS_DIR="${WORKCELL_APT_BROKER_ROOT}/results"
WORKCELL_APT_BROKER_PID_FILE="${WORKCELL_APT_BROKER_ROOT}/pid"
WORKCELL_APT_BROKER_START_WAIT_SECONDS="${WORKCELL_APT_BROKER_START_WAIT_SECONDS:-5}"
WORKCELL_APT_BROKER_START_POLL_SECONDS="${WORKCELL_APT_BROKER_START_POLL_SECONDS:-0.1}"

workcell_runtime_user_die() {
  echo "$*" >&2
  exit 2
}

workcell_pid1_env_value() {
  local key="$1"

  case "${key}" in
    WORKCELL_CONTAINER_MUTABILITY | WORKCELL_MODE | CODEX_PROFILE | \
      WORKCELL_AGENT_AUTONOMY | WORKCELL_SESSION_ASSURANCE | \
      WORKCELL_HOST_UID | WORKCELL_HOST_GID | WORKCELL_HOST_USER) ;;
    *)
      echo "Workcell internal error: unexpected key for pid1 env lookup: ${key}" >&2
      return 1
      ;;
  esac
  [[ -r /proc/1/environ ]] || return 1
  tr '\0' '\n' </proc/1/environ | sed -n "s/^${key}=//p" | head -n1
}

workcell_unset_env_prefix() {
  local prefix="$1"
  local var_name=""

  while IFS= read -r var_name; do
    unset "${var_name}"
  done < <(compgen -A variable -- "${prefix}" || true)
}

workcell_sanitize_git_runtime_env() {
  unset GIT_DIR
  unset GIT_WORK_TREE
  unset GIT_COMMON_DIR
  unset GIT_EXEC_PATH
  unset GIT_OBJECT_DIRECTORY
  unset GIT_ALTERNATE_OBJECT_DIRECTORIES
  unset GIT_INDEX_FILE
  unset GIT_ASKPASS
  unset GIT_EDITOR
  unset GIT_EXTERNAL_DIFF
  unset GIT_PAGER
  unset GIT_SEQUENCE_EDITOR
  unset GIT_SSH
  unset GIT_SSH_COMMAND
  unset SSH_ASKPASS
  unset EDITOR
  unset PAGER
  unset VISUAL
  workcell_unset_env_prefix GIT_CONFIG_
}

workcell_runtime_mutability() {
  local value="${WORKCELL_CONTAINER_MUTABILITY:-}"

  if [[ -z "${value}" ]]; then
    value="$(workcell_pid1_env_value WORKCELL_CONTAINER_MUTABILITY || true)"
  fi
  if [[ -z "${value}" ]]; then
    value="ephemeral"
  fi

  case "${value}" in
    ephemeral | readonly)
      printf '%s\n' "${value}"
      ;;
    *)
      workcell_runtime_user_die "Unsupported Workcell container mutability mode: ${value}"
      ;;
  esac
}

workcell_runtime_identity_value() {
  local key="$1"
  local value="${!key:-}"

  if [[ -z "${value}" ]]; then
    value="$(workcell_pid1_env_value "${key}" || true)"
  fi

  printf '%s\n' "${value}"
}

workcell_runtime_host_uid() {
  printf '%s\n' "$(workcell_runtime_identity_value WORKCELL_HOST_UID)"
}

workcell_runtime_host_gid() {
  printf '%s\n' "$(workcell_runtime_identity_value WORKCELL_HOST_GID)"
}

workcell_runtime_host_user() {
  printf '%s\n' "$(workcell_runtime_identity_value WORKCELL_HOST_USER)"
}

workcell_runtime_require_numeric_id() {
  local value="$1"
  local label="$2"

  case "${value}" in
    '' | *[!0-9]*)
      workcell_runtime_user_die "Workcell requires a numeric ${label} for mutable runtime mode."
      ;;
  esac
}

workcell_runtime_user_name() {
  local uid="$1"
  local requested="$2"
  local existing=""

  existing="$(getent passwd "${uid}" | cut -d: -f1 || true)"
  if [[ -n "${existing}" ]]; then
    printf '%s\n' "${existing}"
    return 0
  fi

  requested="${requested//[^A-Za-z0-9_.-]/-}"
  requested="${requested#[.-]}"
  if [[ -z "${requested}" ]]; then
    requested="agent"
  fi

  if getent passwd "${requested}" >/dev/null 2>&1; then
    printf 'workcell-%s\n' "${uid}"
    return 0
  fi

  printf '%s\n' "${requested}"
}

workcell_runtime_group_name() {
  local gid="$1"
  local requested="$2"
  local existing=""

  existing="$(getent group "${gid}" | cut -d: -f1 || true)"
  if [[ -n "${existing}" ]]; then
    printf '%s\n' "${existing}"
    return 0
  fi

  requested="${requested//[^A-Za-z0-9_.-]/-}"
  requested="${requested#[.-]}"
  if [[ -z "${requested}" ]]; then
    requested="agent"
  fi

  if getent group "${requested}" >/dev/null 2>&1; then
    printf 'workcell-%s\n' "${gid}"
    return 0
  fi

  printf '%s\n' "${requested}"
}

workcell_append_group_entry() {
  local group_name="$1"
  local gid="$2"

  printf '%s:x:%s:\n' "${group_name}" "${gid}" >>/etc/group
}

workcell_append_passwd_entry() {
  local user_name="$1"
  local uid="$2"
  local gid="$3"

  printf '%s:x:%s:%s::%s:/bin/bash\n' "${user_name}" "${uid}" "${gid}" "${HOME}" >>/etc/passwd
}

workcell_append_shadow_entry() {
  local user_name="$1"

  if [[ -f /etc/shadow ]] && ! awk -F: -v wanted="${user_name}" '
    $1 == wanted {
      found = 1
      exit
    }
    END {
      exit found ? 0 : 1
    }
  ' /etc/shadow; then
    printf '%s::20000:0:99999:7:::\n' "${user_name}" >>/etc/shadow
  fi
}

workcell_should_reexec_as_runtime_user() {
  local uid=""

  [[ "$(id -u)" == "0" ]] || return 1
  [[ "$(workcell_runtime_mutability)" == "ephemeral" ]] || return 1
  uid="$(workcell_runtime_host_uid)"
  [[ -n "${uid}" ]] || return 1
  workcell_runtime_require_numeric_id "${uid}" "host uid"
  [[ "${uid}" != "0" ]]
}

workcell_prepare_runtime_identity() {
  local uid=""
  local gid=""
  local requested_user=""
  local user_name=""
  local group_name=""

  uid="$(workcell_runtime_host_uid)"
  gid="$(workcell_runtime_host_gid)"
  requested_user="$(workcell_runtime_host_user)"
  workcell_runtime_require_numeric_id "${uid}" "host uid"
  workcell_runtime_require_numeric_id "${gid}" "host gid"

  group_name="$(workcell_runtime_group_name "${gid}" "${requested_user}")"
  if ! getent group "${gid}" >/dev/null 2>&1; then
    workcell_append_group_entry "${group_name}" "${gid}"
  fi

  user_name="$(workcell_runtime_user_name "${uid}" "${requested_user}")"
  if ! getent passwd "${uid}" >/dev/null 2>&1; then
    workcell_append_passwd_entry "${user_name}" "${uid}" "${gid}"
  fi
  user_name="$(getent passwd "${uid}" | cut -d: -f1)"
  workcell_append_shadow_entry "${user_name}"

  mkdir -p /etc/sudoers.d
  local sudoers_tmp
  sudoers_tmp="$(mktemp /etc/sudoers.d/workcell-runtime-user.tmp.XXXXXX)"
  printf '%s ALL=(root) NOPASSWD: /usr/local/libexec/workcell/apt-helper.sh\n' "${user_name}" >"${sudoers_tmp}"
  chmod 0440 "${sudoers_tmp}"
  mv "${sudoers_tmp}" /etc/sudoers.d/workcell-runtime-user

  printf '%s\n' "${user_name}"
}

workcell_apt_broker_running() {
  local broker_pid=""
  local broker_cmdline=""

  [[ -f "${WORKCELL_APT_BROKER_PID_FILE}" ]] || return 1
  broker_pid="$(head -n1 "${WORKCELL_APT_BROKER_PID_FILE}" 2>/dev/null || true)"
  [[ "${broker_pid}" =~ ^[1-9][0-9]*$ ]] || return 1
  [[ -r "/proc/${broker_pid}/cmdline" ]] || return 1
  broker_cmdline="$(tr '\0' ' ' <"/proc/${broker_pid}/cmdline" 2>/dev/null || true)"
  [[ "${broker_cmdline}" == *"/usr/local/libexec/workcell/apt-broker.sh"* ]]
}

workcell_wait_for_apt_broker() {
  local deadline=0

  deadline=$((SECONDS + WORKCELL_APT_BROKER_START_WAIT_SECONDS))
  while ((SECONDS < deadline)); do
    if workcell_apt_broker_running; then
      return 0
    fi
    sleep "${WORKCELL_APT_BROKER_START_POLL_SECONDS}" || true
  done
  workcell_runtime_user_die "Workcell apt broker failed to start."
}

workcell_start_apt_broker() {
  mkdir -p \
    "${WORKCELL_APT_BROKER_ROOT}" \
    "${WORKCELL_APT_BROKER_REQUESTS_DIR}" \
    "${WORKCELL_APT_BROKER_RESULTS_DIR}"
  chmod 0755 "${WORKCELL_APT_BROKER_ROOT}" "${WORKCELL_APT_BROKER_RESULTS_DIR}"
  chmod 1733 "${WORKCELL_APT_BROKER_REQUESTS_DIR}"
  if workcell_apt_broker_running; then
    export WORKCELL_APT_BROKER_ROOT
    return 0
  fi
  WORKCELL_APT_BROKER_ROOT="${WORKCELL_APT_BROKER_ROOT}" /usr/bin/setsid -f \
    /bin/bash /usr/local/libexec/workcell/apt-broker.sh </dev/null >/dev/null 2>&1
  export WORKCELL_APT_BROKER_ROOT
  workcell_wait_for_apt_broker
}

workcell_write_readonly_state_file() {
  local path="$1"
  local value="$2"
  local tmp_path
  tmp_path="$(mktemp "${path}.tmp.XXXXXX")"

  mkdir -p "$(dirname "${path}")"
  printf '%s\n' "${value}" >"${tmp_path}"
  chmod 0444 "${tmp_path}"
  mv "${tmp_path}" "${path}"
}

workcell_write_runtime_state() {
  local mutability=""
  local mode=""
  local profile=""
  local autonomy=""
  local session_assurance=""
  local session_assurance_override=""

  mutability="$(workcell_runtime_mutability)"
  mode="${WORKCELL_MODE:-${CODEX_PROFILE:-strict}}"
  profile="${CODEX_PROFILE:-${mode}}"
  autonomy="${WORKCELL_AGENT_AUTONOMY:-yolo}"
  session_assurance_override="${WORKCELL_SESSION_ASSURANCE_INITIAL:-}"
  if [[ -n "${session_assurance_override}" ]]; then
    session_assurance="${session_assurance_override}"
  else
    session_assurance="$(workcell_container_assurance "${mutability}")"
  fi
  mkdir -p "${WORKCELL_RUNTIME_STATE_DIR}"
  chmod 0755 "${WORKCELL_RUNTIME_STATE_DIR}"
  workcell_write_readonly_state_file "${WORKCELL_RUNTIME_MUTABILITY_FILE}" "${mutability}"
  workcell_write_readonly_state_file "${WORKCELL_RUNTIME_MODE_FILE}" "${mode}"
  workcell_write_readonly_state_file "${WORKCELL_RUNTIME_PROFILE_FILE}" "${profile}"
  workcell_write_readonly_state_file "${WORKCELL_RUNTIME_AUTONOMY_FILE}" "${autonomy}"
  if [[ ! -e "${WORKCELL_RUNTIME_ASSURANCE_FILE}" ]]; then
    workcell_write_readonly_state_file "${WORKCELL_RUNTIME_ASSURANCE_FILE}" "${session_assurance}"
  fi
}

workcell_runtime_state_value() {
  local key="$1"
  local path=""

  case "${key}" in
    WORKCELL_CONTAINER_MUTABILITY)
      path="${WORKCELL_RUNTIME_MUTABILITY_FILE}"
      ;;
    WORKCELL_MODE)
      path="${WORKCELL_RUNTIME_MODE_FILE}"
      ;;
    CODEX_PROFILE)
      path="${WORKCELL_RUNTIME_PROFILE_FILE}"
      ;;
    WORKCELL_AGENT_AUTONOMY)
      path="${WORKCELL_RUNTIME_AUTONOMY_FILE}"
      ;;
    WORKCELL_SESSION_ASSURANCE)
      path="${WORKCELL_RUNTIME_ASSURANCE_FILE}"
      ;;
    *)
      return 1
      ;;
  esac

  [[ -r "${path}" ]] || return 1
  head -n1 "${path}"
}

workcell_reexec_as_runtime_user() {
  local command_path="${1:-}"
  shift || true
  local -a command_argv=()

  local uid=""
  local gid=""
  local user_name=""

  if [[ -z "${command_path}" ]]; then
    workcell_runtime_user_die "Workcell requires a command to re-exec as the mapped runtime user."
  fi

  uid="$(workcell_runtime_host_uid)"
  gid="$(workcell_runtime_host_gid)"
  user_name="$(workcell_prepare_runtime_identity)"
  workcell_write_runtime_state
  workcell_start_apt_broker
  export USER="${user_name}"
  export LOGNAME="${user_name}"

  if [[ -x "${command_path}" ]]; then
    command_argv=("${command_path}" "$@")
  elif [[ -f "${command_path}" && -r "${command_path}" ]]; then
    command_argv=(/bin/bash "${command_path}" "$@")
  else
    workcell_runtime_user_die "Workcell could not execute runtime command: ${command_path}"
  fi

  exec setpriv --reuid "${uid}" --regid "${gid}" --init-groups "${command_argv[@]}"
}
