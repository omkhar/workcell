#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALL_DIR="${HOME}/.local/bin"
INSTALL_PATH="${INSTALL_DIR}/workcell"
MAN_DIR="${HOME}/.local/share/man/man1"
MAN_PATH="${MAN_DIR}/workcell.1"
DEBUG_INSTALL=0
DEBUG_DIR="${HOME}/.config/workcell/debug"
GO_BIN="${WORKCELL_GO_BIN:-}"

usage() {
  cat <<'EOF'
Usage: ./scripts/install.sh [--debug] [--debug-dir PATH]

Install Workcell into ~/.local/bin and the man page into ~/.local/share/man/man1.

Options:
  --debug           Install a wrapper that auto-enables launcher debug logging
                    and launch-time rebuilds
  --debug-dir PATH  Override the host directory used by the debug wrapper
  -h, --help        Show this help text
EOF
}

resolve_output_dir() {
  resolve_go_bin
  (cd "${ROOT_DIR}" && "${GO_BIN}" run ./cmd/workcell-hostutil path resolve --base "$(pwd -P)" "$1")
}

resolve_go_bin() {
  if [[ -n "${GO_BIN}" && -x "${GO_BIN}" ]]; then
    return 0
  fi
  if GO_BIN="$(command -v go 2>/dev/null)"; then
    return 0
  fi
  for candidate in \
    /opt/homebrew/bin/go \
    /usr/local/go/bin/go \
    /usr/local/bin/go \
    /usr/bin/go; do
    if [[ -x "${candidate}" ]]; then
      GO_BIN="${candidate}"
      return 0
    fi
  done
  echo "Missing required tool: go" >&2
  exit 1
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
