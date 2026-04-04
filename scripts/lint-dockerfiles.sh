#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REQUIRE_DOCKERFILE_LINT="${WORKCELL_REQUIRE_DOCKERFILE_LINT:-0}"

mapfile -d '' -t dockerfiles < <(
  find "${ROOT_DIR}" \
    -path "${ROOT_DIR}/.git" -prune -o \
    -path "${ROOT_DIR}/dist" -prune -o \
    -path "${ROOT_DIR}/tmp" -prune -o \
    -path "${ROOT_DIR}/runtime/container/providers/node_modules" -prune -o \
    -type f -name 'Dockerfile' -print0 | sort -z
)

if [[ "${#dockerfiles[@]}" -eq 0 ]]; then
  echo "No Dockerfiles found."
  exit 0
fi

if ! command -v hadolint >/dev/null 2>&1; then
  if [[ "${REQUIRE_DOCKERFILE_LINT}" == "1" ]]; then
    echo "Missing required tool: hadolint" >&2
    exit 1
  fi
  echo "Skipping Dockerfile lint because hadolint is not installed locally." >&2
  exit 0
fi

hadolint "${dockerfiles[@]}"

echo "Dockerfile lint passed."
