#!/bin/bash -p
set -euo pipefail

AMBIENT_TOKEN="${GH_TOKEN:-${GITHUB_TOKEN:-}}"
export -n AMBIENT_TOKEN 2>/dev/null || true
unset WORKCELL_HOSTED_CONTROLS_TOKEN WORKCELL_GITHUB_API_TOKEN WORKCELL_GITHUB_API_TOKEN_FILE
unset GH_TOKEN GITHUB_TOKEN GH_ENTERPRISE_TOKEN GITHUB_ENTERPRISE_TOKEN
unset AUDIT_TOKEN
if [[ "${1:-}" != "--token-stdin" && -n "${AMBIENT_TOKEN}" ]]; then
  if printf '%s\n' "${AMBIENT_TOKEN}" | "$0" --token-stdin "$@"; then
    exit 0
  else
    relay_status=("${PIPESTATUS[@]}")
    exit "${relay_status[1]}"
  fi
fi

if [[ -n "${WORKCELL_GO_BIN:-}" ]]; then
  echo "GitHub-hosted controls reject ambient WORKCELL_GO_BIN." >&2
  exit 2
fi
readonly TRUSTED_PATH="/opt/homebrew/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
export PATH="${TRUSTED_PATH}"
# shellcheck source=scripts/lib/canonical-build-env.sh
source "${BASH_SOURCE[0]%/*}/lib/canonical-build-env.sh"
workcell_require_modern_privileged_bash "$@"

TOKEN_STDIN=0
if [[ "${1:-}" == "--token-stdin" ]]; then
  TOKEN_STDIN=1
  shift
fi
if [[ $# -gt 1 ]]; then
  echo "GitHub-hosted controls accept only one OWNER/REPO argument." >&2
  exit 2
fi
AUDIT_TOKEN=""
export -n AUDIT_TOKEN 2>/dev/null || true
if ((TOKEN_STDIN == 1)); then
  if ! IFS= read -r -n 4097 AUDIT_TOKEN; then
    echo "GitHub-hosted controls could not read the audit token." >&2
    exit 2
  fi
  trailing=""
  if IFS= read -r -n 1 trailing || [[ -n "${trailing}" ]]; then
    echo "GitHub-hosted controls require the audit token as exactly one line." >&2
    exit 2
  fi
  if [[ -z "${AUDIT_TOKEN}" || "${#AUDIT_TOKEN}" -gt 4096 || "${AUDIT_TOKEN}" =~ [[:space:][:cntrl:]] ]]; then
    echo "GitHub-hosted controls require one bounded non-empty audit token line." >&2
    exit 2
  fi
fi
exec </dev/null

ROOT_DIR="$(CDPATH='' cd -- "${BASH_SOURCE[0]%/*}/.." && pwd -P)"
workcell_require_canonical_build_environment
if [[ -n "${GH_HOST:-}" || -n "${GH_REPO:-}" ]]; then
  echo "Hosted controls reject ambient GH_HOST and GH_REPO." >&2
  exit 2
fi
POLICY_PATH="${WORKCELL_GITHUB_HOSTED_CONTROLS_POLICY_PATH:-${ROOT_DIR}/policy/github-hosted-controls.toml}"
# shellcheck source=scripts/lib/go-run-env.sh
source "${ROOT_DIR}/scripts/lib/go-run-env.sh"

resolve_trusted_tool() {
  local candidate=""
  for candidate in "$@"; do
    [[ -x "${candidate}" ]] || continue
    printf '%s\n' "${candidate}"
    return 0
  done
  return 1
}

resolve_trusted_go_bin() {
  local expected_toolchain=""
  local actual_toolchain=""
  local toolchain_arch=""
  local candidate=""
  expected_toolchain="$(/usr/bin/awk '$1 == "toolchain" { print $2; exit }' "${ROOT_DIR}/go.mod")"
  [[ "${expected_toolchain}" =~ ^go[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
  case "$(/usr/bin/uname -m)" in
    arm64 | aarch64) toolchain_arch="arm64" ;;
    x86_64 | amd64) toolchain_arch="x64" ;;
    *) return 1 ;;
  esac
  for candidate in \
    "/opt/hostedtoolcache/go/${expected_toolchain#go}/${toolchain_arch}/bin/go" \
    /opt/homebrew/bin/go /usr/local/go/bin/go /usr/local/bin/go /usr/bin/go; do
    [[ -x "${candidate}" ]] || continue
    actual_toolchain="$(GOTOOLCHAIN=local "${candidate}" env GOVERSION 2>/dev/null)" || continue
    if [[ "${actual_toolchain}" == "${expected_toolchain}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done
  return 1
}

readonly GITHUB_API_VERSION="2026-03-10"
github_api() {
  if [[ -n "${AUDIT_TOKEN}" ]]; then
    GH_TOKEN="${AUDIT_TOKEN}" "${GH_BIN}" api --hostname github.com -H "X-GitHub-Api-Version: ${GITHUB_API_VERSION}" "$@"
    return
  fi
  "${GH_BIN}" api --hostname github.com -H "X-GitHub-Api-Version: ${GITHUB_API_VERSION}" "$@"
}

github_repo_view() {
  if [[ -n "${AUDIT_TOKEN}" ]]; then
    GH_TOKEN="${AUDIT_TOKEN}" "${GH_BIN}" repo view --json nameWithOwner --jq .nameWithOwner
    return
  fi
  "${GH_BIN}" repo view --json nameWithOwner --jq .nameWithOwner
}

run_citools() {
  "${CITOOLS_BIN}" "$@"
}

GO_BIN="$(resolve_trusted_go_bin)" || {
  echo "The exact Go toolchain from go.mod is unavailable at a trusted path." >&2
  exit 1
}
export -n GO_BIN 2>/dev/null || true
WORKCELL_GO_BIN="${GO_BIN}"
export -n WORKCELL_GO_BIN 2>/dev/null || true
GH_BIN="$(resolve_trusted_tool /opt/homebrew/bin/gh /usr/local/bin/gh /usr/bin/gh)" || {
  echo "Missing trusted gh tool." >&2
  exit 1
}
JQ_BIN="$(resolve_trusted_tool /opt/homebrew/bin/jq /usr/local/bin/jq /usr/bin/jq)" || {
  echo "Missing trusted jq tool." >&2
  exit 1
}
readonly GO_BIN GH_BIN JQ_BIN

REPO="${1:-}"
if [[ -z "${REPO}" ]]; then
  REPO="$(github_repo_view)"
fi
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-gh-controls.XXXXXX")"
CITOOLS_BIN=""
cleanup() {
  rm -rf "${TMP_DIR}"
  if [[ -n "${CITOOLS_BIN}" && -e "${CITOOLS_BIN}" ]]; then
    rm -f "${CITOOLS_BIN}"
  fi
}
trap cleanup EXIT
CITOOLS_BIN="$(mktemp "${TMPDIR:-/tmp}/workcell-citools.XXXXXX")"
build_go_tool_in_repo "${ROOT_DIR}" "${CITOOLS_BIN}" ./cmd/workcell-citools

github_api "repos/${REPO}" >"${TMP_DIR}/repo.json"
github_api "repos/${REPO}/actions/permissions" >"${TMP_DIR}/actions-permissions.json"
if github_api "repos/${REPO}/actions/permissions/selected-actions" >"${TMP_DIR}/actions-selected-actions.json" 2>"${TMP_DIR}/actions-selected-actions.err"; then
  :
else
  status="$("${JQ_BIN}" -r '.status // empty' "${TMP_DIR}/actions-selected-actions.json" 2>/dev/null || true)"
  if [[ "${status}" != "409" ]]; then
    cat "${TMP_DIR}/actions-selected-actions.err" >&2
    cat "${TMP_DIR}/actions-selected-actions.json" >&2
    exit 1
  fi
fi
github_api "repos/${REPO}/actions/permissions/workflow" >"${TMP_DIR}/actions-workflow-permissions.json"
github_api "repos/${REPO}/immutable-releases" >"${TMP_DIR}/immutable-releases.json"
github_api --paginate "repos/${REPO}/actions/variables?per_page=100" |
  run_citools merge-hosted-control-object-pages variables >"${TMP_DIR}/actions-variables.json"
github_api "repos/${REPO}/collaborators?affiliation=direct&per_page=100" >"${TMP_DIR}/collaborators-direct.json"
github_api --paginate "repos/${REPO}/rulesets?per_page=100" |
  run_citools merge-hosted-control-array-pages >"${TMP_DIR}/rulesets-summary.json"
run_citools list-hosted-control-ruleset-ids "${TMP_DIR}/rulesets-summary.json" >"${TMP_DIR}/ruleset-ids"
: >"${TMP_DIR}/rulesets-details.jsons"
while IFS= read -r ruleset_id; do
  github_api "repos/${REPO}/rulesets/${ruleset_id}" |
    run_citools normalize-hosted-control-ruleset "${ruleset_id}" >>"${TMP_DIR}/rulesets-details.jsons"
done <"${TMP_DIR}/ruleset-ids"
run_citools assemble-hosted-control-rulesets "${TMP_DIR}/rulesets-summary.json" "${TMP_DIR}/rulesets-details.jsons" "${TMP_DIR}/rulesets.json"
github_api --paginate "repos/${REPO}/environments?per_page=100" |
  run_citools merge-hosted-control-object-pages environments >"${TMP_DIR}/environments.json"
if github_api "repos/${REPO}/environments/release" >"${TMP_DIR}/environment-release.json" 2>/dev/null; then
  :
else
  echo "Missing required release environment on ${REPO}" >&2
  exit 1
fi
while IFS= read -r environment_name; do
  [[ -n "${environment_name}" ]] || continue
  encoded_environment_name="$("${JQ_BIN}" -rn --arg value "${environment_name}" "\$value | @uri")"
  safe_environment_name="${encoded_environment_name}"
  if github_api "repos/${REPO}/environments/${encoded_environment_name}" >"${TMP_DIR}/environment-${safe_environment_name}.json" 2>/dev/null; then
    :
  else
    echo "Missing required ${environment_name} environment on ${REPO}" >&2
    exit 1
  fi
  github_api --paginate "repos/${REPO}/environments/${encoded_environment_name}/deployment-branch-policies?per_page=100" |
    run_citools merge-hosted-control-object-pages branch_policies >"${TMP_DIR}/environment-${safe_environment_name}-deployment-branch-policies.json"
  github_api --paginate "repos/${REPO}/environments/${encoded_environment_name}/variables?per_page=100" |
    run_citools merge-hosted-control-object-pages variables >"${TMP_DIR}/environment-${safe_environment_name}-variables.json"
  github_api --paginate "repos/${REPO}/environments/${encoded_environment_name}/secrets?per_page=100" |
    run_citools merge-hosted-control-object-pages secrets >"${TMP_DIR}/environment-${safe_environment_name}-secrets.json"
done < <(run_citools list-hosted-control-environments "${POLICY_PATH}")
run_citools verify-github-hosted-controls "${TMP_DIR}" "${REPO}" "${POLICY_PATH}"
