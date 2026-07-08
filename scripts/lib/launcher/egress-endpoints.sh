#!/usr/bin/env -S BASH_ENV= ENV= bash
# shellcheck shell=bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 Omkhar Arasaratnam
#
# scripts/lib/launcher/egress-endpoints.sh — egress-endpoint assembly module
# extracted from scripts/workcell as the next increment of the launcher
# decomposition (roadmap item D4, "wrapper assembly").  These helpers compute
# the per-session network egress allowlist and translate it into the container
# runtime's `--add-host` arguments: they map each provider/target/credential to
# its fixed set of `host:port` endpoints, dedupe and deny-subtract the combined
# list (via the workcell-hostutil Go helper), fail closed when a deny rule
# empties the allowlist, label whether the launch actually enforces the
# allowlist, and resolve the surviving endpoints into `--add-host` runtime
# args.  They depend only on `csv_contains_value` (defined in scripts/workcell),
# `go_hostutil` (scripts/lib/launcher/go-hostutil.sh, sourced before this
# module), the read-only launch-state globals `AGENT`,
# `INJECTION_CREDENTIAL_KEYS`, `NETWORK_POLICY`, and `TARGET_BACKEND`, and the
# `RUNTIME_NETWORK_ARGS` array `build_runtime_host_aliases` populates — every
# dependency is defined before the first call site in the main launch path — so
# they are a self-contained, behaviour-preserving unit.  See
# docs/launcher-contract.md for the module contract.

provider_endpoints() {
  case "$1" in
    codex)
      echo "api.openai.com:443 auth.openai.com:443 chatgpt.com:443"
      ;;
    claude)
      echo "api.anthropic.com:443 claude.ai:443 console.anthropic.com:443"
      ;;
    copilot)
      echo "api.githubcopilot.com:443 api.individual.githubcopilot.com:443 api.github.com:443 github.com:443"
      ;;
    gemini)
      echo "generativelanguage.googleapis.com:443 ai.google.dev:443"
      ;;
    *)
      return 1
      ;;
  esac
}

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
  if [[ "${TARGET_BACKEND}" == "colima" ]] && [[ "${NETWORK_POLICY}" == "allowlist" ]]; then
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

  while IFS=$'\t' read -r host ip; do
    [[ -n "${host}" ]] || continue
    if [[ "${ip}" == *:* ]]; then
      RUNTIME_NETWORK_ARGS+=(--add-host "${host}:[${ip}]")
    else
      RUNTIME_NETWORK_ARGS+=(--add-host "${host}:${ip}")
    fi
  done < <(go_hostutil helper resolve-endpoints "${endpoint_list}")

}
