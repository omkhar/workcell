#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
IMAGE_TAG="workcell-validator:local"

# --list runs on the host without Docker or image builds.
for arg in "$@"; do
  case "${arg}" in
    -l | --list)
      "${ROOT_DIR}/scripts/validate-repo.sh" --list
      exit 0
      ;;
  esac
done

if ! command -v docker &>/dev/null; then
  echo "ERROR: docker is required. Install Docker Desktop or colima." >&2
  exit 1
fi

# --- Host-side checks (same as CI, before the container step) ---
"${ROOT_DIR}/scripts/check-pinned-inputs.sh"

echo "Building validator image..."
docker build -f "${ROOT_DIR}/tools/validator/Dockerfile" -t "${IMAGE_TAG}" "${ROOT_DIR}"

# --- Container-side checks (same container as CI) ---
docker run --rm \
  -v "${ROOT_DIR}:/workspace" \
  -w /workspace \
  "${IMAGE_TAG}" \
  ./scripts/validate-repo.sh "$@"

# --- Host-side checks (same as CI, after the container step) ---
"${ROOT_DIR}/scripts/verify-invariants.sh"
