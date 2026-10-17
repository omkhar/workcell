#!/usr/bin/env -S BASH_ENV= ENV= bash
# Real Colima smoke for the egress proxy, the default for strict Colima: an
# allowlisted host works through the sidecar, and a denied SNI, an IP literal
# and a DNS lookup all fail. The per-session network and sidecar are gone after
# an attached run and after a detached session stops. A brokered Claude key and
# a brokered Codex API key reach the upstream only through the sidecar: the
# agent holds a placeholder, never the key. Copilot stays direct and reaches its
# hosts through a spliced connection. --egress-proxy=off keeps the allowlist
# session with the VM resolver, where the real Codex binary uses a key from
# source that the default refuses.
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
      broker.stdout broker.stderr codex.stdout codex.stderr codex-exec.stdout codex-exec.stderr \
      broker-missing.stderr off.stdout off.stderr copilot.stdout copilot.stderr \
      plain-default.stderr plain-off.stdout plain-off.stderr; do
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
# Replace the marker's build-input digest; the default proxy launch below must
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
  --colima-profile "${PROFILE}" --no-default-injection-policy \
  --allow-arbitrary-command "--ack-arbitrary-command=${ACK_TODAY_UTC}" \
  -- bash -c "$(cat "${TMP_DIR}/probe.sh")" \
  >"${TMP_DIR}/probe.stdout" 2>"${TMP_DIR}/probe.stderr"
grep -q '^egress_enforcement=proxy$' "${TMP_DIR}/probe.stderr"
grep -q '^The egress proxy needs a runtime image prepared from this checkout' "${TMP_DIR}/probe.stderr"
grep -q "^build_inputs_sha256=${prepared_digest}$" "${image_marker}"
grep -Eq '^allowed=[1-5][0-9][0-9]$' "${TMP_DIR}/probe.stdout"
grep -q '^denied_sni=blocked$' "${TMP_DIR}/probe.stdout"
grep -q '^ip_literal=blocked$' "${TMP_DIR}/probe.stdout"
grep -q '^dns=blocked$' "${TMP_DIR}/probe.stdout"
assert_no_proxy_residue "an attached run"

# --egress-proxy=off keeps the allowlist session: the agent uses the VM
# resolver, the allowlisted host works, and an IP literal stays blocked by the
# profile-wide rules. No sidecar or session network is created.
"${ROOT_DIR}/scripts/workcell" --agent codex --workspace "${WORKSPACE}" \
  --colima-profile "${PROFILE}" --no-default-injection-policy --egress-proxy=off \
  --allow-arbitrary-command "--ack-arbitrary-command=${ACK_TODAY_UTC}" \
  -- bash -c "$(cat "${TMP_DIR}/probe.sh")" \
  >"${TMP_DIR}/off.stdout" 2>"${TMP_DIR}/off.stderr"
grep -q '^egress_enforcement=allowlist$' "${TMP_DIR}/off.stderr"
grep -Eq '^allowed=[1-5][0-9][0-9]$' "${TMP_DIR}/off.stdout"
grep -q '^ip_literal=blocked$' "${TMP_DIR}/off.stdout"
grep -q '^dns=open$' "${TMP_DIR}/off.stdout"
assert_no_proxy_residue "an --egress-proxy=off run"

# Copilot stays direct under the default proxy. Its hosts are spliced, not
# terminated, so the image system roots verify the real certificates.
cat >"${TMP_DIR}/copilot-probe.sh" <<'EOF'
for host in api.githubcopilot.com api.github.com github.com; do
  code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 30 "https://${host}/")" && echo "${host}=${code}" || echo "${host}=fail"
done
EOF
"${ROOT_DIR}/scripts/workcell" --agent copilot --workspace "${WORKSPACE}" \
  --colima-profile "${PROFILE}" --no-default-injection-policy \
  --allow-arbitrary-command "--ack-arbitrary-command=${ACK_TODAY_UTC}" \
  -- bash -c "$(cat "${TMP_DIR}/copilot-probe.sh")" \
  >"${TMP_DIR}/copilot.stdout" 2>"${TMP_DIR}/copilot.stderr"
grep -q '^egress_enforcement=proxy$' "${TMP_DIR}/copilot.stderr"
grep -q '^credential_mode=direct$' "${TMP_DIR}/copilot.stderr"
for host in api.githubcopilot.com api.github.com github.com; do
  grep -Eq "^${host}=[1-5][0-9][0-9]$" "${TMP_DIR}/copilot.stdout"
done
assert_no_proxy_residue "a Copilot run"

# The detached session makes one denied request, so its deny log is not empty.
"${ROOT_DIR}/scripts/workcell" session start --session-workspace direct --agent codex --workspace "${WORKSPACE}" \
  --colima-profile "${PROFILE}" --no-default-injection-policy --egress-proxy=on \
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
  --injection-policy "${TMP_DIR}/broker-policy.toml"
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

# A brokered Codex API key. The probe checks the staged auth.json and the CA
# bundle with curl: the brokered host, and a spliced host that must still
# verify with the image roots in the bundle. Then the real Codex binary runs
# as the managed provider, so its own TLS stack (rustls) must trust the sidecar
# leaf through CODEX_CA_CERTIFICATE; neither the system roots nor NODE_EXTRA_CA_CERTS
# hold the session CA for it. The canary is not a real key, so OpenAI answers
# 401 to the swapped request.
security add-generic-password -s workcell-codex-api-key -a smoke -w "${canary}" \
  -T /usr/bin/security "${TMP_DIR}/smoke.keychain-db"
printf 'version = 1\n\n[credentials.codex_auth]\nresolver = "keychain-broker"\n' >"${TMP_DIR}/codex-policy.toml"
codex_flags=(--agent codex --workspace "${WORKSPACE}" --colima-profile "${PROFILE}"
  --injection-policy "${TMP_DIR}/codex-policy.toml")
cat >"${TMP_DIR}/codex-probe.sh" <<'EOF'
bundle=/opt/workcell/host-injections
auth="$(cat "$(jq -r '.credentials.codex_auth.mount_path' "${bundle}/manifest.json")")"
echo "auth=${auth}"
placeholder="$(jq -r .OPENAI_API_KEY <<<"${auth}")"
curl -sS -o /dev/null -w 'status=%{http_code}\n' --max-time 30 --cacert "${bundle}/egress-ca-bundle.pem" \
  -H "Authorization: Bearer ${placeholder}" https://api.openai.com/v1/models || echo "status=fail"
curl -sS -o /dev/null -w 'spliced=%{http_code}\n' --max-time 30 --cacert "${bundle}/egress-ca-bundle.pem" \
  https://chatgpt.com/ || echo "spliced=fail"
echo "canary_hits=$({ env; cat /proc/[0-9]*/environ; grep -rs . "${bundle}" "${HOME}/.codex"; } 2>/dev/null | grep -c sk-canary)"
EOF
brokered_workcell "${TMP_DIR}/smoke.keychain-db" "${codex_flags[@]}" \
  --allow-arbitrary-command "--ack-arbitrary-command=${ACK_TODAY_UTC}" \
  -- bash -c "$(cat "${TMP_DIR}/codex-probe.sh")" >"${TMP_DIR}/codex.stdout" 2>"${TMP_DIR}/codex.stderr"
grep -q '^credential_mode=brokered$' "${TMP_DIR}/codex.stderr"
grep -Eq '^auth=\{"OPENAI_API_KEY":"wcph-[0-9a-f]{32}"\}$' "${TMP_DIR}/codex.stdout"
grep -q '^status=401$' "${TMP_DIR}/codex.stdout"
grep -Eq '^spliced=[1-5][0-9][0-9]$' "${TMP_DIR}/codex.stdout"
grep -q '^canary_hits=0$' "${TMP_DIR}/codex.stdout"
assert_no_proxy_residue "a brokered Codex probe"

codex_exec_status=0
brokered_workcell "${TMP_DIR}/smoke.keychain-db" "${codex_flags[@]}" \
  --agent-arg exec --agent-arg 'Reply with one word.' >"${TMP_DIR}/codex-exec.stdout" 2>"${TMP_DIR}/codex-exec.stderr" ||
  codex_exec_status=$?
echo "codex_exec_status=${codex_exec_status}"
grep -q '^credential_mode=brokered$' "${TMP_DIR}/codex-exec.stderr"
grep -Eiq '401|unauthorized|invalid_api_key|incorrect api key' "${TMP_DIR}/codex-exec.stdout" "${TMP_DIR}/codex-exec.stderr"
if grep -Eiq 'certificate|unknownissuer|invalid peer' "${TMP_DIR}/codex-exec.stdout" "${TMP_DIR}/codex-exec.stderr"; then
  echo "Codex did not trust the session CA" >&2
  exit 1
fi
codex_audit_log="$(sed -n 's/^execution_path=.* audit_log=//p' "${TMP_DIR}/codex-exec.stderr")"
codex_session="$(grep 'event=launch' "${codex_audit_log}" | grep 'agent=codex' | grep 'credential_mode=brokered' | tail -n 1 |
  sed -n 's/.*session_id=\([^ ]*\).*/\1/p')"
[[ -n "${codex_session}" && "${codex_session}" != "${broker_session}" ]]
codex_ledger="$(find "${XDG_STATE_HOME:-${REAL_HOME}/.local/state}/workcell/targets" \
  -path "*/${PROFILE}/sessions/${codex_session}.broker.jsonl" -type f)"
grep '"result":"granted"' "${codex_ledger}" | grep '"credential":"codex_auth"' | grep -q '"host":"api.openai.com"'
if grep -q "${canary}" "${codex_ledger}" || grep -q placeholder_unswapped "${codex_ledger%.broker.jsonl}.egress-deny.jsonl" ||
  [[ -e "${codex_ledger%.jsonl}" ]]; then
  echo "the brokered Codex run left the key, an unswapped placeholder or the broker state" >&2
  exit 1
fi
assert_no_proxy_residue "a brokered Codex run"

# A Codex API key from source stops a default launch before anything starts,
# and the message names --egress-proxy=off. With --egress-proxy=off the real
# Codex binary holds the key directly and reaches OpenAI, which answers 401.
printf '{"OPENAI_API_KEY":"%s"}\n' "${canary}" >"${TMP_DIR}/plain-auth.json"
chmod 0600 "${TMP_DIR}/plain-auth.json"
printf 'version = 1\n\n[credentials.codex_auth]\nsource = "%s"\n' "${TMP_DIR}/plain-auth.json" >"${TMP_DIR}/plain-policy.toml"
plain_flags=(--agent codex --workspace "${WORKSPACE}" --colima-profile "${PROFILE}"
  --injection-policy "${TMP_DIR}/plain-policy.toml" --agent-arg exec --agent-arg 'Reply with one word.')
if "${ROOT_DIR}/scripts/workcell" "${plain_flags[@]}" >/dev/null 2>"${TMP_DIR}/plain-default.stderr"; then
  echo "a default launch accepted a Codex API key from source" >&2
  exit 1
fi
grep -q 'or launch with --egress-proxy=off$' "${TMP_DIR}/plain-default.stderr"
assert_no_proxy_residue "a refused launch"
plain_off_status=0
"${ROOT_DIR}/scripts/workcell" "${plain_flags[@]}" --egress-proxy=off \
  >"${TMP_DIR}/plain-off.stdout" 2>"${TMP_DIR}/plain-off.stderr" || plain_off_status=$?
echo "plain_off_status=${plain_off_status}"
grep -q '^egress_enforcement=allowlist$' "${TMP_DIR}/plain-off.stderr"
grep -q '^credential_mode=direct$' "${TMP_DIR}/plain-off.stderr"
grep -Eiq '401|unauthorized|invalid_api_key|incorrect api key' "${TMP_DIR}/plain-off.stdout" "${TMP_DIR}/plain-off.stderr"
assert_no_proxy_residue "an --egress-proxy=off Codex run"

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
