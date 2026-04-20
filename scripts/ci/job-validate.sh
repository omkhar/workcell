#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
source "${ROOT_DIR}/scripts/ci/lib/local-docker-parity.sh"
PROFILE="${WORKCELL_CI_VALIDATE_PROFILE:-pr-parity}"
VALIDATOR_IMAGE="${WORKCELL_VALIDATOR_IMAGE:-}"
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "${ROOT_DIR}" log -1 --pretty=%ct 2>/dev/null || printf '0')}"
ARCHIVE_REF="${WORKCELL_CI_ARCHIVE_REF:-$(git -C "${ROOT_DIR}" rev-parse HEAD 2>/dev/null || printf 'HEAD')}"
REPOSITORY_NAME="${GITHUB_REPOSITORY:-workcell/local}"
ARTIFACT_DIR="${WORKCELL_CI_INSTALL_ARTIFACT_DIR:-}"
KEEP_ARTIFACT_DIR=0
SKIP_RELEASE_BUNDLE="${WORKCELL_CI_VALIDATE_SKIP_RELEASE_BUNDLE:-0}"

usage() {
  cat <<'EOF'
Usage: job-validate.sh [--profile repo-core|pr-parity|release-preflight]

Run the shared validator-backed repository validation job used by local parity
and GitHub CI. The default profile mirrors the standard PR validate lane.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --profile)
      PROFILE="${2:-}"
      [[ -n "${PROFILE}" ]] || {
        echo "--profile requires a value" >&2
        exit 2
      }
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

case "${PROFILE}" in
  repo-core | pr-parity | release-preflight) ;;
  *)
    echo "Unsupported validate profile: ${PROFILE}" >&2
    exit 2
    ;;
esac

if [[ -z "${ARTIFACT_DIR}" ]]; then
  ARTIFACT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-ci-install.XXXXXX")"
  KEEP_ARTIFACT_DIR=0
else
  mkdir -p "${ARTIFACT_DIR}"
  KEEP_ARTIFACT_DIR=1
fi

cleanup() {
  cleanup_workcell_ci_docker
  if [[ "${KEEP_ARTIFACT_DIR}" -eq 0 ]]; then
    rm -rf "${ARTIFACT_DIR}"
  fi
}
trap cleanup EXIT

echo "[ci/validate] pinned input policy"
"${ROOT_DIR}/scripts/check-pinned-inputs.sh"

echo "[ci/validate] GitHub macOS release test runners"
"${ROOT_DIR}/scripts/verify-github-macos-release-test-runners.sh" macos-26 macos-15

echo "[ci/validate] build input manifest determinism"
"${ROOT_DIR}/scripts/verify-build-input-manifest.sh"

echo "[ci/validate] upstream Codex release"
"${ROOT_DIR}/scripts/verify-upstream-codex-release.sh"

echo "[ci/validate] upstream Claude release"
"${ROOT_DIR}/scripts/verify-upstream-claude-release.sh"

echo "[ci/validate] upstream Gemini release"
"${ROOT_DIR}/scripts/verify-upstream-gemini-release.sh"

if [[ "${PROFILE}" == "release-preflight" ]]; then
  echo "[ci/validate] pinned upstream refresh status"
  "${ROOT_DIR}/scripts/update-upstream-pins.sh" --check
fi

echo "[ci/validate] validator image build"
VALIDATOR_IMAGE="$("${ROOT_DIR}/scripts/ci/build-validator-image.sh")"
export WORKCELL_VALIDATOR_IMAGE="${VALIDATOR_IMAGE}"

echo "[ci/validate] repository validation in validator"
WORKCELL_VALIDATE_REPO_PROFILE="${PROFILE}" \
  "${ROOT_DIR}/scripts/ci/run-validate-in-validator.sh"

echo "[ci/validate] host launcher invariants"
"${ROOT_DIR}/scripts/verify-invariants.sh"

if [[ "${PROFILE}" == "release-preflight" ]] && [[ "${SKIP_RELEASE_BUNDLE}" != "1" ]]; then
  echo "[ci/validate] release bundle reproducibility"
  WORKCELL_VALIDATOR_IMAGE="${VALIDATOR_IMAGE}" \
    SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}" \
    WORKCELL_RELEASE_BUNDLE_NAME="workcell-${ARCHIVE_REF}.tar.gz" \
    WORKCELL_RELEASE_BUNDLE_PREFIX="workcell-${ARCHIVE_REF}" \
    WORKCELL_RELEASE_BUNDLE_REF="${ARCHIVE_REF}" \
    "${ROOT_DIR}/scripts/verify-release-bundle.sh"
fi

if [[ "${PROFILE}" != "repo-core" ]]; then
  echo "[ci/validate] install artifact build"
  setup_workcell_ci_docker
  validator_uid="$(id -u)"
  validator_gid="$(id -g)"
  validator_home="/tmp/workcell-home-${validator_uid}"
  validator_cache="${validator_home}/.cache"
  validator_tmp="${validator_home}/.tmp"
  bundle_name="workcell-ci-${ARCHIVE_REF}.tar.gz"
  bundle_path="${ARTIFACT_DIR}/${bundle_name}"
  mkdir -p "${ARTIFACT_DIR}"
  # shellcheck disable=SC2016
  workcell_ci_docker run --rm \
    --user "${validator_uid}:${validator_gid}" \
    --entrypoint /bin/bash \
    -v "${ROOT_DIR}:/workspace" \
    -w /workspace \
    -e HOME="${validator_home}" \
    -e XDG_CACHE_HOME="${validator_cache}" \
    -e GOCACHE="${validator_cache}/go-build" \
    -e GOMODCACHE="${validator_cache}/go-mod" \
    -e CARGO_TARGET_DIR="${validator_cache}/cargo-target" \
    -e TMPDIR="${validator_tmp}" \
    -e SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}" \
    -e ARCHIVE_REF="${ARCHIVE_REF}" \
    -e BUNDLE_NAME="${bundle_name}" \
    -e BUNDLE_PREFIX="workcell-ci-${ARCHIVE_REF}/" \
    "${VALIDATOR_IMAGE}" \
    -lc '
      set -euo pipefail
      mkdir -p "${HOME}" "${XDG_CACHE_HOME}" "${GOCACHE}" "${GOMODCACHE}" "${CARGO_TARGET_DIR}" "${TMPDIR}"
      git -c safe.directory=/workspace archive \
        --format=tar \
        --mtime="@${SOURCE_DATE_EPOCH}" \
        --prefix="${BUNDLE_PREFIX}" \
        "${ARCHIVE_REF}" | gzip -n -9
    ' >"${bundle_path}"
  bundle_sha="$(sha256sum "${bundle_path}" | awk '{print $1}')"
  "${ROOT_DIR}/scripts/generate-homebrew-formula.sh" \
    "ci-${ARCHIVE_REF}" \
    "${bundle_sha}" \
    "${ARTIFACT_DIR}/workcell.rb" \
    --repository "${REPOSITORY_NAME}"
fi

echo "Workcell shared validate job passed."
