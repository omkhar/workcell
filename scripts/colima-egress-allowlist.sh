#!/usr/bin/env -S -i PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin BASH_ENV= ENV= /bin/bash
# shellcheck shell=bash
set -euo pipefail

readonly TRUSTED_HOST_PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
export PATH="${TRUSTED_HOST_PATH}"
scrub_host_process_env() {
  local env_name

  unset BASH_ENV ENV
  unset WORKCELL_TEST_RUN_IN_VM_CAPTURE_DIR
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

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly ROOT_DIR
# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/go-run-env.sh"
GO_BIN="${WORKCELL_GO_BIN:-}"
LIMACTL_BIN=""
TEST_RUN_IN_VM_CAPTURE_DIR=""

usage() {
  cat <<'EOF'
Usage:
  colima-egress-allowlist.sh plan <profile> "<host:port ...>"
  colima-egress-allowlist.sh apply <profile> "<host:port ...>"
  colima-egress-allowlist.sh clear <profile>
EOF
}

resolve_go_bin() {
  local candidate=""

  if [[ -n "${GO_BIN}" && -x "${GO_BIN}" ]]; then
    case "${GO_BIN}" in
      /opt/homebrew/bin/go | /usr/local/go/bin/go | /usr/local/bin/go | /usr/bin/go)
        return 0
        ;;
      *)
        echo "Untrusted go binary path: ${GO_BIN}" >&2
        exit 1
        ;;
    esac
  fi
  for candidate in \
    /opt/homebrew/bin/go \
    /usr/local/go/bin/go \
    /usr/local/bin/go \
    /usr/bin/go; do
    if [[ -x "${candidate}" ]]; then
      GO_BIN="${candidate}"
      return 0
    fi
  done
  echo "Missing required tool: go" >&2
  exit 1
}

run_clean_repo_command() {
  local home="${REAL_HOME:-}"

  [[ "$#" -gt 0 ]] || return 0
  if [[ -z "${home}" ]]; then
    home="$(resolve_workcell_real_home 2>/dev/null || true)"
  fi
  if [[ ! -d "${home}" ]]; then
    home="/"
  fi

  (
    cd "${ROOT_DIR}" &&
      env -i \
        PATH="${TRUSTED_HOST_PATH}" \
        HOME="${home}" \
        LC_ALL=C \
        LANG=C \
        "$@"
  )
}

go_runtimeutil() {
  resolve_go_bin
  ensure_go_run_env
  run_clean_repo_command env \
    GOPATH="${GOPATH}" \
    GOMODCACHE="${GOMODCACHE}" \
    GOCACHE="${GOCACHE}" \
    "${GO_BIN}" run ./cmd/workcell-runtimeutil "$@"
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

  go_runtimeutil canonicalize-path "${candidate}"
}

run_clean_host_command() {
  local home="${REAL_HOME:-}"

  [[ "$#" -gt 0 ]] || return 0
  if [[ -z "${home}" ]]; then
    home="$(resolve_workcell_real_home 2>/dev/null || true)"
  fi
  if [[ ! -d "${home}" ]]; then
    home="/"
  fi

  (
    cd "${home}" &&
      env -i \
        PATH="${TRUSTED_HOST_PATH}" \
        HOME="${home}" \
        LC_ALL=C \
        LANG=C \
        "$@"
  )
}

validate_endpoint() {
  local endpoint="$1"
  local host=""
  local port=""

  if [[ "${endpoint}" =~ ^\[([0-9A-Fa-f:.]+)\]:([0-9]{1,5})$ ]]; then
    host="[${BASH_REMATCH[1]}]"
    port="${BASH_REMATCH[2]}"
  elif [[ "${endpoint}" =~ ^([^:]+):([0-9]{1,5})$ ]]; then
    host="${BASH_REMATCH[1]}"
    port="${BASH_REMATCH[2]}"
  fi

  [[ -n "${host}" ]] || {
    echo "Invalid endpoint host: ${endpoint}" >&2
    exit 1
  }
  if [[ "${host}" == \[*\] ]]; then
    local ipv6_host="${host:1:${#host}-2}"
    [[ "${ipv6_host}" =~ ^[0-9A-Fa-f:.]+$ ]] || {
      echo "Invalid endpoint host: ${endpoint}" >&2
      exit 1
    }
  else
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
  fi
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
  go_runtimeutil resolve-ips "${host}"
}

COMMAND="${1:-}"
if [[ "${COMMAND}" == "--test-run-in-vm-capture-dir" ]]; then
  TEST_RUN_IN_VM_CAPTURE_DIR="${2:-}"
  [[ -n "${TEST_RUN_IN_VM_CAPTURE_DIR}" ]] || {
    echo "Missing capture directory for --test-run-in-vm-capture-dir" >&2
    exit 1
  }
  shift 2
  COMMAND="${1:-}"
fi
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
  if [[ -z "${REAL_HOME:-}" ]]; then
    REAL_HOME="$(resolve_workcell_real_home)"
  fi
  if [[ -n "${LIMACTL_BIN}" ]]; then
    return 0
  fi

  LIMACTL_BIN="$(resolve_host_tool limactl /opt/homebrew/bin/limactl /usr/local/bin/limactl)"
}

initialize_host_tools() {
  if [[ -z "${REAL_HOME:-}" ]]; then
    REAL_HOME="$(resolve_workcell_real_home)"
  fi
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
  local colima_home=""
  local capture_base=""

  if [[ -n "${TEST_RUN_IN_VM_CAPTURE_DIR}" ]]; then
    initialize_host_tools
    colima_home="${COLIMA_HOME:-${REAL_HOME}/.colima}"
    mkdir -p "${TEST_RUN_IN_VM_CAPTURE_DIR}"
    capture_base="${TEST_RUN_IN_VM_CAPTURE_DIR}/${COMMAND}-${PROFILE}"
    printf '%s\n' "${script}" >"${capture_base}.script"
    cat >"${capture_base}.env" <<EOF
REAL_HOME=${REAL_HOME}
COLIMA_HOME=${colima_home}
LIMA_HOME=${colima_home}/_lima
LIMA_WORKDIR=/
EOF
    return 0
  fi
  initialize_vm_tools
  colima_home="${COLIMA_HOME:-${REAL_HOME}/.colima}"
  (
    cd / &&
      printf 'set -euo pipefail\n%s\n' "${script}" |
      HOME="${REAL_HOME}" \
        COLIMA_HOME="${colima_home}" \
        LIMA_HOME="${colima_home}/_lima" \
        LIMA_WORKDIR=/ \
        "${LIMACTL_BIN}" shell "$(lima_instance)" -- bash -s --
  )
}

clear_rules() {
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

render_clear_plan() {
  cat <<'EOF'
sudo iptables -D DOCKER-USER -j WORKCELL_EGRESS 2>/dev/null || true
sudo iptables -F WORKCELL_EGRESS 2>/dev/null || true
sudo iptables -X WORKCELL_EGRESS 2>/dev/null || true
if type ip6tables >/dev/null 2>&1; then
  sudo ip6tables -D DOCKER-USER -j WORKCELL_EGRESS6 2>/dev/null || true
  sudo ip6tables -F WORKCELL_EGRESS6 2>/dev/null || true
  sudo ip6tables -X WORKCELL_EGRESS6 2>/dev/null || true
fi
EOF
}

declare -a RULES=()
declare -a IPV6_RULES=()

build_allowlist_rules() {
  local endpoints="$1"
  local endpoint=""
  local host=""
  local port=""
  local ip=""

  RULES=(
    "sudo iptables -N WORKCELL_EGRESS 2>/dev/null || true"
    "sudo iptables -F WORKCELL_EGRESS"
    "sudo iptables -C DOCKER-USER -j WORKCELL_EGRESS 2>/dev/null || sudo iptables -I DOCKER-USER 1 -j WORKCELL_EGRESS"
    "sudo iptables -A WORKCELL_EGRESS -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT"
  )
  IPV6_RULES=(
    "if ! type ip6tables >/dev/null 2>&1; then echo \"Workcell requires ip6tables support to enforce dual-stack allowlist egress policy.\" >&2; exit 1; fi"
    "sudo ip6tables -N WORKCELL_EGRESS6 2>/dev/null || true"
    "sudo ip6tables -F WORKCELL_EGRESS6"
    "sudo ip6tables -C DOCKER-USER -j WORKCELL_EGRESS6 2>/dev/null || sudo ip6tables -I DOCKER-USER 1 -j WORKCELL_EGRESS6"
    "sudo ip6tables -A WORKCELL_EGRESS6 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT"
  )

  for endpoint in ${endpoints}; do
    validate_endpoint "${endpoint}"
  done

  initialize_host_tools

  for endpoint in ${endpoints}; do
    if [[ "${endpoint}" =~ ^\[([0-9A-Fa-f:.]+)\]:([0-9]{1,5})$ ]]; then
      host="[${BASH_REMATCH[1]}]"
      port="${BASH_REMATCH[2]}"
    else
      host="${endpoint%:*}"
      port="${endpoint##*:}"
    fi
    if [[ "${host}" == \[*\] ]]; then
      IPV6_RULES+=("sudo ip6tables -A WORKCELL_EGRESS6 -p tcp -d ${host:1:${#host}-2} --dport ${port} -j ACCEPT")
      continue
    fi
    if [[ "${host}" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then
      RULES+=("sudo iptables -A WORKCELL_EGRESS -p tcp -d ${host} --dport ${port} -j ACCEPT")
      continue
    fi
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
  IPV6_RULES+=("sudo ip6tables -A WORKCELL_EGRESS6 -j DROP")
}

render_allowlist_plan() {
  printf '%s\n' "${RULES[@]}"
  printf '%s\n' "${IPV6_RULES[@]}"
}

render_allowlist_apply_plan() {
  cat <<'EOF'
if ! type ip6tables >/dev/null 2>&1; then
  echo "Workcell requires ip6tables support to enforce dual-stack allowlist egress policy." >&2
  exit 1
fi
sudo ip6tables -L WORKCELL_EGRESS6 >/dev/null 2>&1 || true
EOF
  render_clear_plan
  render_allowlist_plan
}

case "${COMMAND}" in
  clear)
    clear_rules
    ;;
  plan)
    ENDPOINTS="${3:-}"
    [[ -n "${ENDPOINTS}" ]] || {
      echo "Missing endpoint list for plan" >&2
      exit 1
    }
    build_allowlist_rules "${ENDPOINTS}"
    render_allowlist_plan
    ;;
  apply)
    ENDPOINTS="${3:-}"
    [[ -n "${ENDPOINTS}" ]] || {
      echo "Missing endpoint list for apply" >&2
      exit 1
    }
    build_allowlist_rules "${ENDPOINTS}"
    run_in_vm "$(render_allowlist_apply_plan)"
    ;;
  *)
    usage
    exit 1
    ;;
esac
