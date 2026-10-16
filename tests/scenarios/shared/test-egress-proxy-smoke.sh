#!/usr/bin/env -S BASH_ENV= ENV= bash
# Real Colima smoke for --egress-proxy: an allowlisted host works through the
# sidecar, and a denied SNI, an IP literal and a DNS lookup all fail. The
# per-session network and sidecar are gone after an attached run and after a
# detached session stops. A brokered Claude key reaches the upstream only
# through the sidecar: the agent holds a placeholder, never the key.
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
    for file in prepare.stderr probe.stdout probe.stderr detached.stdout detached.stderr \
      broker.stdout broker.stderr broker-missing.stderr; do
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
  for file in smoke empty; do
    [[ ! -f "${TMP_DIR}/${file}.keychain-db" ]] || security delete-keychain "${TMP_DIR}/${file}.keychain-db" || status=1
  done
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

# A throwaway keychain holds a canary key; -T lets /usr/bin/security read it
# without a prompt. The harness-only knob points the broker at this keychain.
keychain_password="$(od -An -tx1 -N16 /dev/urandom | tr -d ' \n')"
canary="sk-canary-$(od -An -tx1 -N8 /dev/urandom | tr -d ' \n')"
for file in smoke empty; do
  security create-keychain -p "${keychain_password}" "${TMP_DIR}/${file}.keychain-db"
  security set-keychain-settings "${TMP_DIR}/${file}.keychain-db"
  security unlock-keychain -p "${keychain_password}" "${TMP_DIR}/${file}.keychain-db"
done
security add-generic-password -s workcell-claude-api-key -a smoke -w "${canary}" \
  -T /usr/bin/security "${TMP_DIR}/smoke.keychain-db"
printf 'version = 1\n\n[credentials.claude_api_key]\nresolver = "keychain-broker"\n' >"${TMP_DIR}/broker-policy.toml"
broker_flags=(--agent claude --workspace "${WORKSPACE}" --colima-profile "${PROFILE}"
  --injection-policy "${TMP_DIR}/broker-policy.toml" --egress-proxy
  --allow-arbitrary-command "--ack-arbitrary-command=${ACK_TODAY_UTC}")
brokered_workcell() {
  local keychain="$1"
  shift
  WORKCELL_VERIFY_INVARIANTS_SANITIZED_ENTRYPOINT=1 WORKCELL_TEST_CREDENTIAL_BROKER_KEYCHAIN="${keychain}" \
    /bin/bash -p "${ROOT_DIR}/scripts/workcell" "$@"
}
cat >"${TMP_DIR}/broker-probe.sh" <<'EOF'
bundle=/opt/workcell/host-injections
placeholder="$(cat "$(jq -r '.credentials.claude_api_key.mount_path' "${bundle}/manifest.json")")"
echo "placeholder=${placeholder}"
curl -sS -o /dev/null -w 'status=%{http_code}\n' --max-time 30 --cacert "${bundle}/egress-ca.pem" \
  -H "x-api-key: ${placeholder}" -H 'anthropic-version: 2023-06-01' https://api.anthropic.com/v1/models || echo "status=fail"
echo "canary_hits=$({ env; cat /proc/[0-9]*/environ; grep -rs . "${bundle}" "${HOME}/.claude"; } 2>/dev/null | grep -c sk-canary)"
EOF
brokered_workcell "${TMP_DIR}/smoke.keychain-db" "${broker_flags[@]}" -- bash -c "$(cat "${TMP_DIR}/broker-probe.sh")" \
  >"${TMP_DIR}/broker.stdout" 2>"${TMP_DIR}/broker.stderr"
grep -q '^credential_mode=brokered$' "${TMP_DIR}/broker.stderr"
grep -Eq '^placeholder=wcph-[0-9a-f]{32}$' "${TMP_DIR}/broker.stdout"
# The canary is not a real key, so the upstream answers 401: the swapped
# request left the sidecar. A 502 would mean the proxy refused it.
grep -q '^status=401$' "${TMP_DIR}/broker.stdout"
grep -q '^canary_hits=0$' "${TMP_DIR}/broker.stdout"
audit_log="$(sed -n 's/^execution_path=.* audit_log=//p' "${TMP_DIR}/broker.stderr")"
broker_session="$(grep 'event=launch' "${audit_log}" | grep 'credential_mode=brokered' | tail -n 1 |
  sed -n 's/.*session_id=\([^ ]*\).*/\1/p')"
[[ -n "${broker_session}" ]]
ledger="$(find "${XDG_STATE_HOME:-${REAL_HOME}/.local/state}/workcell/targets" \
  -path "*/${PROFILE}/sessions/${broker_session}.broker.jsonl" -type f)"
grep '"result":"granted"' "${ledger}" | grep -q '"header":"x-api-key"'
[[ -f "${ledger%.broker.jsonl}.egress-deny.jsonl" ]]
if grep -q "${canary}" "${ledger}" || grep -q placeholder_unswapped "${ledger%.broker.jsonl}.egress-deny.jsonl" ||
  [[ -e "${ledger%.jsonl}" ]]; then
  echo "the brokered run left the key, an unswapped placeholder or the broker state" >&2
  exit 1
fi
assert_no_proxy_residue "a brokered run"

# Without the Keychain item the broker stops the launch before the agent runs.
if brokered_workcell "${TMP_DIR}/empty.keychain-db" "${broker_flags[@]}" -- true >/dev/null 2>"${TMP_DIR}/broker-missing.stderr"; then
  echo "a brokered launch without the Keychain item started" >&2
  exit 1
fi
grep -q 'did not open its guest socket' "${TMP_DIR}/broker-missing.stderr"
assert_no_proxy_residue "a brokered launch without the Keychain item"

# The detached re-exec drops the harness-only keychain knob, so a brokered
# detached session needs a login Keychain item and is not part of this smoke.
guest_sockets="$(COLIMA_HOME="${REAL_HOME}/.colima" colima ssh --profile "${PROFILE}" -- ls /tmp)"
if grep -q '^workcell-broker-' <<<"${guest_sockets}"; then
  echo "a credential broker guest socket remains in the VM" >&2
  exit 1
fi

echo "Egress proxy smoke scenario passed"
