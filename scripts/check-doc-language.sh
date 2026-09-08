#!/bin/bash -p
# Enforces the countable ASD-STE100 rules that AGENTS.md and
# docs/documentation-language.md declare mandatory for public documents:
# six sentences per paragraph, twenty words per instruction, twenty-five words
# per descriptive sentence, no -ing verb form, and three words per multi-word
# noun. policy/doc-language-baseline.tsv ratchets the counts the documents
# carry today, so the rules cannot spread.
# shellcheck source=scripts/lib/trusted-entrypoint.sh
# shellcheck disable=SC2312 # repo-wide bootstrap; the path is the running script's own directory
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/trusted-entrypoint.sh"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "check-doc-language-entrypoint-ok"
  exit 0
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

GO_BIN="${WORKCELL_GO_BIN:-}"

resolve_go_bin() {
  if [[ -n "${GO_BIN}" && -x "${GO_BIN}" ]]; then
    return 0
  fi
  if GO_BIN="$(command -v go 2>/dev/null)"; then
    return 0
  fi
  for candidate in \
    /opt/homebrew/bin/go \
    /usr/local/go/bin/go \
    /usr/local/bin/go \
    /usr/bin/go; do
    if [[ -x "${candidate}" ]]; then
      GO_BIN="${candidate}"
      return 0
    fi
  done
  echo "Missing required tool: go" >&2
  exit 1
}

resolve_go_bin

(cd "${ROOT_DIR}" && "${GO_BIN}" run ./cmd/workcell-citools check-doc-language "${ROOT_DIR}")
