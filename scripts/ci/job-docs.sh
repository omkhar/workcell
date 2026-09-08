#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
source "${ROOT_DIR}/scripts/ci/lib/local-docker-parity.sh"
VALIDATOR_IMAGE="${WORKCELL_VALIDATOR_IMAGE:-}"
VALIDATOR_IMAGE_INPUT="${WORKCELL_VALIDATOR_IMAGE:-}"
VALIDATOR_IMAGE_OWNED=0
VALIDATOR_IMAGE_RESERVATION=""

cleanup() {
  local status=$?
  if [[ "${VALIDATOR_IMAGE_OWNED}" -eq 1 ]]; then
    cleanup_workcell_owned_validator_image "${VALIDATOR_IMAGE}" "${VALIDATOR_IMAGE_RESERVATION}"
  fi
  cleanup_workcell_ci_docker
  return "${status}"
}
trap cleanup EXIT

if [[ -z "${VALIDATOR_IMAGE_INPUT}" ]]; then
  claim_workcell_validator_image "${ROOT_DIR}" VALIDATOR_IMAGE VALIDATOR_IMAGE_RESERVATION
  VALIDATOR_IMAGE_OWNED=1
  export WORKCELL_VALIDATOR_IMAGE="${VALIDATOR_IMAGE}"
fi

echo "[ci/docs] pinned input policy"
"${ROOT_DIR}/scripts/check-pinned-inputs.sh"

echo "[ci/docs] support-matrix field parity"
"${ROOT_DIR}/scripts/check-doc-support-matrix-fields.sh"

echo "[ci/docs] public contract drift check"
"${ROOT_DIR}/scripts/check-public-contract.sh"

echo "[ci/docs] documentation language rules"
"${ROOT_DIR}/scripts/check-doc-language.sh"

echo "[ci/docs] markdown link and orphan check"
"${ROOT_DIR}/scripts/check-doc-links.sh"

echo "[ci/docs] validator image build"
BUILT_VALIDATOR_IMAGE="$("${ROOT_DIR}/scripts/ci/build-validator-image.sh")"
if [[ "${BUILT_VALIDATOR_IMAGE}" != "${VALIDATOR_IMAGE}" ]]; then
  echo "Validator image builder returned an unexpected reference" >&2
  exit 1
fi
export WORKCELL_VALIDATOR_IMAGE="${VALIDATOR_IMAGE}"

echo "[ci/docs] spelling and manpage"
"${ROOT_DIR}/scripts/ci/run-docs-in-validator.sh"

echo "Workcell shared docs job passed."
