#!/usr/bin/env -S BASH_ENV= ENV= bash
# generated-artifact: internal/providerpolicy/adapters_gen.go
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT_PATH="${1:-${ROOT_DIR}/internal/providerpolicy/adapters_gen.go}"

cd "${ROOT_DIR}"
go run ./cmd/workcell-hostutil adapters gen provider-policy "${ROOT_DIR}" "${OUTPUT_PATH}"
