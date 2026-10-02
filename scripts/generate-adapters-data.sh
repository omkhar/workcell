#!/usr/bin/env -S BASH_ENV= ENV= bash
# generated-artifact: internal/adapters/data_gen.go
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT_PATH="${1:-${ROOT_DIR}/internal/adapters/data_gen.go}"

cd "${ROOT_DIR}"
go run ./cmd/workcell-hostutil adapters gen data "${ROOT_DIR}" "${OUTPUT_PATH}"
