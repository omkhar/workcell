#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALL_TOOLS=1
CONFIGURE_HOOKS=1
RUN_QUICK_CHECK=0

usage() {
  cat <<'EOF'
Usage: ./scripts/bootstrap-dev.sh [options]

Bootstrap a local contributor environment for Workcell.

Options:
  --skip-tools        Do not install or validate the common local toolchain
  --skip-hooks        Do not configure .githooks as core.hooksPath
  --quick-check       Run ./scripts/dev-quick-check.sh after setup
  -h, --help          Show this help text
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-tools)
      INSTALL_TOOLS=0
      shift
      ;;
    --skip-hooks)
      CONFIGURE_HOOKS=0
      shift
      ;;
    --quick-check)
      RUN_QUICK_CHECK=1
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "Unsupported bootstrap option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ "${INSTALL_TOOLS}" -eq 1 ]]; then
  "${ROOT_DIR}/scripts/install-dev-tools.sh"
fi

if [[ "${CONFIGURE_HOOKS}" -eq 1 ]]; then
  git -C "${ROOT_DIR}" config core.hooksPath .githooks
  echo "Configured repo hooks: .githooks"
fi

if [[ "${RUN_QUICK_CHECK}" -eq 1 ]]; then
  "${ROOT_DIR}/scripts/dev-quick-check.sh"
fi

cat <<'EOF'
Bootstrap complete.

Suggested next steps:
  ./scripts/dev-quick-check.sh
  ./scripts/pre-merge.sh
EOF
