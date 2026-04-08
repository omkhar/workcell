#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/go-run-env.sh"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  echo "verify-requirements-coverage-entrypoint-ok"
  exit 0
fi

run_go_in_repo "${ROOT_DIR}" run ./cmd/workcell-metadatautil validate-requirements "${ROOT_DIR}" "${ROOT_DIR}/policy/requirements.toml"
