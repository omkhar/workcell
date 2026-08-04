#!/bin/bash -p
# shellcheck source=scripts/lib/trusted-entrypoint.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/trusted-entrypoint.sh"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "check-public-contract-entrypoint-ok"
  exit 0
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/canonical-build-env.sh
source "${ROOT_DIR}/scripts/lib/canonical-build-env.sh"
workcell_require_modern_privileged_bash "$@"
workcell_require_canonical_build_environment
CONTRACT_PATH="${ROOT_DIR}/policy/public-contract.toml"
FREEZE_PATH="${ROOT_DIR}/policy/v1-contract-freeze.toml"

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

(
  cd "${ROOT_DIR}" || exit 1
  "${GO_BIN}" run ./cmd/workcell-citools validate-public-contract "${ROOT_DIR}" "${CONTRACT_PATH}"
  "${GO_BIN}" run ./cmd/workcell-citools validate-v1-contract-freeze-git-history \
    "${ROOT_DIR}" \
    "${FREEZE_PATH}"
)
