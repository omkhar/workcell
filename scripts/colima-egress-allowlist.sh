#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  colima-egress-allowlist.sh apply <profile> "<host:port ...>"
  colima-egress-allowlist.sh clear <profile>
EOF
}

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

resolve_ipv4() {
  local host="$1"
  python3 - "$host" <<'PY'
import socket, sys
host = sys.argv[1]
seen = []
for family, _, _, _, sockaddr in socket.getaddrinfo(host, None, socket.AF_INET):
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

require_tool colima
require_tool limactl
require_tool python3

lima_instance() {
  if [[ "${PROFILE}" == "default" ]]; then
    printf 'colima\n'
  else
    printf 'colima-%s\n' "${PROFILE}"
  fi
}

run_in_vm() {
  local script="$1"
  LIMA_HOME="${HOME}/.colima/_lima" LIMA_WORKDIR=/ limactl shell "$(lima_instance)" -- bash -lc "${script}"
}

clear_rules() {
  run_in_vm '
    set -euo pipefail
    sudo iptables -D DOCKER-USER -j WORKCELL_EGRESS 2>/dev/null || true
    sudo iptables -F WORKCELL_EGRESS 2>/dev/null || true
    sudo iptables -X WORKCELL_EGRESS 2>/dev/null || true
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

    for endpoint in ${ENDPOINTS}; do
      host="${endpoint%:*}"
      port="${endpoint##*:}"
      while IFS= read -r ip; do
        [[ -n "${ip}" ]] || continue
        RULES+=("sudo iptables -A WORKCELL_EGRESS -p tcp -d ${ip} --dport ${port} -j ACCEPT")
      done < <(resolve_ipv4 "${host}")
    done

    RULES+=("sudo iptables -A WORKCELL_EGRESS -j DROP")

    clear_rules
    run_in_vm "$(printf '%s\n' "${RULES[@]}")"
    ;;
  *)
    usage
    exit 1
    ;;
esac
