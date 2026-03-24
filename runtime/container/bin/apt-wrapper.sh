#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

command_name="${0##*/}"
real_command="/usr/bin/${command_name}"
helper_command="/usr/local/libexec/workcell/apt-helper.sh"
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

if [[ ! -x "${real_command}" ]]; then
  echo "Workcell could not find the real ${command_name} binary at ${real_command}." >&2
  exit 127
fi

if [[ "$(id -u)" == "0" ]]; then
  exec "${real_command}" "$@"
fi

if [[ "${mutability}" != "ephemeral" ]]; then
  echo "Workcell blocked ${command_name}: readonly container mutability is active." >&2
  echo "Relaunch with --container-mutability ephemeral to allow ephemeral package-manager writes." >&2
  exit 2
fi

if [[ ! -x "${helper_command}" ]]; then
  echo "Workcell could not find the privileged apt helper at ${helper_command}." >&2
  exit 127
fi

exec sudo -n --preserve-env=DEBIAN_FRONTEND,DEBCONF_NONINTERACTIVE_SEEN,APT_LISTCHANGES_FRONTEND "${helper_command}" "${command_name}" "$@"
