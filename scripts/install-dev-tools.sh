#!/usr/bin/env -S BASH_ENV= ENV= bash
# Install development tools needed by validate-repo.sh and dev-quick-check.sh.
# Detects macOS (brew) vs Linux (apt) and installs missing packages.
set -euo pipefail

append_unique_brew() {
  local candidate=""
  local existing=""

  for candidate in "$@"; do
    for existing in "${brew_missing[@]:-}"; do
      if [[ "${existing}" == "${candidate}" ]]; then
        candidate=""
        break
      fi
    done
    [[ -n "${candidate}" ]] || continue
    brew_missing+=("${candidate}")
  done
}

append_unique_apt() {
  local candidate=""
  local existing=""

  for candidate in "$@"; do
    for existing in "${apt_missing[@]:-}"; do
      if [[ "${existing}" == "${candidate}" ]]; then
        candidate=""
        break
      fi
    done
    [[ -n "${candidate}" ]] || continue
    apt_missing+=("${candidate}")
  done
}

python_venv_supported() {
  command -v python3 &>/dev/null || return 1
  python3 -m venv --help >/dev/null 2>&1
}

echo "Checking host tools..."
missing=()
brew_missing=()
apt_missing=()

if ! command -v shellcheck &>/dev/null; then
  missing+=(shellcheck)
  append_unique_brew shellcheck
  append_unique_apt shellcheck
fi
if ! command -v shfmt &>/dev/null; then
  missing+=(shfmt)
  append_unique_brew shfmt
  append_unique_apt shfmt
fi
if ! command -v yamllint &>/dev/null; then
  missing+=(yamllint)
  append_unique_brew yamllint
  append_unique_apt yamllint
fi
if ! command -v codespell &>/dev/null; then
  missing+=(codespell)
  append_unique_brew codespell
  append_unique_apt codespell
fi
if ! command -v jq &>/dev/null; then
  missing+=(jq)
  append_unique_brew jq
  append_unique_apt jq
fi
if ! command -v npm &>/dev/null; then
  missing+=(npm)
  append_unique_brew node
  append_unique_apt nodejs npm
fi
if ! python_venv_supported; then
  missing+=(python3-venv)
  append_unique_brew python
  append_unique_apt python3 python3-venv python3-pip
fi

if [[ ${#missing[@]} -gt 0 ]]; then
  case "$(uname -s)" in
    Darwin)
      echo "  brew install ${brew_missing[*]}"
      brew install "${brew_missing[@]}"
      ;;
    Linux)
      echo "  sudo apt-get install ${apt_missing[*]}"
      sudo apt-get update -qq && sudo apt-get install -y "${apt_missing[@]}"
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
if ! python_venv_supported; then
  echo "Missing required Python venv support after install." >&2
  exit 1
fi
if [[ ! -d "${VENV_DIR}" ]] || ! "${VENV_DIR}/bin/python3" -c "import pytest" &>/dev/null; then
  echo "  Setting up Python venv with pytest..."
  python3 -m venv "${VENV_DIR}"
  "${VENV_DIR}/bin/pip" install --quiet pytest
fi

echo "Done."
