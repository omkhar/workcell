#!/usr/bin/env -S BASH_ENV= ENV= bash
# Real Colima smoke for --egress-proxy: an allowlisted host works through the
# sidecar, and a denied SNI, an IP literal and a DNS lookup all fail. The
# per-session network and sidecar are gone after an attached run and after a
# detached session stops.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-egress-proxy-scenario.XXXXXX")"
# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"

REAL_HOME="$(resolve_workcell_real_home)"
PROFILE="wcl-egp-$$"
WORKSPACE="${TMP_DIR}/workspace"
ACK_TODAY_UTC="$(date -u '+%Y-%m-%d')"

cleanup() {
  local status=$?
  local file=""

  if [[ "${status}" -ne 0 ]]; then
    for file in prepare.stderr probe.stdout probe.stderr detached.stdout detached.stderr; do
      [[ -f "${TMP_DIR}/${file}" ]] || continue
      printf -- '--- %s ---\n' "${file}" >&2
      cat "${TMP_DIR}/${file}" >&2
    done
  fi
  if command -v colima >/dev/null 2>&1; then
    colima stop --profile "${PROFILE}" >/dev/null 2>&1 || true
    colima delete --profile "${PROFILE}" --force >/dev/null 2>&1 || true
  fi
  rm -rf "${REAL_HOME}/.colima/${PROFILE}"
  rm -rf "${REAL_HOME}/.colima/_lima/colima-${PROFILE}"
  rm -rf "${REAL_HOME}/.colima/_lima/_disks/colima-${PROFILE}"
  rm -f "${REAL_HOME}/.colima/_store/colima-${PROFILE}.json"
  rm -rf "${TMP_DIR}"
  exit "${status}"
}
trap cleanup EXIT

profile_docker() {
  COLIMA_HOME="${REAL_HOME}/.colima" colima start --profile "${PROFILE}" >/dev/null
  env DOCKER_HOST="unix://${REAL_HOME}/.colima/${PROFILE}/docker.sock" docker "$@"
}

assert_no_proxy_residue() {
  local label="$1"

  if profile_docker ps -a --format '{{.Names}}' | grep -q '^wc-egress-'; then
    echo "egress proxy sidecar remains after ${label}" >&2
    exit 1
  fi
  if profile_docker network ls --format '{{.Name}}' | grep -q '^wc-'; then
    echo "egress proxy network remains after ${label}" >&2
    exit 1
  fi
}

mkdir -p "${WORKSPACE}"
git -C "${WORKSPACE}" init -q
printf 'scenario workspace\n' >"${WORKSPACE}/README.md"

"${ROOT_DIR}/scripts/workcell" --prepare-only --agent codex --workspace "${WORKSPACE}" \
  --colima-profile "${PROFILE}" --no-default-injection-policy \
  >/dev/null 2>"${TMP_DIR}/prepare.stderr"

# A prepared image built from other inputs may carry an older proxy binary.
# Replace the marker's build-input digest; the --egress-proxy launch below must
# refresh the image automatically and record the current digest again.
image_marker="$(find "${XDG_STATE_HOME:-${REAL_HOME}/.local/state}/workcell/targets" \
  -path "*/${PROFILE}/*" -name workcell.image-ready -type f | head -n 1)"
[[ -n "${image_marker}" ]]
prepared_digest="$(sed -n 's/^build_inputs_sha256=//p' "${image_marker}")"
[[ "${prepared_digest}" =~ ^[0-9a-f]{64}$ ]]
sed -i.bak 's/^build_inputs_sha256=.*/build_inputs_sha256=stale/' "${image_marker}" && rm -f "${image_marker}.bak"

# The allowed probe accepts any HTTP status: a completed TLS handshake with the
# real api.openai.com proves the route. The denied SNI rides the same allowlisted
# proxy address, so only the proxy's SNI check can refuse it.
cat >"${TMP_DIR}/probe.sh" <<'EOF'
code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 30 https://api.openai.com/v1/models)" && echo "allowed=${code}" || echo "allowed=fail"
curl -sS -o /dev/null --max-time 15 --connect-to example.com:443:api.openai.com:443 https://example.com/ && echo "denied_sni=open" || echo "denied_sni=blocked"
curl -sS -o /dev/null --max-time 15 -k https://1.1.1.1/ && echo "ip_literal=open" || echo "ip_literal=blocked"
getent hosts example.com >/dev/null && echo "dns=open" || echo "dns=blocked"
EOF

"${ROOT_DIR}/scripts/workcell" --agent codex --workspace "${WORKSPACE}" \
  --colima-profile "${PROFILE}" --no-default-injection-policy --egress-proxy \
  --allow-arbitrary-command "--ack-arbitrary-command=${ACK_TODAY_UTC}" \
  -- bash -c "$(cat "${TMP_DIR}/probe.sh")" \
  >"${TMP_DIR}/probe.stdout" 2>"${TMP_DIR}/probe.stderr"
grep -q '^egress_enforcement=proxy$' "${TMP_DIR}/probe.stderr"
grep -q -- '^--egress-proxy needs a runtime image prepared from this checkout' "${TMP_DIR}/probe.stderr"
grep -q "^build_inputs_sha256=${prepared_digest}$" "${image_marker}"
grep -Eq '^allowed=[1-5][0-9][0-9]$' "${TMP_DIR}/probe.stdout"
grep -q '^denied_sni=blocked$' "${TMP_DIR}/probe.stdout"
grep -q '^ip_literal=blocked$' "${TMP_DIR}/probe.stdout"
grep -q '^dns=blocked$' "${TMP_DIR}/probe.stdout"
assert_no_proxy_residue "an attached run"

# The detached session makes one denied request, so its deny log is not empty.
"${ROOT_DIR}/scripts/workcell" session start --session-workspace direct --agent codex --workspace "${WORKSPACE}" \
  --colima-profile "${PROFILE}" --no-default-injection-policy --egress-proxy \
  --allow-arbitrary-command "--ack-arbitrary-command=${ACK_TODAY_UTC}" \
  -- bash -c 'curl -sS -o /dev/null --max-time 15 --connect-to example.com:443:api.openai.com:443 https://example.com/; exec sleep 600' \
  >"${TMP_DIR}/detached.stdout" 2>"${TMP_DIR}/detached.stderr"
session_id="$(sed -n 's/^session_id=//p' "${TMP_DIR}/detached.stdout")"
[[ -n "${session_id}" ]]
sidecar="wc-egress-${session_id}"
profile_docker ps --format '{{.Names}}' | grep -qx "${sidecar}"

# The session network is isolated: it has no gateway address, so a VM service
# that listens on all addresses cannot answer the agent at the gateway.
session_gateway="$(profile_docker network inspect -f '{{range .IPAM.Config}}{{.Gateway}}{{end}}' "wc-${session_id}")"
if [[ "${session_gateway}" =~ ^[0-9.]+$ ]]; then
  echo "the session network has a gateway address: ${session_gateway}" >&2
  exit 1
fi
[[ "$(profile_docker network inspect -f '{{index .Options "com.docker.network.bridge.gateway_mode_ipv4"}}' "wc-${session_id}")" == "isolated" ]]

# The sidecar is also on the bridge for its upstream route. It listens only on
# its internal-network address, so another container on the bridge cannot use
# this session's allowlist.
bridge_ip="$(profile_docker inspect -f '{{.NetworkSettings.Networks.bridge.IPAddress}}' "${sidecar}")"
sidecar_image="$(profile_docker inspect -f '{{.Image}}' "${sidecar}")"
[[ -n "${bridge_ip}" ]]
if profile_docker run --rm --network bridge --entrypoint curl "${sidecar_image}" \
  -sS -o /dev/null --max-time 15 --connect-to "api.openai.com:443:${bridge_ip}:443" https://api.openai.com/v1/models; then
  echo "the egress proxy accepted a connection on its bridge address" >&2
  exit 1
fi

for _ in $(seq 1 30); do
  profile_docker logs "${sidecar}" 2>/dev/null | grep -q '"host":"example.com"' && break
  sleep 1
done
"${ROOT_DIR}/scripts/workcell" session stop --id "${session_id}" >/dev/null
for _ in $(seq 1 60); do
  profile_docker ps -a --format '{{.Names}}' | grep -q '^wc-egress-' || break
  sleep 1
done
assert_no_proxy_residue "a detached session stop"
deny_log="$(find "${XDG_STATE_HOME:-${REAL_HOME}/.local/state}/workcell/targets" \
  -path "*/${PROFILE}/sessions/${session_id}.egress-deny.jsonl" -type f | head -n 1)"
grep -q '"host":"example.com"' "${deny_log}"

echo "Egress proxy smoke scenario passed"
