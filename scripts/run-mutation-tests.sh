#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

python3 "${ROOT_DIR}/tests/mutation/mutate_python_helpers.py"
python3 "${ROOT_DIR}/tests/mutation/mutate_rust_guard.py"
