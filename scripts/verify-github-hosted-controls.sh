#!/bin/bash -p
set -euo pipefail

ROOT_DIR="$(CDPATH='' cd -- "${BASH_SOURCE[0]%/*}/.." && pwd -P)"
# shellcheck source=scripts/lib/canonical-build-env.sh
source "${ROOT_DIR}/scripts/lib/canonical-build-env.sh"
workcell_require_modern_privileged_bash "$@"
workcell_require_canonical_build_environment
POLICY_PATH="${WORKCELL_GITHUB_HOSTED_CONTROLS_POLICY_PATH:-${ROOT_DIR}/policy/github-hosted-controls.toml}"
# shellcheck source=scripts/lib/go-run-env.sh
source "${ROOT_DIR}/scripts/lib/go-run-env.sh"

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

require_tool gh
require_tool jq

REPO="${1:-}"
if [[ -z "${REPO}" ]]; then
  REPO="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
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

gh api "repos/${REPO}" >"${TMP_DIR}/repo.json"
gh api "repos/${REPO}/actions/permissions" >"${TMP_DIR}/actions-permissions.json"
if gh api "repos/${REPO}/actions/permissions/selected-actions" >"${TMP_DIR}/actions-selected-actions.json" 2>"${TMP_DIR}/actions-selected-actions.err"; then
  :
else
  status="$(jq -r '.status // empty' "${TMP_DIR}/actions-selected-actions.json" 2>/dev/null || true)"
  if [[ "${status}" != "409" ]]; then
    cat "${TMP_DIR}/actions-selected-actions.err" >&2
    cat "${TMP_DIR}/actions-selected-actions.json" >&2
    exit 1
  fi
fi
gh api "repos/${REPO}/actions/permissions/workflow" >"${TMP_DIR}/actions-workflow-permissions.json"
gh api "repos/${REPO}/immutable-releases" >"${TMP_DIR}/immutable-releases.json"
gh api --paginate "repos/${REPO}/actions/variables?per_page=100" |
  jq -s '{total_count: (map(.total_count // 0) | max // 0), variables: (map(.variables // []) | add)}' >"${TMP_DIR}/actions-variables.json"
gh api "repos/${REPO}/collaborators?affiliation=direct&per_page=100" >"${TMP_DIR}/collaborators-direct.json"
gh api "repos/${REPO}/rulesets" >"${TMP_DIR}/rulesets-summary.json"
"${CITOOLS_BIN}" fetch-rulesets "${TMP_DIR}" "${REPO}"
gh api --paginate "repos/${REPO}/environments?per_page=100" |
  jq -s '{total_count: (map(.total_count // 0) | max // 0), environments: (map(.environments // []) | add)}' >"${TMP_DIR}/environments.json"
if gh api "repos/${REPO}/environments/release" >"${TMP_DIR}/environment-release.json" 2>/dev/null; then
  :
else
  echo "Missing required release environment on ${REPO}" >&2
  exit 1
fi
while IFS= read -r environment_name; do
  [[ -n "${environment_name}" ]] || continue
  encoded_environment_name="$(jq -rn --arg value "${environment_name}" '$value | @uri')"
  safe_environment_name="${encoded_environment_name}"
  if gh api "repos/${REPO}/environments/${encoded_environment_name}" >"${TMP_DIR}/environment-${safe_environment_name}.json" 2>/dev/null; then
    :
  else
    echo "Missing required ${environment_name} environment on ${REPO}" >&2
    exit 1
  fi
  gh api --paginate "repos/${REPO}/environments/${encoded_environment_name}/deployment-branch-policies?per_page=100" |
    jq -s '{total_count: (map(.total_count // 0) | max // 0), branch_policies: (map(.branch_policies // []) | add)}' >"${TMP_DIR}/environment-${safe_environment_name}-deployment-branch-policies.json"
  gh api --paginate "repos/${REPO}/environments/${encoded_environment_name}/variables?per_page=100" |
    jq -s '{total_count: (map(.total_count // 0) | max // 0), variables: (map(.variables // []) | add)}' >"${TMP_DIR}/environment-${safe_environment_name}-variables.json"
  gh api --paginate "repos/${REPO}/environments/${encoded_environment_name}/secrets?per_page=100" |
    jq -s '{total_count: (map(.total_count // 0) | max // 0), secrets: (map(.secrets // []) | add)}' >"${TMP_DIR}/environment-${safe_environment_name}-secrets.json"
done < <("${CITOOLS_BIN}" list-hosted-control-environments "${POLICY_PATH}")

"${CITOOLS_BIN}" verify-github-hosted-controls "${TMP_DIR}" "${REPO}" "${POLICY_PATH}"
