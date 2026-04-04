#!/bin/bash -p
# Host-side validation harness.  Runs repo-level checks (linting,
# compilation, tests, manifest verification) directly on the host using
# locally installed tools.  CI is the authority on exact tool versions;
# this script catches issues early without Docker overhead.
#
# This is a build-time tool, not a runtime entry point; it does not
# launch a Workcell session or go through the launcher's runtime boundary.
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/local/go/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
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
USE_DOCKER=0

default_snapshot_parent() {
  local raw_parent=""

  if [[ -n "${WORKCELL_VALIDATION_SNAPSHOT_PARENT:-}" ]]; then
    raw_parent="${WORKCELL_VALIDATION_SNAPSHOT_PARENT}"
  elif [[ -n "${XDG_CACHE_HOME:-}" ]]; then
    raw_parent="${XDG_CACHE_HOME}/workcell-validation-snapshots"
  elif [[ -n "${HOME:-}" ]]; then
    if [[ "${OSTYPE:-}" == darwin* ]]; then
      raw_parent="${HOME}/Library/Caches/workcell-validation-snapshots"
    else
      raw_parent="${HOME}/.cache/workcell-validation-snapshots"
    fi
  else
    raw_parent="$(dirname "${ROOT_DIR}")"
  fi

  case "${raw_parent}" in
    /*)
      printf '%s\n' "${raw_parent}"
      ;;
    *)
      printf '%s\n' "${ROOT_DIR}/${raw_parent}"
      ;;
  esac
}

run_validate_repo_in_validator_snapshot() {
  local image_tag="$1"
  shift

  local snapshot_parent=""
  snapshot_parent="$(default_snapshot_parent)"
  mkdir -p "${snapshot_parent}"

  # shellcheck disable=SC2016
  env \
    "WORKCELL_VALIDATION_SNAPSHOT_PARENT=${snapshot_parent}" \
    "${ROOT_DIR}/scripts/with-validation-snapshot.sh" \
    --repo "${ROOT_DIR}" \
    --mode worktree \
    --include-untracked \
    -- \
    /bin/bash -p -c '
      workspace="$(pwd -P)"
      docker run --rm \
        -v "${workspace}:/workspace" \
        -w /workspace \
        "$1" \
        ./scripts/validate-repo.sh "${@:2}"
      ./scripts/verify-invariants.sh
    ' bash "${image_tag}" "$@"
}

# Handle flags that build-and-test.sh owns before passing the rest to
# validate-repo.sh.
for arg in "$@"; do
  case "${arg}" in
    --install)
      exec "${ROOT_DIR}/scripts/install-dev-tools.sh"
      ;;
    --docker)
      USE_DOCKER=1
      ;;
    -h | --help)
      cat <<EOF
Usage: build-and-test.sh [OPTIONS]

Host-side validation harness. Runs check-pinned-inputs.sh and
validate-repo.sh directly on the host using locally installed tools. Use
--docker to rerun repo validation inside the CI validator container from a
disposable snapshot of the current worktree.

Options:
  --install     Install missing host tools (brew/apt) and set up Python venv
  --docker      Run repo validation inside the validator container
  -h, --help    Show this help
EOF
      exit 0
      ;;
  esac
done

validate_args=()
for arg in "$@"; do
  [[ "${arg}" == "--docker" ]] || validate_args+=("${arg}")
done

# Activate the repo venv if it exists (provides pytest).
if [[ -f "${ROOT_DIR}/.venv/bin/activate" ]]; then
  # shellcheck source=/dev/null
  source "${ROOT_DIR}/.venv/bin/activate"
fi

# --- Host-side checks (same as CI, before validation) ---
"${ROOT_DIR}/scripts/check-pinned-inputs.sh"
"${ROOT_DIR}/scripts/verify-build-input-manifest.sh"

if [[ "${USE_DOCKER}" -eq 1 ]]; then
  IMAGE_TAG="workcell-validator:local"

  if ! command -v docker &>/dev/null; then
    echo "ERROR: --docker requires Docker. Install Docker Desktop or colima." >&2
    exit 1
  fi

  echo "Building validator image..."
  docker build -f "${ROOT_DIR}/tools/validator/Dockerfile" -t "${IMAGE_TAG}" "${ROOT_DIR}"
  run_validate_repo_in_validator_snapshot "${IMAGE_TAG}" ${validate_args[@]:+"${validate_args[@]}"}
else
  # --- Repo validation (linting, compilation, tests, manifests) ---
  "${ROOT_DIR}/scripts/validate-repo.sh" ${validate_args[@]:+"${validate_args[@]}"}
fi
