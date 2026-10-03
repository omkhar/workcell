#!/usr/bin/env -S BASH_ENV= ENV= bash
# Runs codespell over the tracked-style doc files under ROOT. codespell prints
# each hit as file:line: word ==> fix; that output must reach the CI log.
set -euo pipefail

ROOT="${1:-/workspace}"

if ! find "${ROOT}" \
  -path "${ROOT}/.git" -prune -o \
  -path "${ROOT}/dist" -prune -o \
  -path "${ROOT}/tmp" -prune -o \
  -path "${ROOT}/runtime/container/providers/node_modules" -prune -o \
  -path "${ROOT}/tools/markdownlint/node_modules" -prune -o \
  -path "${ROOT}/runtime/container/rust/vendor" -prune -o \
  -path "${ROOT}/runtime/container/rust/target" -prune -o \
  -type f \( -name "*.md" -o -name "*.txt" -o -name "*.1" \) -print0 |
  sort -z |
  xargs -0 -r codespell --config "${ROOT}/.codespellrc"; then
  echo "codespell failed; the offending lines are listed above as file:line: word ==> fix" >&2
  exit 1
fi
