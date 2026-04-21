#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

echo "[ci/pin-hygiene] pinned input policy"
"${ROOT_DIR}/scripts/check-pinned-inputs.sh"

echo "[ci/pin-hygiene] GitHub macOS release test runners"
"${ROOT_DIR}/scripts/verify-github-macos-release-test-runners.sh" macos-26 macos-15

echo "[ci/pin-hygiene] upstream Codex release"
"${ROOT_DIR}/scripts/verify-upstream-codex-release.sh"

echo "[ci/pin-hygiene] upstream Claude release"
"${ROOT_DIR}/scripts/verify-upstream-claude-release.sh"

echo "[ci/pin-hygiene] upstream Gemini release"
"${ROOT_DIR}/scripts/verify-upstream-gemini-release.sh"

echo "[ci/pin-hygiene] pinned upstream refresh status"
"${ROOT_DIR}/scripts/update-upstream-pins.sh" --check

echo "Workcell shared pin-hygiene job passed."
