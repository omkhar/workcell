#!/usr/bin/env -S BASH_ENV= ENV= bash
# Scope guards for the upstream-refresh candidates.
#
# Usage: upstream-refresh-scope-guard.sh PATCH_FILE
#        upstream-refresh-scope-guard.sh --toolchain PATCH_FILE OUTPUT_FILE
#
# Thin dispatch. The policies live in metadatautil.
#
# With PATCH_FILE alone, the provider guard (CheckUpstreamRefreshScope) exits 0
# only when every change in the git patch is inside the agent-bump surface. A
# patch that is out of scope is still a valid PR, but a human must review it.
#
# With --toolchain, the toolchain guard (CheckUpstreamRefreshToolchainScope)
# applies the patch to HEAD of this checkout in a private index. It exits 0
# only when the tree changes nothing but toolchain pin lines, and then appends
# verdict=passed:<base commit>:<tree> to OUTPUT_FILE, the step output file. The
# publish script publishes that tree on that base and no other.
set -euo pipefail

usage() {
  echo "Usage: $0 PATCH_FILE" >&2
  echo "       $0 --toolchain PATCH_FILE OUTPUT_FILE" >&2
  exit 2
}

toolchain=0
if [[ "${1:-}" == --toolchain ]]; then
  toolchain=1
  shift
  [[ $# -eq 2 ]] || usage
  # The output file is resolved like the patch.
  [[ $2 == /* ]] || set -- "$1" "${PWD}/$2"
else
  [[ $# -eq 1 ]] || usage
fi

# The Go command runs from the repo root, so resolve the patch path first.
[[ $1 == /* ]] || set -- "${PWD}/$1" "${@:2}"

# Hygiene, not a boundary: children do not inherit the runner command files,
# but one could still find them through /proc. The verdict holds because the
# job reads it only on a step that succeeded, and the publisher checks its base
# and tree against the candidate.
unset GITHUB_OUTPUT GITHUB_ENV GITHUB_PATH GITHUB_STATE GITHUB_STEP_SUMMARY

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/go-run-env.sh"

if [[ "${toolchain}" == 0 ]]; then
  exec_go_run_in_repo "${ROOT_DIR}" ./cmd/workcell-citools upstream-refresh-scope-guard "$1"
fi

# Only a well-formed verdict reaches the step output, and only after the
# guard passed, so a failed guard leaves no verdict for the publish job.
verdict="$(run_go_in_repo "${ROOT_DIR}" run ./cmd/workcell-citools upstream-refresh-toolchain-scope-guard "$1")" || exit 1
if [[ ! "${verdict}" =~ ^passed:[0-9a-f]{40}:[0-9a-f]{40}$ ]]; then
  echo "upstream-refresh-scope-guard: the toolchain guard printed no verdict" >&2
  exit 1
fi
printf 'verdict=%s\n' "${verdict}" >>"$2"
