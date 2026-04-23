#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
source "${ROOT_DIR}/scripts/ci/lib/local-docker-parity.sh"
VALIDATOR_IMAGE=""
VALIDATOR_IMAGE_INPUT="${WORKCELL_VALIDATOR_IMAGE:-}"

cleanup() {
  if [[ -z "${VALIDATOR_IMAGE_INPUT}" ]]; then
    cleanup_workcell_validator_image "${VALIDATOR_IMAGE:-}"
  fi
  cleanup_workcell_ci_docker
}
trap cleanup EXIT

echo "[ci/docs] pinned input policy"
"${ROOT_DIR}/scripts/check-pinned-inputs.sh"

echo "[ci/docs] validator image build"
VALIDATOR_IMAGE="$("${ROOT_DIR}/scripts/ci/build-validator-image.sh")"
export WORKCELL_VALIDATOR_IMAGE="${VALIDATOR_IMAGE}"

echo "[ci/docs] spelling and manpage"
"${ROOT_DIR}/scripts/ci/run-docs-in-validator.sh"

echo "Workcell shared docs job passed."
