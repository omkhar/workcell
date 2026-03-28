#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  echo "verify-control-plane-parity-entrypoint-ok"
  exit 0
fi

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
  python3 - "${MANIFEST}" <<'PY'
import json
import pathlib
import sys

m = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
runtime_paths = {
    artifact.get("runtime_path", "")
    for artifact in m.get("runtime_artifacts", [])
    if artifact.get("runtime_path")
}

for provider in ("codex", "claude", "gemini"):
    prefix = f"/opt/workcell/adapters/{provider}/"
    if any(path.startswith(prefix) for path in runtime_paths):
        print(f"prefix\t{provider}\t{prefix}")

managed_settings_path = "/etc/claude-code/managed-settings.json"
if managed_settings_path in runtime_paths:
    print(f"path\tclaude-managed-settings\t{managed_settings_path}")
PY
)

if [[ "${missing}" -gt 0 ]]; then
  echo "${missing} control-plane verification gap(s) found in home-control-plane.sh." >&2
  exit 1
fi

echo "Control plane parity verification passed"
