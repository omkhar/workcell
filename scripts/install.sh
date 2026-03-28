#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALL_DIR="${HOME}/.local/bin"
INSTALL_PATH="${INSTALL_DIR}/workcell"
MAN_DIR="${HOME}/.local/share/man/man1"
MAN_PATH="${MAN_DIR}/workcell.1"

mkdir -p "${INSTALL_DIR}"
mkdir -p "${MAN_DIR}"
ln -sf "${ROOT_DIR}/scripts/workcell" "${INSTALL_PATH}"
ln -sf "${ROOT_DIR}/man/workcell.1" "${MAN_PATH}"

echo "Installed Workcell to ${INSTALL_PATH}"
echo "Installed man page to ${MAN_PATH}"
if [[ ":${PATH}:" != *":${INSTALL_DIR}:"* ]]; then
  echo "Add ${INSTALL_DIR} to PATH to run it without a full path."
fi
