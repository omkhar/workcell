#!/usr/bin/env -S BASH_ENV= ENV= bash
# Install development tools needed by validate-repo.sh and dev-quick-check.sh.
# Detects macOS (brew) vs Linux (apt) and installs missing packages.
set -euo pipefail

readonly MARKDOWNLINT_VERSION="0.49.0"
readonly MARKDOWNLINT_NODE_VERSION_MINIMUM="22.12.0"

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

node_version() {
  local node_bin=""
  local version=""

  if command -v node &>/dev/null; then
    node_bin="node"
  elif command -v nodejs &>/dev/null; then
    node_bin="nodejs"
  else
    return 1
  fi

  version="$("${node_bin}" --version 2>/dev/null || true)"
  version="${version#v}"
  printf '%s\n' "${version%%-*}"
}

version_at_least() {
  local actual="$1"
  local minimum="$2"
  local actual_major actual_minor actual_patch
  local minimum_major minimum_minor minimum_patch

  IFS=. read -r actual_major actual_minor actual_patch <<<"${actual}"
  IFS=. read -r minimum_major minimum_minor minimum_patch <<<"${minimum}"
  for part in actual_major actual_minor actual_patch minimum_major minimum_minor minimum_patch; do
    if [[ -z "${!part}" || ! "${!part}" =~ ^[0-9]+$ ]]; then
      return 1
    fi
  done

  if ((actual_major != minimum_major)); then
    ((actual_major > minimum_major))
    return
  fi
  if ((actual_minor != minimum_minor)); then
    ((actual_minor > minimum_minor))
    return
  fi
  ((actual_patch >= minimum_patch))
}

markdownlint_node_install_hint() {
  cat >&2 <<EOF
Install Node.js ${MARKDOWNLINT_NODE_VERSION_MINIMUM} or newer before installing markdownlint-cli@${MARKDOWNLINT_VERSION}.
On macOS, Homebrew's node package satisfies this requirement.
On Linux, use a current Node.js LTS package source such as your distro's supported Node.js channel, NodeSource, nvm, or asdf; Ubuntu 24.04's nodejs/npm apt packages are too old for this markdownlint release.
Then rerun scripts/install-dev-tools.sh.
EOF
}

require_markdownlint_node() {
  local version=""

  if ! version="$(node_version)" || [[ -z "${version}" ]]; then
    echo "markdownlint-cli@${MARKDOWNLINT_VERSION} requires Node.js ${MARKDOWNLINT_NODE_VERSION_MINIMUM} or newer; no usable node binary was found." >&2
    markdownlint_node_install_hint
    exit 1
  fi
  if ! version_at_least "${version}" "${MARKDOWNLINT_NODE_VERSION_MINIMUM}"; then
    echo "markdownlint-cli@${MARKDOWNLINT_VERSION} requires Node.js ${MARKDOWNLINT_NODE_VERSION_MINIMUM} or newer; found ${version}." >&2
    markdownlint_node_install_hint
    exit 1
  fi
}

require_markdownlint_npm() {
  if command -v npm &>/dev/null; then
    return 0
  fi
  echo "markdownlint-cli@${MARKDOWNLINT_VERSION} requires npm from a Node.js ${MARKDOWNLINT_NODE_VERSION_MINIMUM} or newer installation." >&2
  markdownlint_node_install_hint
  exit 1
}

markdownlint_needs_install() {
  local current_version=""

  if ! command -v markdownlint &>/dev/null; then
    return 0
  fi
  current_version="$(markdownlint --version 2>/dev/null || true)"
  [[ "${current_version}" != *"${MARKDOWNLINT_VERSION}"* ]]
}

echo "Checking host tools..."
missing=()
brew_missing=()
apt_missing=()
host_os="$(uname -s)"

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
  case "${host_os}" in
    Darwin)
      missing+=(npm)
      append_unique_brew node
      ;;
    Linux) ;;
    *)
      missing+=(npm)
      ;;
  esac
fi
if ! command -v actionlint &>/dev/null; then
  missing+=(actionlint)
  append_unique_brew actionlint
  append_unique_apt actionlint
fi
if ! command -v zizmor &>/dev/null; then
  missing+=(zizmor)
  append_unique_brew zizmor
  # Linux distros do not ship zizmor in apt yet; surface as manual install.
  append_unique_apt zizmor
fi
if ! command -v hadolint &>/dev/null; then
  missing+=(hadolint)
  append_unique_brew hadolint
  append_unique_apt hadolint
fi
if ! command -v cosign &>/dev/null; then
  missing+=(cosign)
  append_unique_brew cosign
  append_unique_apt cosign
fi
if ! command -v syft &>/dev/null; then
  missing+=(syft)
  append_unique_brew syft
  # syft isn't packaged in apt repos by default; manual fallback.
  append_unique_apt syft
fi

if [[ "${host_os}" == "Linux" ]] && markdownlint_needs_install; then
  require_markdownlint_node
  require_markdownlint_npm
fi

if [[ ${#missing[@]} -gt 0 ]]; then
  case "${host_os}" in
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

if markdownlint_needs_install; then
  require_markdownlint_node
  require_markdownlint_npm
  echo "  npm install -g markdownlint-cli@${MARKDOWNLINT_VERSION}"
  npm install -g "markdownlint-cli@${MARKDOWNLINT_VERSION}"
fi

echo "Done."
