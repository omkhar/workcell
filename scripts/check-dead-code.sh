#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

readonly DEADCODE_VERSION="v0.38.0"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/go-run-env.sh"

run_deadcode() {
  GOTOOLCHAIN=local run_go_in_repo "${ROOT_DIR}" run "golang.org/x/tools/cmd/deadcode@${DEADCODE_VERSION}" -test ./cmd/... ./internal/... ./tests/...
}

run_deadcode
echo "Go dead code check passed."
