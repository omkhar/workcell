#!/usr/bin/env -S BASH_ENV= ENV= bash
# Merge-time scope check for upstream-refresh provider bump PRs.
#
# Usage: upstream-refresh-merge-time-scope.sh PR_CHECKOUT
#
# Run from a checkout of the PR base, so the guard code is trusted.
# PR_CHECKOUT is a full-history checkout of the PR head. The environment
# holds PR_HEAD_REF, PR_AUTHOR_TYPE, PR_HEAD_REPO, PR_BASE_SHA, PR_HEAD_SHA,
# and GITHUB_REPOSITORY. The check applies only to a PR that the upstream-refresh
# App made: a Bot author, a same-repository branch, and the provider branch
# prefix that upstream-refresh-publish.sh uses. Any other PR passes as not
# applicable. An applicable PR passes only when the diff that merges stays
# inside the bump surface of upstream-refresh-scope-guard.sh.
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "Usage: $0 PR_CHECKOUT" >&2
  exit 2
fi
: "${PR_HEAD_REF:?}" "${PR_AUTHOR_TYPE:?}" "${PR_HEAD_REPO:?}" "${PR_BASE_SHA:?}" "${PR_HEAD_SHA:?}" "${GITHUB_REPOSITORY:?}"

# This prefix must match the provider branch in upstream-refresh-publish.sh.
if [[ "${PR_AUTHOR_TYPE}" != Bot || "${PR_HEAD_REPO}" != "${GITHUB_REPOSITORY}" || "${PR_HEAD_REF}" != codex/upstream-refresh-provider-* ]]; then
  echo "Not applicable: this is not an upstream-refresh provider PR."
  exit 0
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
patch="$(mktemp)"
trap 'rm -f "${patch}"' EXIT
# Three dots give the diff from the merge base, the same diff the PR shows.
git -C "$1" diff --binary --full-index --patch --no-ext-diff --no-color "${PR_BASE_SHA}...${PR_HEAD_SHA}" >"${patch}"
exec "${ROOT_DIR}/scripts/ci/upstream-refresh-scope-guard.sh" "${patch}"
