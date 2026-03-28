#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO="${1:-}"
REQUIRED_MODE="${WORKCELL_HOSTED_CONTROLS_REQUIRED:-0}"
WORKFLOW_TOKEN="${WORKCELL_HOSTED_CONTROLS_TOKEN:-}"

if [[ "${REQUIRED_MODE}" != "0" && "${REQUIRED_MODE}" != "1" ]]; then
  echo "WORKCELL_HOSTED_CONTROLS_REQUIRED must be 0 or 1." >&2
  exit 1
fi

if [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then
  if [[ -n "${WORKFLOW_TOKEN}" ]]; then
    GH_TOKEN="${WORKFLOW_TOKEN}" \
      "${ROOT_DIR}/scripts/verify-github-hosted-controls.sh" "${REPO}"
    exit 0
  fi

  if [[ "${REQUIRED_MODE}" == "1" ]]; then
    echo "Hosted-controls verification requires WORKCELL_HOSTED_CONTROLS_TOKEN in GitHub Actions." >&2
    echo "Configure a fine-grained token or GitHub App token that can read repository administration metadata for this repository." >&2
    exit 1
  fi

  echo "Skipping hosted-controls verification in GitHub Actions because WORKCELL_HOSTED_CONTROLS_TOKEN is not configured." >&2
  echo "github.token cannot read the rulesets/collaborators/environment metadata this audit requires on this repository." >&2
  exit 0
fi

"${ROOT_DIR}/scripts/verify-github-hosted-controls.sh" "${REPO}"
