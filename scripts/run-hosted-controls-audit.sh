#!/bin/bash -p
set -euo pipefail

HOSTED_TOKEN="${WORKCELL_HOSTED_CONTROLS_TOKEN:-}"
LOCAL_TOKEN="${GH_TOKEN:-${GITHUB_TOKEN:-}}"
export -n HOSTED_TOKEN LOCAL_TOKEN 2>/dev/null || true
unset WORKCELL_HOSTED_CONTROLS_TOKEN WORKCELL_GITHUB_API_TOKEN WORKCELL_GITHUB_API_TOKEN_FILE
unset GH_TOKEN GITHUB_TOKEN GH_ENTERPRISE_TOKEN GITHUB_ENTERPRISE_TOKEN
if [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then
  WORKFLOW_TOKEN="${HOSTED_TOKEN}"
else
  WORKFLOW_TOKEN="${LOCAL_TOKEN}"
fi
export -n WORKFLOW_TOKEN 2>/dev/null || true

readonly TRUSTED_PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
export PATH="${TRUSTED_PATH}"
ROOT_DIR="$(CDPATH='' cd -- "${BASH_SOURCE[0]%/*}/.." && pwd -P)"
REPO="${1:-}"
REQUIRED_MODE="${WORKCELL_HOSTED_CONTROLS_REQUIRED:-0}"

if [[ $# -gt 1 ]]; then
  echo "run-hosted-controls-audit.sh accepts only one optional OWNER/REPO argument." >&2
  exit 2
fi
if [[ "${REQUIRED_MODE}" != "0" && "${REQUIRED_MODE}" != "1" ]]; then
  echo "WORKCELL_HOSTED_CONTROLS_REQUIRED must be 0 or 1." >&2
  exit 1
fi

if [[ "${GITHUB_ACTIONS:-}" == "true" && -z "${HOSTED_TOKEN}" ]]; then
  if [[ "${REQUIRED_MODE}" == "1" ]]; then
    echo "Hosted-controls verification requires WORKCELL_HOSTED_CONTROLS_TOKEN in GitHub Actions." >&2
    echo "Configure a fine-grained token or GitHub App token that can read repository administration metadata for this repository." >&2
    exit 1
  fi
  echo "Skipping hosted-controls verification in GitHub Actions because WORKCELL_HOSTED_CONTROLS_TOKEN is not configured." >&2
  echo "github.token cannot read the rulesets/collaborators/environment metadata this audit requires on this repository." >&2
  exit 0
fi

if [[ -n "${WORKFLOW_TOKEN}" ]]; then
  if printf '%s\n' "${WORKFLOW_TOKEN}" |
    "${ROOT_DIR}/scripts/verify-github-hosted-controls.sh" --token-stdin "${REPO}"; then
    exit 0
  else
    relay_status=("${PIPESTATUS[@]}")
    exit "${relay_status[1]}"
  fi
fi
"${ROOT_DIR}/scripts/verify-github-hosted-controls.sh" "${REPO}"
