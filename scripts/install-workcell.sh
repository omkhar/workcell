#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALL_DIR="${HOME}/.local/bin"
INSTALL_PATH="${INSTALL_DIR}/workcell"
MAN_DIR="${HOME}/.local/share/man/man1"
MAN_PATH="${MAN_DIR}/workcell.1"
DEBUG_INSTALL=0
DEBUG_DIR="${HOME}/.config/workcell/debug"
AUTO_INSTALL_DEPS=1
declare -a MISSING_HOST_TOOLS=()
declare -a MISSING_BREW_FORMULAS=()

require_supported_macos_host_arch() {
  local host_os=""
  local host_arch=""
  local arm64_capable=""

  host_os="$(uname -s 2>/dev/null || true)"
  [[ "${host_os}" == "Darwin" ]] || return 0

  arm64_capable="$(sysctl -in hw.optional.arm64 2>/dev/null || true)"
  if [[ "${arm64_capable}" == "1" ]]; then
    return 0
  fi

  host_arch="$(uname -m 2>/dev/null || true)"
  if [[ "${host_arch}" == "arm64" || "${host_arch}" == "aarch64" ]]; then
    return 0
  fi

  echo "Workcell supports Apple Silicon macOS hosts only. Intel macOS is not supported." >&2
  exit 1
}

usage() {
  cat <<'EOF'
Usage: ./scripts/install.sh [--debug] [--debug-dir PATH] [--no-install-deps]

Install Workcell into ~/.local/bin and the man page into ~/.local/share/man/man1.
On Apple Silicon macOS hosts, the installer also uses Homebrew to install only
the required host packages that are still missing: colima, docker, gh, git, go.

Options:
  --debug           Install a wrapper that auto-enables launcher debug logging
                    and launch-time rebuilds
  --debug-dir PATH  Override the host directory used by the debug wrapper
  --no-install-deps Do not install missing host packages; warn at the end
  -h, --help        Show this help text
EOF
}

resolve_output_dir() {
  local raw_path="$1"
  local absolute_path=""
  local parent_dir=""
  local leaf_name=""

  if [[ "${raw_path}" == /* ]]; then
    absolute_path="${raw_path}"
  else
    absolute_path="$(pwd -P)/${raw_path}"
  fi

  leaf_name="$(basename "${absolute_path}")"
  parent_dir="$(dirname "${absolute_path}")"
  mkdir -p "${parent_dir}"
  parent_dir="$(cd "${parent_dir}" && pwd -P)"
  printf '%s/%s\n' "${parent_dir}" "${leaf_name}"
}

running_on_supported_macos_host() {
  [[ "$(uname -s 2>/dev/null || true)" == "Darwin" ]]
}

append_unique_missing_host_tool() {
  local candidate="$1"
  local existing=""

  for existing in "${MISSING_HOST_TOOLS[@]:-}"; do
    if [[ "${existing}" == "${candidate}" ]]; then
      return 0
    fi
  done
  MISSING_HOST_TOOLS+=("${candidate}")
}

append_unique_missing_brew_formula() {
  local candidate="$1"
  local existing=""

  for existing in "${MISSING_BREW_FORMULAS[@]:-}"; do
    if [[ "${existing}" == "${candidate}" ]]; then
      return 0
    fi
  done
  MISSING_BREW_FORMULAS+=("${candidate}")
}

collect_missing_required_host_dependencies() {
  MISSING_HOST_TOOLS=()
  MISSING_BREW_FORMULAS=()

  if ! command -v colima >/dev/null 2>&1; then
    append_unique_missing_host_tool colima
    append_unique_missing_brew_formula colima
  fi
  if ! command -v docker >/dev/null 2>&1; then
    append_unique_missing_host_tool docker
    append_unique_missing_brew_formula docker
  fi
  if ! command -v gh >/dev/null 2>&1; then
    append_unique_missing_host_tool gh
    append_unique_missing_brew_formula gh
  fi
  if ! command -v git >/dev/null 2>&1; then
    append_unique_missing_host_tool git
    append_unique_missing_brew_formula git
  fi
  if ! command -v go >/dev/null 2>&1; then
    append_unique_missing_host_tool go
    append_unique_missing_brew_formula go
  fi
}

brew_bin() {
  local candidate=""

  candidate="$(command -v brew 2>/dev/null || true)"
  if [[ -n "${candidate}" && -x "${candidate}" ]]; then
    printf '%s\n' "${candidate}"
    return 0
  fi
  return 1
}

install_missing_required_host_dependencies() {
  local brew_path=""

  collect_missing_required_host_dependencies
  if [[ "${#MISSING_BREW_FORMULAS[@]}" -eq 0 ]]; then
    return 0
  fi

  brew_path="$(brew_bin || true)"
  if [[ -z "${brew_path}" ]]; then
    echo "Homebrew is required to auto-install missing Workcell host packages." >&2
    echo "Missing required host packages: ${MISSING_BREW_FORMULAS[*]}" >&2
    echo "Re-run with --no-install-deps to install only the launcher and print a warning summary instead." >&2
    exit 1
  fi

  echo "Installing required host packages via Homebrew: ${MISSING_BREW_FORMULAS[*]}"
  "${brew_path}" install "${MISSING_BREW_FORMULAS[@]}"
  collect_missing_required_host_dependencies
  if [[ "${#MISSING_BREW_FORMULAS[@]}" -gt 0 ]]; then
    echo "Workcell could not verify these required host packages after Homebrew install: ${MISSING_BREW_FORMULAS[*]}" >&2
    exit 1
  fi
}

warn_missing_required_host_dependencies() {
  if [[ "${#MISSING_BREW_FORMULAS[@]}" -eq 0 ]]; then
    return 0
  fi

  echo ""
  echo "Workcell warning: the launcher was installed without the full required host toolchain."
  echo "Missing required host packages: ${MISSING_BREW_FORMULAS[*]}"
  echo "Install them with:"
  echo "  brew install ${MISSING_BREW_FORMULAS[*]}"
  echo "Or rerun ./scripts/install.sh without --no-install-deps once Homebrew is available."
}

write_debug_wrapper() {
  local install_path="$1"
  local root_dir="$2"
  local debug_dir="$3"

  mkdir -p "${debug_dir}"
  chmod 0700 "${debug_dir}" 2>/dev/null || true
  rm -f "${install_path}"
  local quoted_root_dir
  quoted_root_dir="$(printf '%q' "${root_dir}")"
  local quoted_debug_dir
  quoted_debug_dir="$(printf '%q' "${debug_dir}")"
  cat >"${install_path}" <<EOF
#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail
# Workcell debug installer wrapper

ROOT_DIR=${quoted_root_dir}
DEBUG_DIR=${quoted_debug_dir}
DEFAULT_DEBUG_LOG="\${DEBUG_DIR}/latest-debug.log"
HAS_DEBUG_LOG=0
HAS_REBUILD=0
SKIP_AUTO_DEBUG=0
ARG=""
declare -a EXTRA_ARGS=()

mkdir -p "\${DEBUG_DIR}"
chmod 0700 "\${DEBUG_DIR}" 2>/dev/null || true

for ARG in "\$@"; do
  case "\${ARG}" in
    --auth-status | --doctor | --gc | --help | --inspect | --logs | --prepare-only | -h | auth-status | doctor | gc | help | inspect | logs | publish-pr)
      SKIP_AUTO_DEBUG=1
      ;;
    session)
      SKIP_AUTO_DEBUG=1
      ;;
    --debug-log)
      HAS_DEBUG_LOG=1
      ;;
    --rebuild)
      HAS_REBUILD=1
      ;;
  esac
done

if [[ "\${SKIP_AUTO_DEBUG}" -eq 0 ]] && [[ "\${HAS_DEBUG_LOG}" -eq 0 ]]; then
  EXTRA_ARGS+=(--debug-log "\${DEFAULT_DEBUG_LOG}")
fi
if [[ "\${SKIP_AUTO_DEBUG}" -eq 0 ]] && [[ "\${HAS_REBUILD}" -eq 0 ]]; then
  EXTRA_ARGS+=(--rebuild)
fi

exec "\${ROOT_DIR}/scripts/workcell" \${EXTRA_ARGS[@]:+"\${EXTRA_ARGS[@]}"} "\$@"
EOF
  chmod 0755 "${install_path}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --debug)
      DEBUG_INSTALL=1
      shift
      ;;
    --debug-dir)
      DEBUG_DIR="$(resolve_output_dir "${2:?--debug-dir requires a path}")"
      shift 2
      ;;
    --no-install-deps)
      AUTO_INSTALL_DEPS=0
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "Unsupported install option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

require_supported_macos_host_arch
if running_on_supported_macos_host; then
  if [[ "${AUTO_INSTALL_DEPS}" -eq 1 ]]; then
    install_missing_required_host_dependencies
  else
    collect_missing_required_host_dependencies
  fi
fi

mkdir -p "${INSTALL_DIR}"
mkdir -p "${MAN_DIR}"
if [[ "${DEBUG_INSTALL}" -eq 1 ]]; then
  write_debug_wrapper "${INSTALL_PATH}" "${ROOT_DIR}" "${DEBUG_DIR}"
else
  ln -sf "${ROOT_DIR}/scripts/workcell" "${INSTALL_PATH}"
fi
ln -sf "${ROOT_DIR}/man/workcell.1" "${MAN_PATH}"

echo "Installed Workcell to ${INSTALL_PATH}"
echo "Installed man page to ${MAN_PATH}"
if [[ "${DEBUG_INSTALL}" -eq 1 ]]; then
  echo "Debug launcher wrapper enabled."
  echo "Launcher log: ${DEBUG_DIR}/latest-debug.log"
  echo "Launch-time rebuilds: enabled"
fi
if running_on_supported_macos_host && [[ "${AUTO_INSTALL_DEPS}" -eq 0 ]]; then
  warn_missing_required_host_dependencies
fi
if [[ ":${PATH}:" != *":${INSTALL_DIR}:"* ]]; then
  case "${SHELL:-/bin/zsh}" in
    */bash) rc_file=".bashrc" ;;
    *) rc_file=".zshrc" ;;
  esac
  echo ""
  echo "Add this line to ~/${rc_file}:"
  # shellcheck disable=SC2016
  echo '  export PATH="$HOME/.local/bin:$PATH"'
  echo ""
  echo "Or run it now for this session only."
fi
