#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

require_tool go
require_tool gofmt

"${ROOT_DIR}/scripts/verify-scenario-coverage.sh"
"${ROOT_DIR}/scripts/run-mutation-tests.sh"

(
  cd "${ROOT_DIR}"
  go_files=()
  while IFS= read -r -d '' item; do
    go_files+=("${item}")
  done < <(find "${ROOT_DIR}/cmd" "${ROOT_DIR}/internal" -type f -name '*.go' -print0 | sort -z)
  if [[ "${#go_files[@]}" -gt 0 ]]; then
    if gofmt -l "${go_files[@]}" | grep -q .; then
      echo "Go files are not formatted with gofmt." >&2
      exit 1
    fi
  fi
  go vet ./...
  go test ./...
)

echo "Go port validation baseline passed."
