#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

require_tool shellcheck
require_tool shfmt
require_tool python3
require_tool cargo
require_tool rustfmt

shell_files=(
  "${ROOT_DIR}/scripts/dev-quick-check.sh"
  "${ROOT_DIR}/scripts/workcell"
  "${ROOT_DIR}/scripts/dev-remote-validate.sh"
  "${ROOT_DIR}/runtime/container/bin/apt-helper.sh"
  "${ROOT_DIR}/runtime/container/bin/apt-wrapper.sh"
  "${ROOT_DIR}/runtime/container/home-control-plane.sh"
  "${ROOT_DIR}/runtime/container/provider-wrapper.sh"
  "${ROOT_DIR}/runtime/container/runtime-user.sh"
)

mapfile -t python_files < <(
  find "${ROOT_DIR}/scripts/lib" "${ROOT_DIR}/tests/python" \
    -type f -name '*.py' -print | sort
)

shellcheck -x "${shell_files[@]}"
shfmt -ln=bash -i 2 -ci -d "${shell_files[@]}"
python3 -m py_compile "${python_files[@]}"
python3 -m unittest discover -s "${ROOT_DIR}/tests/python" -p 'test_*.py'

(
  cd "${ROOT_DIR}/runtime/container/rust"
  cargo fmt --all --check
  cargo test --locked --offline
)

echo "Workcell quick validation passed."
