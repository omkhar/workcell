#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

echo "[ci/docs] pinned input policy"
"${ROOT_DIR}/scripts/check-pinned-inputs.sh"

echo "[ci/docs] validator image build"
VALIDATOR_IMAGE="$("${ROOT_DIR}/scripts/ci/build-validator-image.sh")"
export WORKCELL_VALIDATOR_IMAGE="${VALIDATOR_IMAGE}"

echo "[ci/docs] spelling and manpage"
"${ROOT_DIR}/scripts/ci/run-docs-in-validator.sh"

echo "Workcell shared docs job passed."
