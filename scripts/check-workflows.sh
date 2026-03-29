#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
POLICY_PATH="${ROOT_DIR}/policy/github-hosted-controls.toml"

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

require_tool actionlint
require_tool zizmor
require_tool python3

(
  cd "${ROOT_DIR}"
  actionlint
)
zizmor --persona auditor --config "${ROOT_DIR}/.github/zizmor.yml" "${ROOT_DIR}/.github/workflows/"*.yml

python3 - "${ROOT_DIR}" "${POLICY_PATH}" <<'PY'
import pathlib
import re
import sys
import tomllib

root = pathlib.Path(sys.argv[1])
policy_path = pathlib.Path(sys.argv[2])
policy = tomllib.loads(policy_path.read_text(encoding="utf-8"))
expected = policy.get("required_status_checks", {}).get("contexts")
if not isinstance(expected, list) or not expected:
    raise SystemExit(
        f"{policy_path} must define required_status_checks.contexts as a non-empty array"
    )
if not all(isinstance(context, str) and context for context in expected):
    raise SystemExit(
        f"{policy_path} must define required_status_checks.contexts as non-empty strings"
    )

job_name_pattern = re.compile(r"^ {4}name:\s*(.+?)\s*$", re.MULTILINE)
job_names: set[str] = set()
for path in sorted((root / ".github" / "workflows").glob("*.yml")):
    content = path.read_text(encoding="utf-8")
    for match in job_name_pattern.finditer(content):
        name = match.group(1).strip()
        if (
            (name.startswith('"') and name.endswith('"'))
            or (name.startswith("'") and name.endswith("'"))
        ):
            name = name[1:-1]
        job_names.add(name)

missing = sorted(set(expected) - job_names)
if missing:
    raise SystemExit(
        "Workflow jobs are missing required status-check names from "
        f"{policy_path}: {', '.join(missing)}"
    )
PY

echo "Workcell workflow checks passed."
