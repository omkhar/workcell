#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MANIFEST_PATH="${WORKCELL_WORKFLOW_LANE_MANIFEST_PATH:-${ROOT_DIR}/policy/workflow-lanes.json}"
POLICY_PATH="${WORKCELL_WORKFLOW_LANE_POLICY_PATH:-${ROOT_DIR}/policy/workflow-lane-policy.json}"

cd "${ROOT_DIR}"
go run ./cmd/workcell-metadatautil verify-workflow-lane-manifest "${ROOT_DIR}" "${POLICY_PATH}" "${MANIFEST_PATH}"
