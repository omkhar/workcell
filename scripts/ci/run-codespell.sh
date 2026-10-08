#!/usr/bin/env -S BASH_ENV= ENV= bash
# Runs codespell over the tracked-style doc files under ROOT. codespell prints
# each hit as file:line: word ==> fix; that output must reach the CI log.
# Paths after ROOT replace the tree walk, so the pre-push hook scans only the
# changed files through the same doc filter.
set -euo pipefail

ROOT="${1:-/workspace}"
[[ $# -eq 0 ]] || shift

list_candidates() {
  if [[ $# -gt 0 ]]; then
    printf '%s\0' "$@"
    return
  fi
  find "${ROOT}" \
    -path "${ROOT}/.git" -prune -o \
    -path "${ROOT}/dist" -prune -o \
    -path "${ROOT}/tmp" -prune -o \
    -path "${ROOT}/runtime/container/providers/node_modules" -prune -o \
    -path "${ROOT}/tools/markdownlint/node_modules" -prune -o \
    -path "${ROOT}/runtime/container/rust/vendor" -prune -o \
    -path "${ROOT}/runtime/container/rust/target" -prune -o \
    -type f -print0
}

if ! list_candidates "$@" |
  { grep -zE '\.(md|txt|1)$' || [[ $? -eq 1 ]]; } |
  sort -z |
  xargs -0 -r codespell --config "${ROOT}/.codespellrc"; then
  echo "codespell failed; the offending lines are listed above as file:line: word ==> fix" >&2
  exit 1
fi
