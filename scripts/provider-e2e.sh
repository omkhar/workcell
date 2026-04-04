#!/bin/bash -p

readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  exec /usr/bin/env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME=/tmp \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_E2E_CLAUDE_API_KEY="${WORKCELL_E2E_CLAUDE_API_KEY-}" \
    WORKCELL_E2E_CLAUDE_AUTH_JSON="${WORKCELL_E2E_CLAUDE_AUTH_JSON-}" \
    WORKCELL_E2E_CLAUDE_MCP_JSON="${WORKCELL_E2E_CLAUDE_MCP_JSON-}" \
    WORKCELL_E2E_CODEX_AUTH_JSON="${WORKCELL_E2E_CODEX_AUTH_JSON-}" \
    WORKCELL_E2E_GCLOUD_ADC_JSON="${WORKCELL_E2E_GCLOUD_ADC_JSON-}" \
    WORKCELL_E2E_GEMINI_ENV="${WORKCELL_E2E_GEMINI_ENV-}" \
    WORKCELL_E2E_GEMINI_OAUTH_JSON="${WORKCELL_E2E_GEMINI_OAUTH_JSON-}" \
    WORKCELL_E2E_GEMINI_PROJECTS_JSON="${WORKCELL_E2E_GEMINI_PROJECTS_JSON-}" \
    WORKCELL_PROVIDER_E2E_WORKCELL_SCRIPT="${WORKCELL_PROVIDER_E2E_WORKCELL_SCRIPT-}" \
    WORKCELL_SANITIZED_ENTRYPOINT=1 \
    /bin/bash -p "$0" "$@"
fi

set -euo pipefail
export PATH="${TRUSTED_HOST_PATH}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKCELL_SCRIPT="${WORKCELL_PROVIDER_E2E_WORKCELL_SCRIPT:-${ROOT_DIR}/scripts/workcell}"
WORKSPACE=""
AGENT=""
COLIMA_PROFILE=""
INJECTION_POLICY=""
readonly PROBE_RESPONSE_TOKEN="WORKCELL_PROVIDER_E2E_OK"
DRY_RUN=0
REQUIRE_INJECTION=0
GENERATED_POLICY=""
INJECTION_SOURCE="default-workcell-resolution"
TMP_ROOT=""
declare -a GENERATED_CREDENTIAL_KEYS=()

usage() {
  cat <<EOF
Usage: $(basename "$0") --agent codex|claude|gemini --workspace PATH [options]

Run a small provider-focused Workcell credential and launch sequence:

1. Print Workcell auth status for the selected agent
2. Seed or refresh the prepared runtime image with --prepare-only
3. Run a small provider-specific authenticated probe inside the strict runtime

Options:
  --agent <name>              Provider to exercise: codex, claude, or gemini
  --workspace <path>          Workspace to run against
  --injection-policy <path>   Optional explicit injection policy
  --colima-profile <name>     Optional managed Colima profile name
  --require-injection         Fail unless an explicit or generated injection
                              policy is available
  --dry-run                   Print the planned commands and exit
  -h, --help                  Show this help

If --injection-policy is omitted, the script will generate a temporary policy
from WORKCELL_E2E_* environment variables when they are present. Otherwise it
falls back to Workcell's ordinary default injection-policy resolution unless
--require-injection is set.
EOF
}

die() {
  echo "$*" >&2
  exit 2
}

cleanup() {
  if [[ -n "${TMP_ROOT}" ]] && [[ -d "${TMP_ROOT}" ]]; then
    rm -rf "${TMP_ROOT}"
  fi
}
trap cleanup EXIT

ensure_tmp_root() {
  if [[ -z "${TMP_ROOT}" ]]; then
    TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/workcell-provider-e2e.XXXXXX")"
  fi
}

toml_quote() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  printf '"%s"' "${value}"
}

write_secret_file() {
  local env_name="$1"
  local filename="$2"
  local required="${3:-0}"
  local value="${!env_name-}"
  local path=""

  if [[ -z "${value}" ]]; then
    if [[ "${required}" -eq 1 ]]; then
      die "Missing required provider E2E secret: ${env_name}"
    fi
    return 1
  fi

  path="${TMP_ROOT}/${filename}"
  printf '%s' "${value}" >"${path}"
  chmod 0600 "${path}"
  printf '%s\n' "${path}"
}

generate_policy_from_env() {
  local policy_path=""
  local codex_auth_path=""
  local claude_auth_path=""
  local claude_api_key_path=""
  local claude_mcp_path=""
  local gemini_env_path=""
  local gemini_oauth_path=""
  local gemini_projects_path=""
  local gcloud_adc_path=""

  if [[ -n "${INJECTION_POLICY}" ]]; then
    GENERATED_POLICY=""
    INJECTION_SOURCE="explicit"
    return 0
  fi

  GENERATED_CREDENTIAL_KEYS=()

  case "${AGENT}" in
    codex)
      [[ -n "${WORKCELL_E2E_CODEX_AUTH_JSON-}" ]] || return 0
      ensure_tmp_root
      policy_path="${TMP_ROOT}/policy.toml"
      codex_auth_path="$(write_secret_file WORKCELL_E2E_CODEX_AUTH_JSON codex-auth.json 1)"
      GENERATED_CREDENTIAL_KEYS+=("codex_auth")
      ;;
    claude)
      if [[ -z "${WORKCELL_E2E_CLAUDE_AUTH_JSON-}" ]] && [[ -z "${WORKCELL_E2E_CLAUDE_API_KEY-}" ]]; then
        return 0
      fi
      ensure_tmp_root
      policy_path="${TMP_ROOT}/policy.toml"
      claude_auth_path="$(write_secret_file WORKCELL_E2E_CLAUDE_AUTH_JSON claude-auth.json 0 || true)"
      claude_api_key_path="$(write_secret_file WORKCELL_E2E_CLAUDE_API_KEY claude-api-key.txt 0 || true)"
      claude_mcp_path="$(write_secret_file WORKCELL_E2E_CLAUDE_MCP_JSON claude-mcp.json 0 || true)"
      [[ -n "${claude_auth_path}" ]] && GENERATED_CREDENTIAL_KEYS+=("claude_auth")
      [[ -n "${claude_api_key_path}" ]] && GENERATED_CREDENTIAL_KEYS+=("claude_api_key")
      [[ -n "${claude_mcp_path}" ]] && GENERATED_CREDENTIAL_KEYS+=("claude_mcp")
      ;;
    gemini)
      if [[ -z "${WORKCELL_E2E_GEMINI_ENV-}" ]] && [[ -z "${WORKCELL_E2E_GEMINI_OAUTH_JSON-}" ]]; then
        return 0
      fi
      ensure_tmp_root
      policy_path="${TMP_ROOT}/policy.toml"
      gemini_env_path="$(write_secret_file WORKCELL_E2E_GEMINI_ENV gemini.env 0 || true)"
      gemini_oauth_path="$(write_secret_file WORKCELL_E2E_GEMINI_OAUTH_JSON gemini-oauth.json 0 || true)"
      gemini_projects_path="$(write_secret_file WORKCELL_E2E_GEMINI_PROJECTS_JSON gemini-projects.json 0 || true)"
      gcloud_adc_path="$(write_secret_file WORKCELL_E2E_GCLOUD_ADC_JSON gcloud-adc.json 0 || true)"
      [[ -n "${gemini_env_path}" ]] && GENERATED_CREDENTIAL_KEYS+=("gemini_env")
      [[ -n "${gemini_oauth_path}" ]] && GENERATED_CREDENTIAL_KEYS+=("gemini_oauth")
      [[ -n "${gemini_projects_path}" ]] && GENERATED_CREDENTIAL_KEYS+=("gemini_projects")
      [[ -n "${gcloud_adc_path}" ]] && GENERATED_CREDENTIAL_KEYS+=("gcloud_adc")
      ;;
  esac

  printf 'version = 1\n\n[credentials]\n' >"${policy_path}"
  case "${AGENT}" in
    codex)
      printf 'codex_auth = %s\n' "$(toml_quote "${codex_auth_path}")" >>"${policy_path}"
      ;;
    claude)
      if [[ -n "${claude_auth_path}" ]]; then
        printf 'claude_auth = %s\n' "$(toml_quote "${claude_auth_path}")" >>"${policy_path}"
      fi
      if [[ -n "${claude_api_key_path}" ]]; then
        printf 'claude_api_key = %s\n' "$(toml_quote "${claude_api_key_path}")" >>"${policy_path}"
      fi
      if [[ -n "${claude_mcp_path}" ]]; then
        printf 'claude_mcp = %s\n' "$(toml_quote "${claude_mcp_path}")" >>"${policy_path}"
      fi
      ;;
    gemini)
      if [[ -n "${gemini_env_path}" ]]; then
        printf 'gemini_env = %s\n' "$(toml_quote "${gemini_env_path}")" >>"${policy_path}"
      fi
      if [[ -n "${gemini_oauth_path}" ]]; then
        printf 'gemini_oauth = %s\n' "$(toml_quote "${gemini_oauth_path}")" >>"${policy_path}"
      fi
      if [[ -n "${gemini_projects_path}" ]]; then
        printf 'gemini_projects = %s\n' "$(toml_quote "${gemini_projects_path}")" >>"${policy_path}"
      fi
      if [[ -n "${gcloud_adc_path}" ]]; then
        printf 'gcloud_adc = %s\n' "$(toml_quote "${gcloud_adc_path}")" >>"${policy_path}"
      fi
      ;;
  esac

  GENERATED_POLICY="${policy_path}"
  INJECTION_SOURCE="generated-env"
}

print_command() {
  local label="$1"
  shift

  printf '%s=' "${label}"
  printf '%q ' "$@"
  printf '\n'
}

require_tool() {
  command -v "$1" >/dev/null 2>&1 || die "Missing required tool: $1"
}

build_probe_prompt() {
  printf 'Reply with exactly the single token %s and nothing else.' "${PROBE_RESPONSE_TOKEN}"
}

probe_output_matches_expected_token() {
  local output="$1"

  case "${AGENT}" in
    codex)
      grep -Eq "\"text\"[[:space:]]*:[[:space:]]*\"${PROBE_RESPONSE_TOKEN}\"" <<<"${output}" ||
        grep -qxF "${PROBE_RESPONSE_TOKEN}" <<<"${output}"
      ;;
    claude)
      grep -Eq "\"(result|response|text)\"[[:space:]]*:[[:space:]]*\"${PROBE_RESPONSE_TOKEN}\"" <<<"${output}" ||
        grep -qxF "${PROBE_RESPONSE_TOKEN}" <<<"${output}"
      ;;
    gemini)
      grep -Eq "\"response\"[[:space:]]*:[[:space:]]*\"${PROBE_RESPONSE_TOKEN}\"" <<<"${output}" ||
        grep -qxF "${PROBE_RESPONSE_TOKEN}" <<<"${output}"
      ;;
  esac
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --agent)
      [[ $# -ge 2 ]] || die "Option --agent requires a value."
      AGENT="$2"
      shift 2
      ;;
    --workspace)
      [[ $# -ge 2 ]] || die "Option --workspace requires a value."
      WORKSPACE="$2"
      shift 2
      ;;
    --injection-policy)
      [[ $# -ge 2 ]] || die "Option --injection-policy requires a value."
      INJECTION_POLICY="$2"
      shift 2
      ;;
    --colima-profile)
      [[ $# -ge 2 ]] || die "Option --colima-profile requires a value."
      COLIMA_PROFILE="$2"
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    --require-injection)
      REQUIRE_INJECTION=1
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      die "Unsupported option: $1"
      ;;
  esac
done

[[ -n "${AGENT}" ]] || die "Option --agent is required."
[[ -n "${WORKSPACE}" ]] || die "Option --workspace is required."
case "${AGENT}" in
  codex | claude | gemini) ;;
  *)
    die "Unsupported agent: ${AGENT}"
    ;;
esac

require_tool /usr/bin/env
[[ -x "${WORKCELL_SCRIPT}" ]] || die "Workcell launcher is missing or not executable: ${WORKCELL_SCRIPT}"

generate_policy_from_env
if [[ "${REQUIRE_INJECTION}" -eq 1 ]] && [[ -z "${INJECTION_POLICY}" ]] && [[ -z "${GENERATED_POLICY}" ]]; then
  die "No injection policy is available. Pass --injection-policy or populate the required WORKCELL_E2E_* environment variables."
fi

declare -a common_args=("${WORKCELL_SCRIPT}" "--agent" "${AGENT}" "--workspace" "${WORKSPACE}")
if [[ -n "${COLIMA_PROFILE}" ]]; then
  common_args+=("--colima-profile" "${COLIMA_PROFILE}")
fi
if [[ -n "${INJECTION_POLICY}" ]]; then
  common_args+=("--injection-policy" "${INJECTION_POLICY}")
elif [[ -n "${GENERATED_POLICY}" ]]; then
  common_args+=("--injection-policy" "${GENERATED_POLICY}")
fi

declare -a auth_status_cmd=("${common_args[@]}" "--auth-status")
declare -a prepare_only_cmd=("${WORKCELL_SCRIPT}" "--prepare-only" "--agent" "${AGENT}" "--workspace" "${WORKSPACE}")
declare -a probe_cmd=("${WORKCELL_SCRIPT}" "--agent" "${AGENT}" "--workspace" "${WORKSPACE}")
auth_status_output=""
probe_output=""
probe_output_path=""
if [[ -n "${COLIMA_PROFILE}" ]]; then
  prepare_only_cmd+=("--colima-profile" "${COLIMA_PROFILE}")
  probe_cmd+=("--colima-profile" "${COLIMA_PROFILE}")
fi
if [[ -n "${INJECTION_POLICY}" ]]; then
  prepare_only_cmd+=("--injection-policy" "${INJECTION_POLICY}")
  probe_cmd+=("--injection-policy" "${INJECTION_POLICY}")
elif [[ -n "${GENERATED_POLICY}" ]]; then
  prepare_only_cmd+=("--injection-policy" "${GENERATED_POLICY}")
  probe_cmd+=("--injection-policy" "${GENERATED_POLICY}")
fi
case "${AGENT}" in
  codex)
    probe_cmd+=(
      "--agent-arg" "exec"
      "--agent-arg" "--json"
      "--agent-arg" "$(build_probe_prompt)"
    )
    ;;
  claude)
    probe_cmd+=(
      "--agent-arg" "-p"
      "--agent-arg" "--output-format"
      "--agent-arg" "json"
      "--agent-arg" "--no-session-persistence"
      "--agent-arg" "--max-budget-usd"
      "--agent-arg" "0.10"
      "--agent-arg" "--tools"
      "--agent-arg" ""
      "--agent-arg" "$(build_probe_prompt)"
    )
    ;;
  gemini)
    probe_cmd+=(
      "--agent-arg" "-p"
      "--agent-arg" "$(build_probe_prompt)"
      "--agent-arg" "--output-format"
      "--agent-arg" "json"
    )
    ;;
esac

if [[ "${DRY_RUN}" -eq 1 ]]; then
  printf 'provider_e2e_agent=%s\n' "${AGENT}"
  printf 'provider_e2e_workspace=%s\n' "${WORKSPACE}"
  printf 'provider_e2e_injection_source=%s\n' "${INJECTION_SOURCE}"
  if ((${#GENERATED_CREDENTIAL_KEYS[@]} > 0)); then
    printf 'provider_e2e_credential_keys=%s\n' "$(
      IFS=,
      printf '%s' "${GENERATED_CREDENTIAL_KEYS[*]}"
    )"
  else
    printf 'provider_e2e_credential_keys=\n'
  fi
  printf 'provider_e2e_steps=auth-status,prepare-only,live-probe\n'
  print_command provider_e2e_auth_status_cmd "${auth_status_cmd[@]}"
  print_command provider_e2e_prepare_only_cmd "${prepare_only_cmd[@]}"
  print_command provider_e2e_probe_cmd "${probe_cmd[@]}"
  exit 0
fi

printf '[provider-e2e] auth-status (%s)\n' "${AGENT}"
if ! auth_status_output="$("${auth_status_cmd[@]}" 2>&1)"; then
  printf '%s\n' "${auth_status_output}" >&2
  exit 1
fi
printf '%s\n' "${auth_status_output}"
if ! grep -q '^provider_auth_mode=' <<<"${auth_status_output}"; then
  die "Workcell auth-status did not report provider_auth_mode for ${AGENT}."
fi
if grep -q '^provider_auth_mode=none$' <<<"${auth_status_output}"; then
  die "Workcell did not detect provider auth for ${AGENT}."
fi
printf '[provider-e2e] prepare-only (%s)\n' "${AGENT}"
"${prepare_only_cmd[@]}"
printf '[provider-e2e] live-probe (%s)\n' "${AGENT}"
ensure_tmp_root
probe_output_path="${TMP_ROOT}/${AGENT}-probe.out"
if ! "${probe_cmd[@]}" >"${probe_output_path}" 2>&1; then
  cat "${probe_output_path}" >&2
  exit 1
fi
probe_output="$(cat "${probe_output_path}")"
printf '%s\n' "${probe_output}"
if ! probe_output_matches_expected_token "${probe_output}"; then
  die "Provider probe did not emit the expected token for ${AGENT}."
fi
