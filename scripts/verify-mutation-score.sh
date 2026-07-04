#!/usr/bin/env -S BASH_ENV= ENV= bash
# Run the mutation harness and fail when the mutation score drops below the
# reviewed baseline in policy/mutation-score-policy.json. Used by the
# release-preflight validation profile so a release cannot regress the mutation
# safety net.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

(
  cd "${ROOT_DIR}"
  go run ./cmd/workcell-citools mutation-score policy/mutation-score-policy.json
)
