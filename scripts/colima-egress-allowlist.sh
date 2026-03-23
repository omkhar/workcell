#!/usr/bin/env -S -i PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin BASH_ENV= ENV= /bin/bash
# shellcheck shell=bash
set -euo pipefail

readonly TRUSTED_HOST_PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
export PATH="${TRUSTED_HOST_PATH}"
scrub_host_process_env() {
  local env_name

  unset BASH_ENV ENV
  unset PYTHONPATH PYTHONHOME PYTHONSAFEPATH PYTHONSTARTUP
  unset RUBYOPT RUBYLIB GEM_HOME GEM_PATH
  unset PERL5OPT PERL5LIB PERLLIB PERL_MB_OPT PERL_MM_OPT
  unset LD_PRELOAD LD_LIBRARY_PATH LD_AUDIT LD_DEBUG LD_DEBUG_OUTPUT LD_BIND_NOW LD_ASSUME_KERNEL

  for env_name in $(compgen -v); do
    case "${env_name}" in
      DYLD_*)
        unset "${env_name}"
        ;;
    esac
  done
}

scrub_host_process_env

LIMACTL_BIN=""
PYTHON3_BIN=""
CANONICALIZER_PYTHON="/usr/bin/python3"

usage() {
  cat <<'EOF'
Usage:
  colima-egress-allowlist.sh apply <profile> "<host:port ...>"
  colima-egress-allowlist.sh clear <profile>
EOF
}

resolve_host_tool() {
  local name="$1"
  shift
  local candidate canonical_candidate

  for candidate in "$@"; do
    [[ -n "${candidate}" ]] || continue
    [[ -x "${candidate}" ]] || continue
    canonical_candidate="$(canonicalize_host_tool_path "${candidate}")"
    if is_trusted_host_tool_path "${candidate}" && is_trusted_host_tool_path "${canonical_candidate}"; then
      printf '%s\n' "${canonical_candidate}"
      return 0
    fi
  done

  echo "Missing required tool: ${name}" >&2
  exit 1
}

is_trusted_host_tool_path() {
  local candidate="$1"
  local trusted_prefixes=(
    /usr/bin
    /bin
    /usr/sbin
    /sbin
    /usr/local/bin
    /usr/local/Cellar
    /opt/homebrew/bin
    /opt/homebrew/Cellar
  )
  local prefix

  [[ "${candidate}" = /* ]] || return 1

  for prefix in "${trusted_prefixes[@]}"; do
    case "${candidate}" in
      "${prefix}" | "${prefix}"/*)
        return 0
        ;;
    esac
  done

  return 1
}

canonicalize_host_tool_path() {
  local candidate="$1"

  if [[ -z "${candidate}" ]]; then
    printf '\n'
    return 0
  fi

  if [[ -x "${CANONICALIZER_PYTHON}" ]]; then
    run_clean_host_command "${CANONICALIZER_PYTHON}" - "${candidate}" <<'PY'
import os
import sys

print(os.path.realpath(sys.argv[1]))
PY
    return 0
  fi

  printf '%s\n' "${candidate}"
}

run_clean_host_command() {
  local home="${REAL_HOME:-/}"

  env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME="${home}" \
    LC_ALL=C \
    LANG=C \
    "$@"
}

validate_endpoint() {
  local endpoint="$1"
  local host="${endpoint%:*}"
  local port="${endpoint##*:}"

  [[ -n "${host}" ]] || {
    echo "Invalid endpoint host: ${endpoint}" >&2
    exit 1
  }
  [[ "${host}" =~ ^[A-Za-z0-9.-]+$ ]] || {
    echo "Invalid endpoint host: ${endpoint}" >&2
    exit 1
  }
  [[ "${host}" != .* ]] || {
    echo "Invalid endpoint host: ${endpoint}" >&2
    exit 1
  }
  [[ "${host}" != *..* ]] || {
    echo "Invalid endpoint host: ${endpoint}" >&2
    exit 1
  }
  [[ "${port}" =~ ^[0-9]{1,5}$ ]] || {
    echo "Invalid endpoint port: ${endpoint}" >&2
    exit 1
  }
  if ((port < 1 || port > 65535)); then
    echo "Invalid endpoint port: ${endpoint}" >&2
    exit 1
  fi
}

resolve_ips() {
  local host="$1"
  run_clean_host_command "${PYTHON3_BIN}" - "$host" <<'PY'
import socket, sys
host = sys.argv[1]
seen = []
for family, _, _, _, sockaddr in socket.getaddrinfo(host, None, socket.AF_UNSPEC, socket.SOCK_STREAM):
    ip = sockaddr[0]
    if ip not in seen:
        seen.append(ip)
for ip in seen:
    print(ip)
PY
}

COMMAND="${1:-}"
PROFILE="${2:-}"

[[ -n "${COMMAND}" ]] || {
  usage
  exit 1
}

[[ -n "${PROFILE}" ]] || {
  usage
  exit 1
}

initialize_vm_tools() {
  if [[ -n "${LIMACTL_BIN}" && -n "${PYTHON3_BIN}" && -n "${REAL_HOME:-}" ]]; then
    return 0
  fi

  LIMACTL_BIN="$(resolve_host_tool limactl /opt/homebrew/bin/limactl /usr/local/bin/limactl)"
  PYTHON3_BIN="$(resolve_host_tool python3 /usr/bin/python3 /opt/homebrew/bin/python3 /usr/local/bin/python3)"
  REAL_HOME="$(
    run_clean_host_command "${PYTHON3_BIN}" - <<'PY'
import os
import pwd
print(pwd.getpwuid(os.getuid()).pw_dir)
PY
  )"
}

lima_instance() {
  if [[ "${PROFILE}" == "default" ]]; then
    printf 'colima\n'
  else
    printf 'colima-%s\n' "${PROFILE}"
  fi
}

run_in_vm() {
  local script="$1"
  local colima_home="${COLIMA_HOME:-${REAL_HOME}/.colima}"

  printf 'set -euo pipefail\n%s\n' "${script}" |
    HOME="${REAL_HOME}" \
      COLIMA_HOME="${colima_home}" \
      LIMA_HOME="${colima_home}/_lima" \
      LIMA_WORKDIR=/ \
      "${LIMACTL_BIN}" shell "$(lima_instance)" -- bash -s --
}

clear_rules() {
  initialize_vm_tools
  run_in_vm '
    set -euo pipefail
    sudo iptables -D DOCKER-USER -j WORKCELL_EGRESS 2>/dev/null || true
    sudo iptables -F WORKCELL_EGRESS 2>/dev/null || true
    sudo iptables -X WORKCELL_EGRESS 2>/dev/null || true
    if type ip6tables >/dev/null 2>&1; then
      sudo ip6tables -D DOCKER-USER -j WORKCELL_EGRESS6 2>/dev/null || true
      sudo ip6tables -F WORKCELL_EGRESS6 2>/dev/null || true
      sudo ip6tables -X WORKCELL_EGRESS6 2>/dev/null || true
    fi
  '
}

case "${COMMAND}" in
  clear)
    clear_rules
    ;;
  apply)
    ENDPOINTS="${3:-}"
    [[ -n "${ENDPOINTS}" ]] || {
      echo "Missing endpoint list for apply" >&2
      exit 1
    }

    RULES=("sudo iptables -N WORKCELL_EGRESS 2>/dev/null || true"
      "sudo iptables -F WORKCELL_EGRESS"
      "sudo iptables -C DOCKER-USER -j WORKCELL_EGRESS 2>/dev/null || sudo iptables -I DOCKER-USER 1 -j WORKCELL_EGRESS"
      "sudo iptables -A WORKCELL_EGRESS -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT")
    IPV6_RULES=()

    for endpoint in ${ENDPOINTS}; do
      validate_endpoint "${endpoint}"
    done

    initialize_vm_tools

    for endpoint in ${ENDPOINTS}; do
      host="${endpoint%:*}"
      port="${endpoint##*:}"
      while IFS= read -r ip; do
        [[ -n "${ip}" ]] || continue
        if [[ "${ip}" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then
          RULES+=("sudo iptables -A WORKCELL_EGRESS -p tcp -d ${ip} --dport ${port} -j ACCEPT")
          continue
        fi
        if [[ "${ip}" == *:* ]]; then
          IPV6_RULES+=("sudo ip6tables -A WORKCELL_EGRESS6 -p tcp -d ${ip} --dport ${port} -j ACCEPT")
          continue
        fi
        echo "Resolver returned invalid IP address: ${ip}" >&2
        exit 1
      done < <(resolve_ips "${host}")
    done

    RULES+=("sudo iptables -A WORKCELL_EGRESS -j DROP")
    if [[ "${#IPV6_RULES[@]}" -gt 0 ]]; then
      IPV6_RULES=("sudo ip6tables -N WORKCELL_EGRESS6 2>/dev/null || true"
        "sudo ip6tables -F WORKCELL_EGRESS6"
        "sudo ip6tables -C DOCKER-USER -j WORKCELL_EGRESS6 2>/dev/null || sudo ip6tables -I DOCKER-USER 1 -j WORKCELL_EGRESS6"
        "sudo ip6tables -A WORKCELL_EGRESS6 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT"
        "${IPV6_RULES[@]}"
        "sudo ip6tables -A WORKCELL_EGRESS6 -j DROP")
    fi

    clear_rules
    run_in_vm "$(
      cat <<EOF
$(printf '%s\n' "${RULES[@]}")
if type ip6tables >/dev/null 2>&1; then
  sudo sysctl -w net.ipv6.conf.all.disable_ipv6=0 >/dev/null 2>&1 || true
  sudo sysctl -w net.ipv6.conf.default.disable_ipv6=0 >/dev/null 2>&1 || true
$(if [[ "${#IPV6_RULES[@]}" -gt 0 ]]; then printf '%s\n' "${IPV6_RULES[@]}"; else
        printf '%s\n%s\n' "sudo ip6tables -N WORKCELL_EGRESS6 2>/dev/null || true" "sudo ip6tables -F WORKCELL_EGRESS6"
        printf '%s\n%s\n' "sudo ip6tables -C DOCKER-USER -j WORKCELL_EGRESS6 2>/dev/null || sudo ip6tables -I DOCKER-USER 1 -j WORKCELL_EGRESS6" "sudo ip6tables -A WORKCELL_EGRESS6 -j DROP"
      fi)
else
  sudo sysctl -w net.ipv6.conf.all.disable_ipv6=1 >/dev/null 2>&1 || true
  sudo sysctl -w net.ipv6.conf.default.disable_ipv6=1 >/dev/null 2>&1 || true
fi
EOF
    )"
    ;;
  *)
    usage
    exit 1
    ;;
esac
