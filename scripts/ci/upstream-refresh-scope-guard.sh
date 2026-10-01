#!/usr/bin/env -S BASH_ENV= ENV= bash
# Scope guard for automatic upstream-refresh merges.
#
# Usage: upstream-refresh-scope-guard.sh PATCH_FILE
#
# Thin dispatch. The policy lives in metadatautil.CheckUpstreamRefreshScope.
# Exit 0 only when every change in the git patch is inside the agent-bump
# surface. A patch that is out of scope is still a valid PR, but a human must
# review it.
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "Usage: $0 PATCH_FILE" >&2
  exit 2
fi

# The Go command runs from the repo root, so resolve the patch path first.
[[ $1 == /* ]] || set -- "${PWD}/$1"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/go-run-env.sh"

exec_go_run_in_repo "${ROOT_DIR}" ./cmd/workcell-citools upstream-refresh-scope-guard "$1"
