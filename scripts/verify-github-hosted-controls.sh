#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
POLICY_PATH="${WORKCELL_GITHUB_HOSTED_CONTROLS_POLICY_PATH:-${ROOT_DIR}/policy/github-hosted-controls.toml}"
# shellcheck source=/dev/null
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
METADATAUTIL_BIN=""
cleanup() {
  rm -rf "${TMP_DIR}"
  if [[ -n "${METADATAUTIL_BIN}" && -e "${METADATAUTIL_BIN}" ]]; then
    rm -f "${METADATAUTIL_BIN}"
  fi
}
trap cleanup EXIT

METADATAUTIL_BIN="$(mktemp "${TMPDIR:-/tmp}/workcell-metadatautil.XXXXXX")"
build_go_tool_in_repo "${ROOT_DIR}" "${METADATAUTIL_BIN}" ./cmd/workcell-metadatautil

gh api "repos/${REPO}" >"${TMP_DIR}/repo.json"
gh api "repos/${REPO}/actions/permissions" >"${TMP_DIR}/actions-permissions.json"
gh api --paginate "repos/${REPO}/actions/variables?per_page=100" |
  jq -s '{total_count: (map(.total_count // 0) | max // 0), variables: (map(.variables // []) | add)}' >"${TMP_DIR}/actions-variables.json"
gh api "repos/${REPO}/collaborators?affiliation=direct&per_page=100" >"${TMP_DIR}/collaborators-direct.json"
gh api "repos/${REPO}/rulesets" >"${TMP_DIR}/rulesets-summary.json"
"${METADATAUTIL_BIN}" fetch-rulesets "${TMP_DIR}" "${REPO}"
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
  gh api --paginate "repos/${REPO}/environments/${encoded_environment_name}/variables?per_page=100" |
    jq -s '{total_count: (map(.total_count // 0) | max // 0), variables: (map(.variables // []) | add)}' >"${TMP_DIR}/environment-${safe_environment_name}-variables.json"
  gh api --paginate "repos/${REPO}/environments/${encoded_environment_name}/secrets?per_page=100" |
    jq -s '{total_count: (map(.total_count // 0) | max // 0), secrets: (map(.secrets // []) | add)}' >"${TMP_DIR}/environment-${safe_environment_name}-secrets.json"
done < <("${METADATAUTIL_BIN}" list-hosted-control-environments "${POLICY_PATH}")

"${METADATAUTIL_BIN}" verify-github-hosted-controls "${TMP_DIR}" "${REPO}" "${POLICY_PATH}"
