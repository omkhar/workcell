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

# --- Credential broker (--egress-proxy with a keychain-broker key) ---
#
# The host broker reads each brokered key from its fixed Keychain item and
# reaches the sidecar only through an ssh reverse forward to a guest socket in
# the VM. The guest socket belongs to the sidecar uid with mode 0600, so only
# the sidecar and root in the VM can connect. The agent holds placeholders only.

# credential_broker_guest_socket derives the guest socket from the session id,
# so a stop needs no launcher state.
credential_broker_guest_socket() {
  printf '/tmp/workcell-broker-%s.sock\n' "$1"
}

# start_credential_broker starts the broker for a session whose bundle holds a
# placeholder, and fills the fixed sidecar arguments of start_egress_proxy. The
# token stays in a shell variable: the broker reads it from fd 3, and the docker
# client passes it to the sidecar from its environment, never in argv. After
# the guest socket check it appends the plan's credential_broker_start records;
# a failed append stops the launch, so no broker serves a session unaudited.
start_credential_broker() {
  local plan="" key="" value="" guest="" pid="" deadline=0 mode="" start=""
  local -a sources=() keychain=() starts=() fields=()

  EGRESS_PROXY_BROKER_DOCKER_ARGS=()
  EGRESS_PROXY_BROKER_PROXY_ARGS=()
  CREDENTIAL_BROKER_TOKEN=""
  CREDENTIAL_BROKER_DIR=""
  [[ -n "${INJECTION_CREDENTIAL_PLACEHOLDER}" ]] || return 0
  if [[ -n "${WORKCELL_TEST_CREDENTIAL_BROKER_KEYCHAIN:-}" ]]; then
    if ! support_matrix_host_override_allowed; then
      echo "WORKCELL_TEST_CREDENTIAL_BROKER_KEYCHAIN is reserved for the Workcell test harness." >&2
      return 1
    fi
    keychain=(--keychain "${WORKCELL_TEST_CREDENTIAL_BROKER_KEYCHAIN}")
  fi
  plan="$(go_hostutil helper credential-broker-plan "${INJECTION_BUNDLE_ROOT}" "${INJECTION_CREDENTIAL_PLACEHOLDER}")" || return 1
  while IFS='=' read -r key value; do
    case "${key}" in
      terminate) EGRESS_PROXY_BROKER_PROXY_ARGS+=(-terminate "${value}") ;;
      credential) sources+=(--credential "${value}") ;;
      audit) starts+=("${value}") ;;
      *)
        echo "workcell: unexpected credential broker plan field: ${key}" >&2
        return 1
        ;;
    esac
  done <<<"${plan}"
  if ((${#sources[@]} == 0)); then
    echo "workcell: the credential broker plan names no credential." >&2
    return 1
  fi
  guest="$(credential_broker_guest_socket "${SESSION_ID}")"
  EGRESS_PROXY_BROKER_PROXY_ARGS+=(-ca-out /run/workcell-egress/ca.pem -ca-ttl 24h -broker-socket /run/workcell-broker.sock)
  EGRESS_PROXY_BROKER_DOCKER_ARGS=(
    --tmpfs "/run/workcell-egress:rw,noexec,nosuid,nodev,size=64k,uid=65532,gid=65532,mode=0700"
    -v "${guest}:/run/workcell-broker.sock"
    -e WORKCELL_BROKER_TOKEN
  )
  # The socket path must stay under the 104-byte limit, and every ancestor
  # must pass the broker's private-parent check, so the real path is used.
  CREDENTIAL_BROKER_DIR="$(mktemp -d "${TMPDIR:-/tmp}/wcb.XXXXXX")" || return 1
  CREDENTIAL_BROKER_DIR="$(cd "${CREDENTIAL_BROKER_DIR}" && pwd -P)" || {
    abort_credential_broker "${pid}" "${guest}"
    return 1
  }
  run_host_colima ssh-config --profile "${COLIMA_PROFILE}" >"${CREDENTIAL_BROKER_DIR}/ssh_config" || {
    abort_credential_broker "${pid}" "${guest}"
    return 1
  }
  chmod 0600 "${CREDENTIAL_BROKER_DIR}/ssh_config" || {
    abort_credential_broker "${pid}" "${guest}"
    return 1
  }
  # Colima sshd does not unlink a stale socket before it binds, so the forward
  # would fail on one left by an earlier run.
  run_host_colima ssh --profile "${COLIMA_PROFILE}" -- sudo rm -f -- "${guest}" || {
    abort_credential_broker "${pid}" "${guest}"
    return 1
  }
  CREDENTIAL_BROKER_TOKEN="$(od -An -tx1 -N32 /dev/urandom | tr -d ' \n')" || {
    abort_credential_broker "${pid}" "${guest}"
    return 1
  }
  if [[ ! "${CREDENTIAL_BROKER_TOKEN}" =~ ^[0-9a-f]{64}$ ]]; then
    echo "workcell: could not create the credential broker session token." >&2
    abort_credential_broker "${pid}" "${guest}"
    return 1
  fi
  go_tool_bin workcell-hostutil || {
    abort_credential_broker "${pid}" "${guest}"
    return 1
  }
  (cd / && exec env -i PATH="${TRUSTED_HOST_PATH}" HOME="${REAL_HOME}" LC_ALL=C LANG=C \
    "${GO_TOOL_BIN}" credential-broker-cli serve --socket "${CREDENTIAL_BROKER_DIR}/b.sock" --token-fd 3 \
    --ssh-config "${CREDENTIAL_BROKER_DIR}/ssh_config" --ssh-host "colima-${COLIMA_PROFILE}" \
    --guest-socket "${guest}" "${sources[@]}" ${keychain[@]+"${keychain[@]}"} \
    --log "${CREDENTIAL_BROKER_DIR}/ledger.jsonl") \
    3<<<"${CREDENTIAL_BROKER_TOKEN}" </dev/null >/dev/null 2>"${CREDENTIAL_BROKER_DIR}/broker.log" &
  pid="$!"
  disown "${pid}"
  # The published copy is the durable one: the helper stages it owner-only,
  # syncs it and its directory, and renames it into place.
  (umask 077 && printf '%s %s\n' "${pid}" "${CREDENTIAL_BROKER_DIR}" >"${CREDENTIAL_BROKER_DIR}/state") || {
    abort_credential_broker "${pid}" "${guest}"
    return 1
  }
  go_hostutil helper publish-session-capture-file "${CREDENTIAL_BROKER_DIR}/state" \
    "$(profile_sessions_dir_path "${COLIMA_PROFILE}")/${SESSION_ID}.broker" >/dev/null || {
    abort_credential_broker "${pid}" "${guest}"
    return 1
  }
  # The first read of a Keychain item can wait for the owner's answer.
  deadline=$((SECONDS + 150))
  until run_host_colima ssh --profile "${COLIMA_PROFILE}" -- test -S "${guest}" 2>/dev/null; do
    if ! kill -0 "${pid}" 2>/dev/null || ((SECONDS >= deadline)); then
      echo "workcell: the credential broker did not open its guest socket ${guest}." >&2
      cat "${CREDENTIAL_BROKER_DIR}/broker.log" >&2
      abort_credential_broker "${pid}" "${guest}"
      return 1
    fi
    sleep 1
  done
  run_host_colima ssh --profile "${COLIMA_PROFILE}" -- sudo chown 65532:65532 -- "${guest}" || {
    abort_credential_broker "${pid}" "${guest}"
    return 1
  }
  # The sshd bind mask decides the mode the forward creates, so set it and
  # read back the owner and mode before the sidecar mounts the socket.
  run_host_colima ssh --profile "${COLIMA_PROFILE}" -- sudo chmod 0600 -- "${guest}" || {
    abort_credential_broker "${pid}" "${guest}"
    return 1
  }
  mode="$(run_host_colima ssh --profile "${COLIMA_PROFILE}" -- stat -c %u:%g:%a -- "${guest}")" || mode=""
  if [[ "${mode}" != 65532:65532:600 ]]; then
    echo "workcell: the credential broker socket ${guest} is not 0600 for the sidecar uid: ${mode}" >&2
    abort_credential_broker "${pid}" "${guest}"
    return 1
  fi
  for start in ${starts[@]+"${starts[@]}"}; do
    read -r -a fields <<<"${start}"
    append_session_control_audit_record "${COLIMA_PROFILE}" "${SESSION_ID}" "${fields[@]}" || {
      echo "workcell: could not audit the credential broker start for ${SESSION_ID}." >&2
      abort_credential_broker "${pid}" "${guest}"
      return 1
    }
  done
}

# abort_credential_broker undoes a start that failed after the private
# directory exists: it stops the broker it started, if any, removes the guest
# socket and the private directory, and clears the token. A failed launch then
# leaves no forward and no resolved key behind.
abort_credential_broker() {
  credential_broker_terminate "${COLIMA_PROFILE}" "$1" "$2" "${CREDENTIAL_BROKER_DIR}" ""
  CREDENTIAL_BROKER_DIR=""
  CREDENTIAL_BROKER_TOKEN=""
}

# credential_broker_find owns the answer to "which broker serves this
# session". The launcher can die between the start and the state file, so it
# reads this uid's processes, not the state file. It compares argv words: the
# pinned workcell-hostutil binary, then credential-broker-cli serve, and a
# --guest-socket word that is exactly this session's guest socket. A shell or
# an editor whose arguments merely mention those words does not match. It sets CREDENTIAL_BROKER_FOUND_PID, and
# CREDENTIAL_BROKER_FOUND_DIR from the --socket <dir>/b.sock argument when that
# directory has the private shape and belongs to this user. ps joins argv with
# spaces: the guest socket holds none, and the directory words are joined back
# until one ends in /b.sock, then checked.
credential_broker_find() {
  local guest="" listing="" pid="" command="" dir="" i=0 match=0
  local -a words=()

  CREDENTIAL_BROKER_FOUND_PID=""
  CREDENTIAL_BROKER_FOUND_DIR=""
  guest="$(credential_broker_guest_socket "$1")"
  listing="$(ps -U "$(id -u)" -o pid=,command=)" || return 1
  while read -r pid command; do
    read -r -a words <<<"${command}"
    [[ "${#words[@]}" -gt 3 && "${words[0]##*/}" == workcell-hostutil-* &&
      "${words[1]}" == credential-broker-cli && "${words[2]}" == serve ]] || continue
    match=0
    dir=""
    for ((i = 3; i + 1 < ${#words[@]}; i++)); do
      if [[ "${words[i]}" == --guest-socket && "${words[i + 1]}" == "${guest}" ]]; then
        match=1
      elif [[ "${words[i]}" == --socket && -z "${dir}" ]]; then
        dir="${words[i + 1]}"
        while [[ "${dir}" != */b.sock ]] && ((i + 2 < ${#words[@]})); do
          i=$((i + 1))
          dir="${dir} ${words[i + 1]}"
        done
      fi
    done
    [[ "${match}" -eq 1 && "${pid}" =~ ^[0-9]+$ ]] || continue
    CREDENTIAL_BROKER_FOUND_PID="${pid}"
    dir="${dir%/b.sock}"
    if [[ "${dir}" == */wcb.?????? && -d "${dir}" && ! -L "${dir}" && -O "${dir}" ]]; then
      CREDENTIAL_BROKER_FOUND_DIR="${dir}"
    fi
    return 0
  done <<<"${listing}"
}

# credential_broker_terminate owns the cleanup of one broker: it signals PID
# when one is given, removes the guest socket in the VM, publishes the ledger
# to RECORD.jsonl when RECORD is given, and removes the private directory only
# when it has the private shape and belongs to this user. A failure only warns.
credential_broker_terminate() {
  local profile="$1"
  local pid="$2"
  local guest="$3"
  local dir="$4"
  local record="$5"
  local stat=""

  if [[ -n "${pid}" ]]; then
    kill -TERM "${pid}" || echo "workcell: warning: could not stop the credential broker ${pid}." >&2
    # The launcher's own cleanup is the broker's parent, so an exited broker
    # stays a zombie there until the launcher exits.
    for _ in {1..20}; do
      stat="$(ps -p "${pid}" -o stat=)" || break
      [[ "${stat}" != *Z* ]] || break
      sleep 0.25
    done
  fi
  run_host_colima ssh --profile "${profile}" -- sudo rm -f -- "${guest}" >/dev/null ||
    echo "workcell: warning: could not remove the credential broker socket ${guest} in the VM." >&2
  # The broker refuses a ledger under a parent with an extended ACL, as a macOS
  # home has, so the ledger lives in the private directory until the stop
  # publishes it beside the session record.
  if [[ "${dir}" == */wcb.?????? && -d "${dir}" && ! -L "${dir}" && -O "${dir}" ]]; then
    [[ -z "${record}" || ! -f "${dir}/ledger.jsonl" ]] ||
      go_hostutil helper publish-session-capture-file "${dir}/ledger.jsonl" "${record}.jsonl" >/dev/null ||
      echo "workcell: warning: could not save the credential broker ledger." >&2
    rm -rf "${dir}"
  fi
}

# stop_credential_broker stops the broker of a session from the session id
# alone, so the monitor, stop and delete paths need no launcher state. The
# process scan finds a broker even when no state file was written; the state
# file still names the private directory of a broker that already exited.
# Then it appends the credential_broker_summary record of the saved ledger,
# or status=unreadable when the ledger cannot be read. The seal signs the last
# session record, so a caller stops the broker before it seals. A third
# argument no-audit appends nothing.
stop_credential_broker() {
  local profile="$1"
  local session="$2"
  local audit="${3:-audit}"
  local record="" pid="" dir=""
  local records="credential_broker_summary status=unreadable"
  local -a fields=()

  record="$(profile_sessions_dir_path "${profile}")/${session}.broker" || {
    echo "workcell: warning: could not find the credential broker state of ${session}." >&2
    record=""
  }
  if [[ -n "${record}" && -f "${record}" ]]; then
    read -r pid dir <"${record}" || dir=""
  fi
  credential_broker_find "${session}" ||
    echo "workcell: warning: could not list processes to find the credential broker of ${session}." >&2
  [[ -n "${CREDENTIAL_BROKER_FOUND_PID}" || -n "${dir}" ]] || return 0
  [[ -z "${CREDENTIAL_BROKER_FOUND_DIR}" ]] || dir="${CREDENTIAL_BROKER_FOUND_DIR}"
  credential_broker_terminate "${profile}" "${CREDENTIAL_BROKER_FOUND_PID}" \
    "$(credential_broker_guest_socket "${session}")" "${dir}" "${record}"
  [[ -z "${record}" ]] || rm -f "${record}"
  [[ "${audit}" != "no-audit" ]] || return 0
  if [[ -n "${record}" ]] && ! records="$(go_hostutil helper credential-broker-audit-records "${record}.jsonl")"; then
    records="credential_broker_summary status=unreadable"
  fi
  while read -r -a fields; do
    append_session_control_audit_record "${profile}" "${session}" "${fields[@]}" ||
      echo "workcell: warning: could not audit the credential broker ledger for ${session}." >&2
  done <<<"${records}"
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
  WORKCELL_BROKER_TOKEN="${CREDENTIAL_BROKER_TOKEN:-}" run_workcell_docker_client_command "${HOST_DOCKER_BIN}" create \
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
    ${EGRESS_PROXY_BROKER_DOCKER_ARGS[@]+"${EGRESS_PROXY_BROKER_DOCKER_ARGS[@]}"} \
    "${image_id}" -allow "${ALLOW_ENDPOINTS}" -listen "${subnet}" \
    ${EGRESS_PROXY_BROKER_PROXY_ARGS[@]+"${EGRESS_PROXY_BROKER_PROXY_ARGS[@]}"} >/dev/null || return 1
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
  CREDENTIAL_BROKER_TOKEN=""
  # The sidecar wrote the session CA before it listened. The host helper checks
  # it as untrusted input and publishes it into the bundle for the agent, with
  # the system roots of the verified image from the read-only sidecar.
  if [[ -n "${CREDENTIAL_BROKER_DIR:-}" ]]; then
    run_workcell_docker_client_command "${HOST_DOCKER_BIN}" exec "${EGRESS_PROXY_CONTAINER}" \
      cat /run/workcell-egress/ca.pem >"${CREDENTIAL_BROKER_DIR}/ca.pem" || return 1
    run_workcell_docker_client_command "${HOST_DOCKER_BIN}" exec "${EGRESS_PROXY_CONTAINER}" \
      cat /etc/ssl/certs/ca-certificates.crt >"${CREDENTIAL_BROKER_DIR}/roots.pem" || return 1
    go_hostutil helper publish-egress-ca "${CREDENTIAL_BROKER_DIR}/ca.pem" "${CREDENTIAL_BROKER_DIR}/roots.pem" \
      "${INJECTION_BUNDLE_ROOT}" || return 1
    rm -f "${CREDENTIAL_BROKER_DIR}/ca.pem" "${CREDENTIAL_BROKER_DIR}/roots.pem"
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
# directory, and session delete removes the file with the record. The save
# stages the logs in a private directory and publishes them through the same
# staged, fsynced, owner-only rename as the other session captures, so a failed
# or partial collection never replaces the final file. A failed save warns and
# the cleanup still runs: a live sidecar that holds the allowlist is worse than
# a missing log. It runs on every exit path, including after a failed start.
#
# Then it appends the deny log to the session audit chain: the Go helper checks
# each untrusted line and prints bounded egress_deny records and one
# egress_summary record. A failed save or read appends
# egress_summary status=unreadable. The seal signs the last session record, so
# a caller stops the proxy before it seals. Session delete passes no-audit: the
# session and its audit go away, so it appends nothing.
stop_egress_proxy() {
  local profile="$1"
  local audit="${2:-audit}"
  local deny_file=""
  local staged_dir=""
  local records="egress_summary status=unreadable"
  local -a record=()

  [[ -n "${EGRESS_PROXY_CONTAINER:-}" ]] || return 0
  deny_file="$(profile_sessions_dir_path "${profile}")/${SESSION_ID}.egress-deny.jsonl"
  staged_dir="$(mktemp -d "${TMPDIR:-/tmp}/workcell-egress-deny.XXXXXX")" || staged_dir=""
  if [[ -z "${staged_dir}" ]] ||
    ! run_profile_docker_command "${profile}" logs "${EGRESS_PROXY_CONTAINER}" \
      >"${staged_dir}/file" 2>/dev/null ||
    ! go_hostutil helper publish-session-capture-file "${staged_dir}/file" "${deny_file}" >/dev/null 2>&1; then
    echo "workcell: warning: could not save the egress proxy deny log for ${SESSION_ID}." >&2
  elif ! records="$(go_hostutil helper egress-deny-audit-records "${deny_file}")"; then
    records="egress_summary status=unreadable"
  fi
  [[ -z "${staged_dir}" ]] || rm -rf "${staged_dir}"
  stop_credential_broker "${profile}" "${SESSION_ID}" "${audit}"
  [[ "${audit}" == "no-audit" ]] || while read -r -a record; do
    append_session_control_audit_record "${profile}" "${SESSION_ID}" "${record[@]}" ||
      echo "workcell: warning: could not audit the egress proxy deny log for ${SESSION_ID}." >&2
  done <<<"${records}"
  run_profile_docker_command "${profile}" rm -f "${EGRESS_PROXY_CONTAINER}" >/dev/null 2>&1 ||
    echo "workcell: warning: could not remove the egress proxy sidecar ${EGRESS_PROXY_CONTAINER}; session delete removes it." >&2
  run_profile_docker_command "${profile}" network rm "${EGRESS_PROXY_NETWORK}" >/dev/null 2>&1 ||
    echo "workcell: warning: could not remove the egress proxy network ${EGRESS_PROXY_NETWORK}; session delete removes it." >&2
}

# egress_proxy_network_for_session prints the proxy network name of a session
# when that network exists, and nothing when it does not. It changes nothing,
# so a delete preview can use it. A failed lookup is not proof of absence: it
# fails, so the sidecar is never silently left behind.
egress_proxy_network_for_session() {
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
  ! grep -qx -- "${EGRESS_PROXY_NETWORK}" <<<"${networks}" || printf '%s\n' "${EGRESS_PROXY_NETWORK}"
}

# stop_egress_proxy_for_session runs the same save and cleanup for a session
# whose monitor died before its own cleanup ran. The names derive from the
# session id, so the stop command needs no monitor state. A session that never
# started a proxy has no such network and is left alone. A third argument
# no-audit passes to stop_egress_proxy.
stop_egress_proxy_for_session() {
  local profile="$1"
  local SESSION_ID="$2"
  local EGRESS_PROXY_NETWORK="" EGRESS_PROXY_CONTAINER=""
  local network=""

  network="$(egress_proxy_network_for_session "${profile}" "${SESSION_ID}")" || return 1
  if [[ -z "${network}" ]]; then
    # A launch that died after the broker start and before the network
    # create leaves only the broker.
    stop_credential_broker "${profile}" "${SESSION_ID}" "${3:-audit}"
    return 0
  fi
  egress_proxy_names
  stop_egress_proxy "${profile}" "${3:-audit}"
}
