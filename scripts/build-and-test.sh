#!/bin/bash -p
# Host-side validation harness.  Runs repo-level checks (linting,
# compilation, tests, manifest verification) directly on the host using
# locally installed tools.  CI is the authority on exact tool versions;
# this script catches issues early without Docker overhead.
#
# This is a build-time tool, not a runtime entry point; it does not
# launch a Workcell session or go through the launcher's runtime boundary.
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  exec /usr/bin/env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME="${HOME:-/tmp}" \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_SANITIZED_ENTRYPOINT=1 \
    /bin/bash -p "$0" "$@"
fi
set -euo pipefail
export PATH="${HOME}/.cargo/bin:${TRUSTED_HOST_PATH}"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "build-and-test-entrypoint-ok"
  exit 0
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Handle flags that build-and-test.sh owns before passing the rest to
# validate-repo.sh.
for arg in "$@"; do
  case "${arg}" in
    --install)
      exec "${ROOT_DIR}/scripts/install-dev-tools.sh"
      ;;
    -h | --help)
      cat <<EOF
Usage: build-and-test.sh [OPTIONS]

Host-side validation harness. Runs check-pinned-inputs.sh and
validate-repo.sh directly on the host using locally installed tools.

Options:
  --install     Install missing host tools (brew/apt) and set up Python venv
  -h, --help    Show this help
EOF
      exit 0
      ;;
  esac
done

# Activate the repo venv if it exists (provides pytest).
if [[ -f "${ROOT_DIR}/.venv/bin/activate" ]]; then
  # shellcheck source=/dev/null
  source "${ROOT_DIR}/.venv/bin/activate"
fi

# --- Host-side checks (same as CI, before validation) ---
"${ROOT_DIR}/scripts/check-pinned-inputs.sh"
"${ROOT_DIR}/scripts/verify-build-input-manifest.sh"

# --- Repo validation (linting, compilation, tests, manifests) ---
"${ROOT_DIR}/scripts/validate-repo.sh" "$@"
