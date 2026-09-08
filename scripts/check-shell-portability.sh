#!/bin/bash -p
# Rejects a GNU-only or Bash-4-only construct in a tracked shell script. The
# host baseline is macOS: /bin/bash 3.2 and a BSD userland, so mapfile,
# declare -A, sha256sum, stat -c and date -d fail there. A script that runs
# only inside the Linux image declares "# portability-exempt: linux-only
# (reason)" in its header, and a reviewed line carries the same tag at its end.
# shellcheck source=scripts/lib/trusted-entrypoint.sh
# shellcheck disable=SC2312 # repo-wide bootstrap; the path is the running script's own directory
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/trusted-entrypoint.sh"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "check-shell-portability-entrypoint-ok"
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

(cd "${ROOT_DIR}" && "${GO_BIN}" run ./cmd/workcell-citools check-shell-portability "${ROOT_DIR}")
