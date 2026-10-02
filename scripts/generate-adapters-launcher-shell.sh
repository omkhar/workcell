#!/usr/bin/env -S BASH_ENV= ENV= bash
# generated-artifact: scripts/lib/launcher/generated-adapters.sh
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT_PATH="${1:-${ROOT_DIR}/scripts/lib/launcher/generated-adapters.sh}"

cd "${ROOT_DIR}"
go run ./cmd/workcell-hostutil adapters gen launcher-shell "${ROOT_DIR}" "${OUTPUT_PATH}"
