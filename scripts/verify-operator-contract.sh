#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  echo "verify-operator-contract-entrypoint-ok"
  exit 0
fi

(
  cd "${ROOT_DIR}"
  env -u WORKCELL_HELP_BIN \
    go run ./cmd/workcell-metadatautil validate-operator-contract \
    "${ROOT_DIR}" \
    "${ROOT_DIR}/policy/operator-contract.toml" \
    "${ROOT_DIR}/policy/requirements.toml"
)

echo "Operator contract verification passed"
