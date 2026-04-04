#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  echo "verify-control-plane-parity-entrypoint-ok"
  exit 0
fi

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

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

GO_BIN="$(resolve_go_bin)"

MANIFEST="${ROOT_DIR}/runtime/container/control-plane-manifest.json"
CONTROL_PLANE="${ROOT_DIR}/runtime/container/home-control-plane.sh"

missing=0

while IFS=$'\t' read -r requirement_type label value; do
  [[ -n "${requirement_type}" ]] || continue

  case "${requirement_type}" in
    prefix)
      expected_call="workcell_verify_control_plane_prefix \"\${ADAPTER_ROOT}/${label}/\""
      if ! grep -Fq "${expected_call}" "${CONTROL_PLANE}"; then
        echo "missing control-plane verification prefix for ${label}: ${expected_call}" >&2
        missing=$((missing + 1))
      fi
      ;;
    path)
      expected_call="workcell_verify_control_plane_path \"${value}\""
      if ! grep -Fq "${expected_call}" "${CONTROL_PLANE}"; then
        echo "missing control-plane verification path for ${label}: ${expected_call}" >&2
        missing=$((missing + 1))
      fi
      ;;
    *)
      echo "unsupported parity requirement type: ${requirement_type}" >&2
      missing=$((missing + 1))
      ;;
  esac
done < <(
  cd "${ROOT_DIR}" && "${GO_BIN}" run ./cmd/workcell-metadatautil verify-control-plane-parity "${MANIFEST}"
)

if [[ "${missing}" -gt 0 ]]; then
  echo "${missing} control-plane verification gap(s) found in home-control-plane.sh." >&2
  exit 1
fi

echo "Control plane parity verification passed"
