#!/usr/bin/env -S BASH_ENV= ENV= bash
# Install development tools needed by validate-repo.sh and dev-quick-check.sh.
# Detects macOS (brew) vs Linux (apt) and installs missing packages.
set -euo pipefail

echo "Checking host tools..."
missing=()

command -v shellcheck &>/dev/null || missing+=(shellcheck)
command -v shfmt &>/dev/null || missing+=(shfmt)
command -v yamllint &>/dev/null || missing+=(yamllint)
command -v codespell &>/dev/null || missing+=(codespell)
command -v jq &>/dev/null || missing+=(jq)

if [[ ${#missing[@]} -gt 0 ]]; then
  case "$(uname -s)" in
    Darwin)
      echo "  brew install ${missing[*]}"
      brew install "${missing[@]}"
      ;;
    Linux)
      echo "  sudo apt-get install ${missing[*]}"
      sudo apt-get update -qq && sudo apt-get install -y "${missing[@]}"
      ;;
    *)
      echo "Unsupported OS. Install manually: ${missing[*]}" >&2
      exit 1
      ;;
  esac
fi

if ! command -v markdownlint &>/dev/null; then
  echo "  npm install -g markdownlint-cli"
  npm install -g markdownlint-cli
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VENV_DIR="${ROOT_DIR}/.venv"
if [[ ! -d "${VENV_DIR}" ]] || ! "${VENV_DIR}/bin/python3" -c "import pytest" &>/dev/null; then
  echo "  Setting up Python venv with pytest..."
  python3 -m venv "${VENV_DIR}"
  "${VENV_DIR}/bin/pip" install --quiet pytest
fi

echo "Done."
