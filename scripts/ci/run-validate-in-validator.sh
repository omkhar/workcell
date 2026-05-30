#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
source "${ROOT_DIR}/scripts/ci/lib/local-docker-parity.sh"
VALIDATOR_IMAGE="${WORKCELL_VALIDATOR_IMAGE:-}"
VALIDATE_PROFILE="${WORKCELL_VALIDATE_REPO_PROFILE:-release-preflight}"
WORKSPACE="${WORKCELL_VALIDATOR_WORKSPACE:-${ROOT_DIR}}"
SKIP_HEAVY_SHELLCHECK="${WORKCELL_SKIP_HEAVY_HOST_SHELLCHECK:-0}"

cleanup() {
  cleanup_workcell_ci_docker
}
trap cleanup EXIT

if [[ -z "${VALIDATOR_IMAGE}" ]]; then
  echo "WORKCELL_VALIDATOR_IMAGE is required" >&2
  exit 2
fi
if [[ ! -d "${WORKSPACE}" ]]; then
  echo "Validator workspace does not exist: ${WORKSPACE}" >&2
  exit 2
fi

validator_uid="$(id -u)"
validator_gid="$(id -g)"
# GitHub-hosted runners are exclusive per-job, so the /tmp/workcell-home-<uid>
# planted-symlink TOCTOU surface is not reachable here.  Keep the
# predictable path for CI to preserve test-fixture stability across
# scenarios that rely on resolve_workcell_real_home's `synthetic`
# fallback finding the same path multiple times within one run.  The
# dev-side build-and-test.sh and verify-release-bundle.sh use mktemp -d
# because those run on shared developer hosts where the TOCTOU is real.
validator_home="/tmp/workcell-home-${validator_uid}"
validator_cache="${validator_home}/.cache"
validator_tmp="${validator_home}/.tmp"

setup_workcell_ci_docker

# Optional persistent build cache: when WORKCELL_VALIDATOR_HOST_HOME_DIR is set
# (CI restores its .cache subdir via actions/cache), bind-mount it as the
# validator HOME so Go/Cargo build artifacts under ${validator_cache} survive
# across runs.  We mount the home dir itself (not just .cache) because mounting
# a subdir would make Docker create the home as root, blocking the unprivileged
# container user from creating sibling .tmp.  The host dir is created by — and
# therefore owned by — the same uid the container runs as, so the user can
# write.  Default (env unset) keeps the run byte-identical to the ephemeral
# behavior the other callers rely on.  Only incremental build/test artifacts are
# persisted (content-addressed by Go, fingerprinted by Cargo — a stale entry is
# a rebuild, never a wrong pass); the reproducible-build job is separate and
# stays --no-cache.
host_home_mount=()
host_home_dir="${WORKCELL_VALIDATOR_HOST_HOME_DIR:-}"
if [[ -n "${host_home_dir}" ]]; then
  mkdir -p "${host_home_dir}"
  host_home_mount=(-v "${host_home_dir}:${validator_home}")
fi

# shellcheck disable=SC2016
workcell_ci_docker run --rm \
  --user "${validator_uid}:${validator_gid}" \
  --entrypoint /bin/bash \
  -e WORKCELL_SKIP_HEAVY_HOST_SHELLCHECK="${SKIP_HEAVY_SHELLCHECK}" \
  -e WORKCELL_VALIDATE_REPO_PROFILE="${VALIDATE_PROFILE}" \
  -e HOME="${validator_home}" \
  -e XDG_CACHE_HOME="${validator_cache}" \
  -e GOCACHE="${validator_cache}/go-build" \
  -e GOMODCACHE="${validator_cache}/go-mod" \
  -e CARGO_TARGET_DIR="${validator_cache}/cargo-target" \
  -e TMPDIR="${validator_tmp}" \
  "${host_home_mount[@]}" \
  -v "${WORKSPACE}:/workspace" \
  -w /workspace \
  "${VALIDATOR_IMAGE}" \
  -lc '
    set -euo pipefail
    mkdir -p "${HOME}" "${XDG_CACHE_HOME}" "${GOCACHE}" "${GOMODCACHE}" "${CARGO_TARGET_DIR}" "${TMPDIR}"
    ./scripts/validate-repo.sh
  '
