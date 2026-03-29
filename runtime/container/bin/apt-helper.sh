#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

if [[ "$(id -u)" != "0" ]]; then
  echo "Workcell apt helper must run as root." >&2
  exit 2
fi

command_name="${1-}"
shift || true
WORKCELL_RUNTIME_STATE_DIR="/run/workcell"
WORKCELL_RUNTIME_ASSURANCE_FILE="${WORKCELL_RUNTIME_STATE_DIR}/session-assurance"
WORKCELL_PERSISTED_ASSURANCE_FILE="${WORKCELL_PERSISTED_ASSURANCE_FILE:-/var/lib/workcell/session-assurance}"

case "${command_name}" in
  apt | apt-get) ;;
  *)
    echo "Workcell blocked unsupported privileged helper command: ${command_name}" >&2
    exit 2
    ;;
esac

workcell_block_apt_arg() {
  local reason="$1"

  echo "Workcell blocked unsafe ${command_name} argument: ${reason}" >&2
  exit 2
}

workcell_validate_package_name() {
  local package_name="$1"

  case "${package_name}" in
    "" | -* | */* | file:* | http:* | https:* | *.deb)
      workcell_block_apt_arg "${package_name}"
      ;;
    *[!A-Za-z0-9+._:=~-]*)
      workcell_block_apt_arg "${package_name}"
      ;;
  esac
}

workcell_validate_apt_args() {
  local subcommand="${1-}"
  shift || true
  local arg=""

  if [[ "${subcommand}" == "--help" ]]; then
    return 0
  fi

  case "${subcommand}" in
    update | install | remove | purge | autoremove | clean) ;;
    *)
      workcell_block_apt_arg "${subcommand}"
      ;;
  esac

  for arg in "$@"; do
    case "${arg}" in
      -y | --yes | --assume-yes | --no-install-recommends | -q | -qq | --quiet | --help) ;;
      -o | --option | -c | --config-file | --option=* | --config-file=* | -f | --fix-broken | --allow-downgrades | --allow-remove-essential | --allow-change-held-packages | --allow-unauthenticated | --print-uris | --simulate | -s)
        workcell_block_apt_arg "${arg}"
        ;;
      -*)
        workcell_block_apt_arg "${arg}"
        ;;
      *)
        if [[ "${subcommand}" == "update" ]] || [[ "${subcommand}" == "clean" ]]; then
          workcell_block_apt_arg "${arg}"
        fi
        workcell_validate_package_name "${arg}"
        ;;
    esac
  done
}

workcell_mark_lower_assurance_session() {
  local persisted_dir=""

  mkdir -p "${WORKCELL_RUNTIME_STATE_DIR}"
  if [[ -e "${WORKCELL_RUNTIME_ASSURANCE_FILE}" ]]; then
    chmod u+w "${WORKCELL_RUNTIME_ASSURANCE_FILE}"
  fi
  printf '%s\n' "lower-assurance-package-mutation" >"${WORKCELL_RUNTIME_ASSURANCE_FILE}"
  chmod 0444 "${WORKCELL_RUNTIME_ASSURANCE_FILE}"
  persisted_dir="$(dirname "${WORKCELL_PERSISTED_ASSURANCE_FILE}")"
  mkdir -p "${persisted_dir}"
  if [[ -L "${WORKCELL_PERSISTED_ASSURANCE_FILE}" ]]; then
    echo "Workcell blocked unsafe persisted assurance symlink: ${WORKCELL_PERSISTED_ASSURANCE_FILE}" >&2
    exit 2
  fi
  if [[ -e "${WORKCELL_PERSISTED_ASSURANCE_FILE}" ]]; then
    chmod u+w "${WORKCELL_PERSISTED_ASSURANCE_FILE}" 2>/dev/null || true
  fi
  printf '%s\n' "lower-assurance-package-mutation" >"${WORKCELL_PERSISTED_ASSURANCE_FILE}"
  chmod 0444 "${WORKCELL_PERSISTED_ASSURANCE_FILE}" 2>/dev/null || true
}

mutability="${WORKCELL_CONTAINER_MUTABILITY:-}"
if [[ -z "${mutability}" ]] && [[ -r /run/workcell/container-mutability ]]; then
  mutability="$(head -n1 /run/workcell/container-mutability)"
fi
if [[ -z "${mutability}" ]] && [[ -r /proc/1/environ ]]; then
  mutability="$(tr '\0' '\n' </proc/1/environ | sed -n 's/^WORKCELL_CONTAINER_MUTABILITY=//p' | head -n1)"
fi
if [[ -z "${mutability}" ]]; then
  mutability="ephemeral"
fi

if [[ "${mutability}" != "ephemeral" ]]; then
  echo "Workcell blocked ${command_name}: readonly container mutability is active." >&2
  exit 2
fi

real_command="/usr/bin/${command_name}"
if [[ ! -x "${real_command}" ]]; then
  echo "Workcell could not find the real ${command_name} binary at ${real_command}." >&2
  exit 127
fi

workcell_validate_apt_args "$@"
subcommand="${1-}"
mutates_packages=0
case "${subcommand}" in
  install | remove | purge | autoremove)
    mutates_packages=1
    echo "Workcell warning: ${command_name} ${subcommand} runs package maintainer scripts as root and downgrades in-container control-plane assurance until this session exits." >&2
    ;;
esac

if ! "${real_command}" "$@"; then
  exit $?
fi

if [[ "${mutates_packages}" -eq 1 ]]; then
  workcell_mark_lower_assurance_session
  echo "WORKCELL_EVENT package-mutation assurance=lower-assurance-package-mutation" >&2
  echo "Workcell note: this session is now lower-assurance until the container exits." >&2
fi
