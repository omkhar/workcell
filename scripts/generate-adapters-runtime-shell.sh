#!/usr/bin/env -S BASH_ENV= ENV= bash
# generated-artifact: runtime/container/generated-adapters.sh
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT_PATH="${1:-${ROOT_DIR}/runtime/container/generated-adapters.sh}"

cd "${ROOT_DIR}"
go run ./cmd/workcell-hostutil adapters gen runtime-shell "${ROOT_DIR}" "${OUTPUT_PATH}"
