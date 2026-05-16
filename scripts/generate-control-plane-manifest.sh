#!/bin/bash -p
# shellcheck source=scripts/lib/trusted-entrypoint.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/trusted-entrypoint.sh"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "generate-control-plane-manifest-entrypoint-ok"
  exit 0
fi

OUTPUT_PATH="${1:-}"

resolve_go_bin() {
  local candidate

  if candidate="$(command -v go 2>/dev/null)"; then
    printf '%s\n' "${candidate}"
    return 0
  fi

  for candidate in \
    /opt/homebrew/bin/go \
    /usr/local/go/bin/go \
    /usr/local/bin/go \
    /usr/bin/go; do
    if [[ -x "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done

  echo "Missing required tool: go" >&2
  exit 1
}

resolve_root_path() {
  local candidate="$1"
  (
    cd "${candidate}"
    pwd -P
  )
}

resolve_output_path() {
  local candidate="$1"

  case "${candidate}" in
    /*) printf '%s\n' "${candidate}" ;;
    ./*) printf '%s/%s\n' "$(pwd -P)" "${candidate#./}" ;;
    *) printf '%s/%s\n' "$(pwd -P)" "${candidate}" ;;
  esac
}

[[ -n "${OUTPUT_PATH}" ]] || {
  echo "usage: $0 OUTPUT_PATH" >&2
  exit 64
}

ROOT_DIR="$(resolve_root_path "${WORKCELL_CONTROL_PLANE_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)}")"
GO_BIN="$(resolve_go_bin)"
OUTPUT_PATH="$(resolve_output_path "${OUTPUT_PATH}")"

(cd "${ROOT_DIR}" && "${GO_BIN}" run ./cmd/workcell-citools generate-control-plane-manifest "${ROOT_DIR}" "${OUTPUT_PATH}")
