#!/usr/bin/env -S BASH_ENV= ENV= bash
# Build the validator image and run the Go fuzz targets inside it, so the
# scheduled fuzz lane uses the reviewed pinned Go toolchain. Mirrors the
# build+run portion of job-mutation.sh.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

echo "[ci/fuzz] validator image build"
VALIDATOR_IMAGE="$("${ROOT_DIR}/scripts/ci/build-validator-image.sh")"
export WORKCELL_VALIDATOR_IMAGE="${VALIDATOR_IMAGE}"

echo "[ci/fuzz] go fuzz targets in validator"
exec "${ROOT_DIR}/scripts/ci/run-fuzz-in-validator.sh"
