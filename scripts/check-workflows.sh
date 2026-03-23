#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

require_tool actionlint
require_tool zizmor

(
  cd "${ROOT_DIR}"
  actionlint
)
zizmor "${ROOT_DIR}/.github/workflows/"*.yml

echo "Workcell workflow checks passed."
