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

while IFS= read -r artifact_name; do
  if ! grep -qF "${artifact_name}" "${CONTROL_PLANE}"; then
    echo "runtime_artifact not referenced in home-control-plane.sh: ${artifact_name}" >&2
    missing=$((missing + 1))
  fi
done < <(python3 - "${MANIFEST}" <<'PY'
import json, os, pathlib, sys
m = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
for artifact in m.get("runtime_artifacts", []):
    rp = artifact.get("runtime_path", "")
    if rp:
        print(os.path.basename(rp))
PY
)

if [[ "${missing}" -gt 0 ]]; then
  echo "${missing} runtime artifact(s) not referenced in home-control-plane.sh." >&2
  exit 1
fi

echo "Control plane parity verification passed"
