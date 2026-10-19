#!/bin/bash -p
# shellcheck source=scripts/lib/trusted-entrypoint.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/trusted-entrypoint.sh"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "verify-upstream-goose-release-entrypoint-ok"
  exit 0
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

require_tool go

# One GitHub API request reads the release of the Dockerfile pin. The request
# uses WORKCELL_GITHUB_API_TOKEN or WORKCELL_GITHUB_API_TOKEN_FILE when one is
# set.
(cd "${ROOT_DIR}" && go run ./cmd/workcell-citools verify-goose-release runtime/container/Dockerfile)

echo "Workcell upstream goose release verification passed."
