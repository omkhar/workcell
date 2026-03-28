#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  echo "verify-scenario-coverage-entrypoint-ok"
  exit 0
fi

MANIFEST="${ROOT_DIR}/tests/scenarios/manifest.json"

missing=0

while IFS= read -r test_file; do
  full_path="${ROOT_DIR}/tests/scenarios/${test_file}"
  if [[ ! -f "${full_path}" ]]; then
    echo "Missing test file: tests/scenarios/${test_file}" >&2
    missing=$((missing + 1))
  fi
done < <(
  python3 - "${MANIFEST}" <<'PY'
import json, pathlib, sys
m = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
for s in m["scenarios"]:
    tf = s.get("test_file", "")
    if tf:
        print(tf)
PY
)

if [[ "${missing}" -gt 0 ]]; then
  echo "${missing} test file(s) missing." >&2
  exit 1
fi

echo "Scenario coverage verification passed"
