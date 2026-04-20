#!/bin/bash -p
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-host-support-matrix.XXXXXX")"

cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

GENERATED_PATH="${TMP_DIR}/host-support-matrix.md"
COMMITTED_PATH="${ROOT_DIR}/docs/host-support-matrix.md"

"${ROOT_DIR}/scripts/generate-host-support-matrix-doc.sh" "${GENERATED_PATH}"

if ! diff -u "${COMMITTED_PATH}" "${GENERATED_PATH}"; then
  echo "docs/host-support-matrix.md is out of date; regenerate it from policy/host-support-matrix.tsv." >&2
  exit 1
fi
