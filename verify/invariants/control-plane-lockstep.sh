#!/usr/bin/env -S BASH_ENV= ENV= bash
# verify/invariants/control-plane-lockstep.sh
#
# Concrete lockstep invariant for AGENTS.md L157 ("runtime/, policy/,
# adapters/, verify/, workflows/ should evolve in lockstep").  Asserts that
# every policy/*.{toml,tsv,json} file is mentioned by name in at least one
# user-facing markdown surface, so a new policy file cannot land without an
# operator-visible doc pointer.
#
# This is the first concrete check living under verify/invariants/; the
# directory previously held only narrative markdown.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SEARCH_ROOTS=(
  "${ROOT_DIR}/docs"
  "${ROOT_DIR}/verify"
  "${ROOT_DIR}/README.md"
  "${ROOT_DIR}/SECURITY.md"
  "${ROOT_DIR}/AGENTS.md"
  "${ROOT_DIR}/CONTRIBUTING.md"
)

missing=()
for path in "${ROOT_DIR}"/policy/*; do
  base="$(basename "${path}")"
  case "${base}" in
    README.md) continue ;;
  esac
  hit=0
  for root in "${SEARCH_ROOTS[@]}"; do
    if [[ -e "${root}" ]] && grep -rlqF "${base}" "${root}" 2>/dev/null; then
      hit=1
      break
    fi
  done
  if [[ "${hit}" -eq 0 ]]; then
    missing+=("${base}")
  fi
done

if [[ "${#missing[@]}" -gt 0 ]]; then
  echo "Control-plane lockstep invariant failed: policy/ files missing from user-facing docs:" >&2
  for name in "${missing[@]}"; do
    echo "  - policy/${name}" >&2
  done
  exit 1
fi

echo "Control-plane lockstep invariant passed: every policy/ file is documented."
