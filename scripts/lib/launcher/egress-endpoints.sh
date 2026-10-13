#!/usr/bin/env -S BASH_ENV= ENV= bash
# shellcheck shell=bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 Omkhar Arasaratnam
#
# scripts/lib/launcher/egress-endpoints.sh — egress-endpoint assembly module
# extracted from scripts/workcell as the next increment of the launcher
# decomposition (roadmap item D4, "wrapper assembly").  These helpers compute
# the per-session network egress allowlist and translate it into the container
# runtime's `--add-host` arguments: they map each target/credential to its
# fixed set of `host:port` endpoints (provider_endpoints is generated into
# scripts/lib/launcher/generated-adapters.sh), dedupe and deny-subtract the
# combined list (via the workcell-hostutil Go helper), fail closed when a deny
# rule empties the allowlist, label whether the launch actually enforces the
# allowlist, and resolve the surviving endpoints into `--add-host` runtime
# args.  They depend only on `csv_contains_value` (defined in scripts/workcell),
# `go_hostutil` (scripts/lib/launcher/go-hostutil.sh, sourced before this
# module), the read-only launch-state globals `AGENT`,
# `INJECTION_CREDENTIAL_KEYS`, `NETWORK_POLICY`, and `TARGET_BACKEND`, and the
# `RUNTIME_NETWORK_ARGS` array `build_runtime_host_aliases` populates — every
# dependency is defined before the first call site in the main launch path — so
# they are a self-contained, behaviour-preserving unit.  The --egress-proxy
# helpers also read `EGRESS_PROXY`, `SESSION_ID`, `ALLOW_ENDPOINTS`,
# `HOST_DOCKER_BIN` and `DOCKER_RUN`, and call the launcher's
# `run_workcell_docker_client_command`, `run_profile_docker_command` and
# `profile_sessions_dir_path`.  See
# docs/launcher-contract.md for the module contract.

target_broker_endpoints() {
  case "$1" in
    aws-ec2-ssm)
      echo "ec2.amazonaws.com:443 ssm.amazonaws.com:443 ssmmessages.amazonaws.com:443 ec2messages.amazonaws.com:443"
      ;;
    gcp-vm)
      echo "compute.googleapis.com:443 iap.googleapis.com:443 oslogin.googleapis.com:443"
      ;;
    *)
      return 0
      ;;
  esac
}

credential_extra_endpoints() {
  local credential_keys="${INJECTION_CREDENTIAL_KEYS:-}"
  local -a endpoints=()

  if csv_contains_value "${credential_keys}" "github_hosts" || csv_contains_value "${credential_keys}" "github_config"; then
    endpoints+=(
      github.com:443
      api.github.com:443
      objects.githubusercontent.com:443
      raw.githubusercontent.com:443
    )
  fi

  if [[ "${AGENT}" == "gemini" ]]; then
    if csv_contains_value "${credential_keys}" "gemini_oauth" || csv_contains_value "${credential_keys}" "gcloud_adc"; then
      endpoints+=(
        accounts.google.com:443
        oauth2.googleapis.com:443
        sts.googleapis.com:443
        aiplatform.googleapis.com:443
      )
    fi
  fi

  if ((${#endpoints[@]} == 0)); then
    return 0
  fi

  printf '%s\n' "${endpoints[*]}"
}

dedupe_endpoint_list() {
  go_hostutil helper dedupe-endpoints "$1"
}

# subtract_endpoint_list removes every endpoint in the second (deny) list from
# the first (allow) list, preserving allow order. Deny wins over allow: an
# operator-declared [network].deny_endpoints entry is removed from the computed
# allowlist even when a provider needs it. This only ever TIGHTENS the
# allowlist; it can never add an endpoint or change NETWORK_POLICY.
subtract_endpoint_list() {
  go_hostutil helper subtract-endpoints "$1" "$2"
}

# fail_empty_egress_after_deny aborts fail-closed when [network].deny_endpoints
# removed every computed endpoint on the enforced colima allowlist path: a
# zero-egress session cannot reach any provider, so report an actionable
# diagnostic instead of the helper's low-level empty-list error. The launch does
# not proceed, so no unbounded egress is ever installed.
fail_empty_egress_after_deny() {
  local phase="$1"
  echo "workcell: injection-policy [network].deny_endpoints removed every ${phase} egress endpoint on the strict colima allowlist path." >&2
  echo "  A session with no allowed egress cannot reach any provider or registry." >&2
  echo "  Remove a deny_endpoints entry, or add the required host:port to allow_endpoints." >&2
  exit 1
}

# egress_enforcement_label reports whether the launch actually enforces the
# per-session default-deny allowlist. Only the colima target applies the
# iptables/ip6tables DOCKER-USER allowlist (scripts/colima-egress-allowlist.sh),
# and only when NETWORK_POLICY=allowlist. Every other target (docker-desktop,
# aws-ec2-ssm, gcp-vm) relies on its own network controls, so this prints
# 'none' to make the parity gap explicit on the launch summary.
egress_enforcement_label() {
  if [[ "${EGRESS_PROXY}" -eq 1 ]]; then
    printf 'proxy\n'
  elif [[ "${TARGET_BACKEND}" == "colima" ]] && [[ "${NETWORK_POLICY}" == "allowlist" ]]; then
    printf 'allowlist\n'
  else
    printf 'none\n'
  fi
}

build_runtime_host_aliases() {
  local endpoint_list="$1"
  local host=""
  local ip=""

  RUNTIME_NETWORK_ARGS=()
  [[ "${NETWORK_POLICY}" == "allowlist" ]] || return 0
  if [[ "${EGRESS_PROXY}" -eq 1 ]]; then
    # The proxy refuses an endpoint set it cannot route. Say so before launch.
    go_hostutil helper validate-egress-proxy-allowlist "${endpoint_list}" || return 1
    egress_proxy_agent_network_args "${endpoint_list}"
    return 0
  fi

  while IFS=$'\t' read -r host ip; do
    [[ -n "${host}" ]] || continue
    if [[ "${ip}" == *:* ]]; then
      RUNTIME_NETWORK_ARGS+=(--add-host "${host}:[${ip}]")
    else
      RUNTIME_NETWORK_ARGS+=(--add-host "${host}:${ip}")
    fi
  done < <(go_hostutil helper resolve-endpoints "${endpoint_list}")

}

# --- Egress proxy (--egress-proxy, strict Colima only) ---
#
# The agent joins only the per-session internal network wc-<session>, with no
# resolver. Each allowlisted host maps to the sidecar proxy, which peeks at the
# SNI and is the only route out. The proxy IP is known only after the sidecar
# starts, so the --add-host values carry EGRESS_PROXY_IP_TOKEN until
# start_egress_proxy replaces it in DOCKER_RUN, in the --add-host values before
# the image only.
EGRESS_PROXY_IP_TOKEN="egress-proxy-ip"

# egress_proxy_names derives the per-session network and sidecar names from
# the session id alone, so a host command can find them without monitor state.
egress_proxy_names() {
  EGRESS_PROXY_NETWORK="wc-${SESSION_ID}"
  EGRESS_PROXY_CONTAINER="wc-egress-${SESSION_ID}"
}

egress_proxy_agent_network_args() {
  local endpoint=""

  egress_proxy_names
  RUNTIME_NETWORK_ARGS=(--network "${EGRESS_PROXY_NETWORK}" --dns 127.0.0.1)
  for endpoint in ${1}; do
    RUNTIME_NETWORK_ARGS+=(--add-host "${endpoint%:*}:${EGRESS_PROXY_IP_TOKEN}")
  done
}

# start_egress_proxy creates the internal network and runs the sidecar from the
# verified image with the agent's conformance flags. The network is internal
# and isolated: Docker gives an internal network no outside route, but its
# bridge still carries the VM's gateway address, where any VM service that
# listens on all addresses would answer the agent. The isolated gateway mode
# gives the bridge no gateway address. The sidecar also joins the
# bridge for its upstream route, so it listens only on its address in the
# internal subnet: other containers on the bridge cannot use this session's
# allowlist. Then it puts the sidecar's internal IP into the agent's --add-host
# values. Any failure stops the launch; cleanup and the detached monitor remove
# what was created.
start_egress_proxy() {
  local image_id="$1"
  local subnet=""
  local ip=""
  local i=""
  local endpoint=""
  local listening=""
  local ready=0
  local port=""

  run_workcell_docker_client_command "${HOST_DOCKER_BIN}" network create --internal \
    --opt com.docker.network.bridge.gateway_mode_ipv4=isolated "${EGRESS_PROXY_NETWORK}" >/dev/null || return 1
  subnet="$(run_workcell_docker_client_command "${HOST_DOCKER_BIN}" network inspect \
    -f '{{range .IPAM.Config}}{{.Subnet}} {{end}}' "${EGRESS_PROXY_NETWORK}")" || return 1
  subnet="${subnet% }"
  if [[ ! "${subnet}" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}/[0-9]{1,2}$ ]]; then
    echo "workcell: ${EGRESS_PROXY_NETWORK} has no single IPv4 subnet: ${subnet}" >&2
    return 1
  fi
  run_workcell_docker_client_command "${HOST_DOCKER_BIN}" create \
    --name "${EGRESS_PROXY_CONTAINER}" \
    --network "${EGRESS_PROXY_NETWORK}" \
    --user 65532:65532 \
    --cap-drop ALL \
    --security-opt no-new-privileges:true \
    --read-only \
    --pids-limit 256 \
    --memory 256m \
    --log-driver json-file \
    --log-opt max-size=10m \
    --sysctl net.ipv4.ip_unprivileged_port_start=0 \
    --entrypoint /usr/local/libexec/workcell/workcell-egress-proxy \
    "${image_id}" -allow "${ALLOW_ENDPOINTS}" -listen "${subnet}" >/dev/null || return 1
  run_workcell_docker_client_command "${HOST_DOCKER_BIN}" network connect bridge "${EGRESS_PROXY_CONTAINER}" || return 1
  run_workcell_docker_client_command "${HOST_DOCKER_BIN}" start "${EGRESS_PROXY_CONTAINER}" >/dev/null || return 1
  ip="$(run_workcell_docker_client_command "${HOST_DOCKER_BIN}" inspect \
    -f "{{if .State.Running}}{{(index .NetworkSettings.Networks \"${EGRESS_PROXY_NETWORK}\").IPAddress}}{{end}}" \
    "${EGRESS_PROXY_CONTAINER}")" || return 1
  if [[ ! "${ip}" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then
    echo "workcell: the egress proxy sidecar did not start on ${EGRESS_PROXY_NETWORK}." >&2
    run_workcell_docker_client_command "${HOST_DOCKER_BIN}" logs "${EGRESS_PROXY_CONTAINER}" >&2 || true
    return 1
  fi
  # A running container is not a listening proxy. Read its listening sockets
  # until every allowlisted port is bound, so the agent never meets a refusal.
  # The read opens no connection, so it adds no line to the deny log.
  for _ in {1..40}; do
    listening="$(run_workcell_docker_client_command "${HOST_DOCKER_BIN}" exec "${EGRESS_PROXY_CONTAINER}" cat /proc/net/tcp 2>/dev/null)" || listening=""
    ready=1
    for endpoint in ${ALLOW_ENDPOINTS}; do
      printf -v port ':%04X [0-9A-F]{8}:[0-9A-F]{4} 0A ' "$((10#${endpoint##*:}))"
      grep -Eqi -- "${port}" <<<"${listening}" || {
        ready=0
        break
      }
    done
    [[ "${ready}" -eq 1 ]] && break
    sleep 0.25
  done
  if [[ "${ready}" -ne 1 ]]; then
    echo "workcell: the egress proxy sidecar is not listening on every allowlisted port." >&2
    run_workcell_docker_client_command "${HOST_DOCKER_BIN}" logs "${EGRESS_PROXY_CONTAINER}" >&2 || true
    return 1
  fi
  for i in "${!DOCKER_RUN[@]}"; do
    # Only the generated --add-host values before the image carry the token. A
    # provider or prompt argument after the image is user input and stays as is.
    [[ "${DOCKER_RUN[i]}" != "${IMAGE_TAG}" ]] || break
    [[ "${DOCKER_RUN[i]}" == *":${EGRESS_PROXY_IP_TOKEN}" && "${DOCKER_RUN[i - 1]}" == "--add-host" ]] || continue
    DOCKER_RUN[i]="${DOCKER_RUN[i]%:"${EGRESS_PROXY_IP_TOKEN}"}:${ip}"
  done
}

# stop_egress_proxy saves the proxy's deny lines (its stdout, one JSON object
# per line) beside the session record, as <sessions-dir>/<session>.egress-deny.jsonl.
# The sessions directory already holds the record, so the save creates no
# directory. The save
# stages the logs in a private directory and publishes them through the same
# staged, fsynced, owner-only rename as the other session captures, so a failed
# or partial collection never replaces the final file. A failed save warns and
# the cleanup still runs: a live sidecar that holds the allowlist is worse than
# a missing log. It runs on every exit path, including after a failed start.
stop_egress_proxy() {
  local profile="$1"
  local deny_file=""
  local staged_dir=""

  [[ -n "${EGRESS_PROXY_CONTAINER:-}" ]] || return 0
  deny_file="$(profile_sessions_dir_path "${profile}")/${SESSION_ID}.egress-deny.jsonl"
  staged_dir="$(mktemp -d "${TMPDIR:-/tmp}/workcell-egress-deny.XXXXXX")" || staged_dir=""
  if [[ -z "${staged_dir}" ]] ||
    ! run_profile_docker_command "${profile}" logs "${EGRESS_PROXY_CONTAINER}" \
      >"${staged_dir}/file" 2>/dev/null ||
    ! go_hostutil helper publish-session-capture-file "${staged_dir}/file" "${deny_file}" >/dev/null 2>&1; then
    echo "workcell: warning: could not save the egress proxy deny log for ${SESSION_ID}." >&2
  fi
  [[ -z "${staged_dir}" ]] || rm -rf "${staged_dir}"
  run_profile_docker_command "${profile}" rm -f "${EGRESS_PROXY_CONTAINER}" >/dev/null 2>&1 ||
    echo "workcell: warning: could not remove the egress proxy sidecar ${EGRESS_PROXY_CONTAINER}." >&2
  run_profile_docker_command "${profile}" network rm "${EGRESS_PROXY_NETWORK}" >/dev/null 2>&1 ||
    echo "workcell: warning: could not remove the egress proxy network ${EGRESS_PROXY_NETWORK}." >&2
}

# stop_egress_proxy_for_session runs the same save and cleanup for a session
# whose monitor died before its own cleanup ran. The names derive from the
# session id, so the stop command needs no monitor state. A session that never
# started a proxy has no such network and is left alone. A failed lookup is
# not proof of absence: it fails the stop so the sidecar is never silently
# left behind.
stop_egress_proxy_for_session() {
  local profile="$1"
  local SESSION_ID="$2"
  local EGRESS_PROXY_NETWORK="" EGRESS_PROXY_CONTAINER=""
  local networks=""

  egress_proxy_names
  networks="$(run_profile_docker_command "${profile}" network ls \
    --filter "name=^${EGRESS_PROXY_NETWORK}\$" --format '{{.Name}}')" || {
    echo "workcell: could not list networks to find the egress proxy of ${SESSION_ID}." >&2
    return 1
  }
  grep -qx -- "${EGRESS_PROXY_NETWORK}" <<<"${networks}" || return 0
  stop_egress_proxy "${profile}"
}
