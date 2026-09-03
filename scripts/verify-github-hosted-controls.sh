#!/bin/bash -p
set -euo pipefail

ROOT_DIR="$(CDPATH='' cd -- "${BASH_SOURCE[0]%/*}/.." && pwd -P)"
# shellcheck source=scripts/lib/canonical-build-env.sh
source "${ROOT_DIR}/scripts/lib/canonical-build-env.sh"
workcell_require_modern_privileged_bash "$@"
workcell_require_canonical_build_environment
if [[ -n "${GH_HOST:-}" || -n "${GH_REPO:-}" ]]; then
  echo "Hosted controls reject ambient GH_HOST and GH_REPO." >&2
  exit 2
fi
POLICY_PATH="${WORKCELL_GITHUB_HOSTED_CONTROLS_POLICY_PATH:-${ROOT_DIR}/policy/github-hosted-controls.toml}"
# shellcheck source=scripts/lib/go-run-env.sh
source "${ROOT_DIR}/scripts/lib/go-run-env.sh"

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

readonly GITHUB_API_VERSION="2026-03-10"
github_api() {
  gh api --hostname github.com -H "X-GitHub-Api-Version: ${GITHUB_API_VERSION}" "$@"
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

github_api "repos/${REPO}" >"${TMP_DIR}/repo.json"
github_api "repos/${REPO}/actions/permissions" >"${TMP_DIR}/actions-permissions.json"
if github_api "repos/${REPO}/actions/permissions/selected-actions" >"${TMP_DIR}/actions-selected-actions.json" 2>"${TMP_DIR}/actions-selected-actions.err"; then
  :
else
  status="$(jq -r '.status // empty' "${TMP_DIR}/actions-selected-actions.json" 2>/dev/null || true)"
  if [[ "${status}" != "409" ]]; then
    cat "${TMP_DIR}/actions-selected-actions.err" >&2
    cat "${TMP_DIR}/actions-selected-actions.json" >&2
    exit 1
  fi
fi
github_api "repos/${REPO}/actions/permissions/workflow" >"${TMP_DIR}/actions-workflow-permissions.json"
github_api "repos/${REPO}/immutable-releases" >"${TMP_DIR}/immutable-releases.json"
github_api --paginate "repos/${REPO}/actions/variables?per_page=100" |
  "${CITOOLS_BIN}" merge-hosted-control-object-pages variables >"${TMP_DIR}/actions-variables.json"
github_api "repos/${REPO}/collaborators?affiliation=direct&per_page=100" >"${TMP_DIR}/collaborators-direct.json"
github_api --paginate "repos/${REPO}/rulesets?per_page=100" |
  "${CITOOLS_BIN}" merge-hosted-control-array-pages >"${TMP_DIR}/rulesets-summary.json"
"${CITOOLS_BIN}" fetch-rulesets "${TMP_DIR}" "${REPO}"
github_api --paginate "repos/${REPO}/environments?per_page=100" |
  "${CITOOLS_BIN}" merge-hosted-control-object-pages environments >"${TMP_DIR}/environments.json"
if github_api "repos/${REPO}/environments/release" >"${TMP_DIR}/environment-release.json" 2>/dev/null; then
  :
else
  echo "Missing required release environment on ${REPO}" >&2
  exit 1
fi
while IFS= read -r environment_name; do
  [[ -n "${environment_name}" ]] || continue
  encoded_environment_name="$(jq -rn --arg value "${environment_name}" '$value | @uri')"
  safe_environment_name="${encoded_environment_name}"
  if github_api "repos/${REPO}/environments/${encoded_environment_name}" >"${TMP_DIR}/environment-${safe_environment_name}.json" 2>/dev/null; then
    :
  else
    echo "Missing required ${environment_name} environment on ${REPO}" >&2
    exit 1
  fi
  github_api --paginate "repos/${REPO}/environments/${encoded_environment_name}/deployment-branch-policies?per_page=100" |
    "${CITOOLS_BIN}" merge-hosted-control-object-pages branch_policies >"${TMP_DIR}/environment-${safe_environment_name}-deployment-branch-policies.json"
  github_api --paginate "repos/${REPO}/environments/${encoded_environment_name}/variables?per_page=100" |
    "${CITOOLS_BIN}" merge-hosted-control-object-pages variables >"${TMP_DIR}/environment-${safe_environment_name}-variables.json"
  github_api --paginate "repos/${REPO}/environments/${encoded_environment_name}/secrets?per_page=100" |
    "${CITOOLS_BIN}" merge-hosted-control-object-pages secrets >"${TMP_DIR}/environment-${safe_environment_name}-secrets.json"
done < <("${CITOOLS_BIN}" list-hosted-control-environments "${POLICY_PATH}")

"${CITOOLS_BIN}" verify-github-hosted-controls "${TMP_DIR}" "${REPO}" "${POLICY_PATH}"
