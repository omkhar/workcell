#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
source "${ROOT_DIR}/scripts/ci/lib/local-docker-parity.sh"
source "${ROOT_DIR}/scripts/ci/lib/validator-passwd.sh"
VALIDATOR_IMAGE="${WORKCELL_VALIDATOR_IMAGE:-}"
VALIDATE_PROFILE="${WORKCELL_VALIDATE_REPO_PROFILE:-release-preflight}"
WORKSPACE="${WORKCELL_VALIDATOR_WORKSPACE:-${ROOT_DIR}}"
SKIP_HEAVY_SHELLCHECK="${WORKCELL_SKIP_HEAVY_HOST_SHELLCHECK:-0}"
# WORKCELL_HOSTILE_ENV names one hostile axis for the advisory lane in
# .github/workflows/ci.yml.  Each axis is a shape that review has already
# caught defects with, and each one runs the ordinary validation underneath, so
# a failure is a defect in the repository rather than in the lane.
HOSTILE_ENV="${WORKCELL_HOSTILE_ENV:-none}"

validator_passwd=""
hostile_root=""
cleanup() {
  [[ -z "${validator_passwd}" ]] || rm -f "${validator_passwd}"
  [[ -z "${hostile_root}" ]] || rm -rf "${hostile_root}"
  cleanup_workcell_ci_docker
}
trap cleanup EXIT

case "${HOSTILE_ENV}" in
  none | tmpdir | workspace | root | uidmap) ;;
  *)
    echo "Unsupported WORKCELL_HOSTILE_ENV: ${HOSTILE_ENV}" >&2
    exit 2
    ;;
esac

if [[ -z "${VALIDATOR_IMAGE}" ]]; then
  echo "WORKCELL_VALIDATOR_IMAGE is required" >&2
  exit 2
fi
if [[ ! -d "${WORKSPACE}" ]]; then
  echo "Validator workspace does not exist: ${WORKSPACE}" >&2
  exit 2
fi
WORKSPACE="$(cd "${WORKSPACE}" && pwd -P)"

if [[ "${HOSTILE_ENV}" == "workspace" ]]; then
  # A bind source holding a space, a comma and a --prefixed token.  The comma
  # is the one that matters most: the --mount record is CSV, so a source that
  # carries one has to survive the encoder rather than split the record.  The
  # copy is required because the checkout itself lives at a plain path.
  hostile_root="$(mktemp -d "${TMPDIR:-/tmp}/workcell-hostile.XXXXXX")"
  hostile_workspace="${hostile_root}/hostile ws,dir --workspace"
  mkdir -p "${hostile_workspace}"
  cp -a "${WORKSPACE}/." "${hostile_workspace}/"
  WORKSPACE="$(cd "${hostile_workspace}" && pwd -P)"
fi

validator_uid="$(id -u)"
validator_gid="$(id -g)"
case "${HOSTILE_ENV}" in
  root)
    # The suite behaves differently as root, and review has already found
    # assertions that only hold for an unprivileged uid: a chmod that root
    # ignores, and a write-only file root can still read.
    validator_uid=0
    validator_gid=0
    ;;
  uidmap)
    # A uid that owns none of the bind-mounted files and that the image has no
    # passwd record for.  That is what a remapped-uid container looks like from
    # inside, and it is where a readability assumption breaks.
    validator_uid=$((validator_uid + 1))
    ;;
esac
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
# The tmpdir axis points TMPDIR at a directory whose name carries the shapes
# that have broken this repository under review: a space, a literal `$` that a
# re-expanding generator would substitute, a `--`-prefixed component that a
# substring flag check mistakes for an option, and ~80 characters of padding
# that pushes any AF_UNIX path derived from TMPDIR past sun_path.  Three review
# findings were first reproduced by hand this way.
if [[ "${HOSTILE_ENV}" == "tmpdir" ]]; then
  validator_tmp="${validator_tmp}/hostile \$HOME --hostname/$(printf 'p%.0s' {1..80})"
fi

setup_workcell_ci_docker

validator_passwd="$(workcell_ci_validator_passwd_file \
  workcell_ci_docker "${VALIDATOR_IMAGE}" \
  "${validator_uid}" "${validator_gid}" "${validator_home}" "${WORKSPACE}")"
validator_passwd_mount="$(workcell_ci_workspace_mount_spec "${validator_passwd}" true /etc/passwd)"

require_workcell_ci_workspace_mount "${VALIDATOR_IMAGE}" "${WORKSPACE}"
validator_workspace_mount="$(workcell_ci_workspace_mount_spec "${WORKSPACE}" false)"

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
  --mount "${validator_workspace_mount}" \
  --mount "${validator_passwd_mount}" \
  -w /workspace \
  "${VALIDATOR_IMAGE}" \
  -lc '
    set -euo pipefail
    mkdir -p "${HOME}" "${XDG_CACHE_HOME}" "${GOCACHE}" "${GOMODCACHE}" "${CARGO_TARGET_DIR}" "${TMPDIR}"
    ./scripts/validate-repo.sh
  '
