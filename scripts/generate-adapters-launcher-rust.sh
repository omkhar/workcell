#!/usr/bin/env -S BASH_ENV= ENV= bash
# generated-artifact: runtime/container/rust/src/generated_targets.rs
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT_PATH="${1:-${ROOT_DIR}/runtime/container/rust/src/generated_targets.rs}"

cd "${ROOT_DIR}"
go run ./cmd/workcell-hostutil adapters gen launcher-rust "${ROOT_DIR}" "${OUTPUT_PATH}"
