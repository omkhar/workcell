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
# re-expanding generator would substitute, a backtick that an unquoted
# re-interpolation would run as a command substitution, a `--`-prefixed
# component that a substring flag check mistakes for an option, and ~80
# characters of padding that pushes any AF_UNIX path derived from TMPDIR past
# sun_path.  Three review findings were first reproduced by hand this way; the
# backtick adds a fourth shape without changing any of the others.  A
# backtick is a legal filename character, so mkdir -p below still creates the
# path; it is only hostile to code that re-interpolates the path unquoted.
if [[ "${HOSTILE_ENV}" == "tmpdir" ]]; then
  validator_tmp="${validator_tmp}/hostile \$HOME \`id\` --hostname/$(printf 'p%.0s' {1..80})"
fi
# The root and uidmap axes run as a uid that owns nothing in the bind mount,
# and git refuses a repository owned by another uid: "detected dubious
# ownership in repository at '/workspace'".  scripts/ci-plan.sh runs git with
# no global and no system configuration on purpose, so safe.directory is not
# reachable and is not the fix here; align the ownership instead.  The copy is
# made inside the container by the validator uid itself, so it owns the result
# with no privileged step on the host and no change to either axis: root still
# runs as uid 0 and uidmap still runs as a uid the bind mount and the image
# know nothing about.
validator_workspace_copy=""
case "${HOSTILE_ENV}" in
  root | uidmap) validator_workspace_copy="${validator_home}/workspace" ;;
esac

setup_workcell_ci_docker

validator_passwd="$(workcell_ci_validator_passwd_file \
  workcell_ci_docker "${VALIDATOR_IMAGE}" \
  "${validator_uid}" "${validator_gid}" "${validator_home}" "${WORKSPACE}")"
validator_passwd_mount="$(workcell_ci_workspace_mount_spec "${validator_passwd}" true /etc/passwd)"

require_workcell_ci_workspace_mount "${VALIDATOR_IMAGE}" "${WORKSPACE}"
validator_workspace_mount="$(workcell_ci_workspace_mount_spec "${WORKSPACE}" false)"

# WORKCELL_VALIDATOR_CACHE_DIR is a host directory that persists the Go build,
# Go module and cargo target caches across runs.  It is mounted at its own
# top-level path, because Docker creates the parents of a mount target as root
# and a target under ${validator_home} would make the home unwritable.  Mount it only when the
# container uid is the host uid: the root and uidmap axes cannot write to it
# (or would leave root-owned files in it), so they keep the in-container cache.
cache_mount_args=()
if [[ -n "${WORKCELL_VALIDATOR_CACHE_DIR:-}" && "${validator_uid}" == "$(id -u)" ]]; then
  # Fail closed: the source must be an absolute path whose final component is a
  # real directory, owned by the host uid and owner-only, so a
  # caller cannot aim this read-write mount at an unrelated host directory.
  case "${WORKCELL_VALIDATOR_CACHE_DIR}" in
    /*) ;;
    *)
      echo "WORKCELL_VALIDATOR_CACHE_DIR must be an absolute path" >&2
      exit 1
      ;;
  esac
  if [[ -L "${WORKCELL_VALIDATOR_CACHE_DIR}" ]]; then
    echo "WORKCELL_VALIDATOR_CACHE_DIR must not be a symlink" >&2
    exit 1
  fi
  (umask 077 && mkdir -p "${WORKCELL_VALIDATOR_CACHE_DIR}")
  # Resolve every parent link once and use only the canonical path below, for
  # the checks and for the mount, so Docker never receives a path whose parents
  # can be swapped for links.  The owner-only mode check then closes the
  # directory itself to other local users.
  cache_dir="$(cd -P -- "${WORKCELL_VALIDATOR_CACHE_DIR}" && pwd -P)" || {
    echo "WORKCELL_VALIDATOR_CACHE_DIR cannot be resolved" >&2
    exit 1
  }
  # Take the directory over as owner-only.  A directory that actions/cache or an
  # earlier run created with a looser mode is tightened first, but only when
  # this uid owns it, so an unowned directory still fails closed.
  if [[ ! -d "${cache_dir}" || ! -O "${cache_dir}" ]]; then
    echo "WORKCELL_VALIDATOR_CACHE_DIR must be a directory owned by the host uid" >&2
    exit 1
  fi
  chmod 700 "${cache_dir}"
  # The find status is checked on its own: inside a test expression a failing
  # find would yield empty output and pass as safe.
  cache_unsafe_mode="$(find "${cache_dir}" -maxdepth 0 -perm /077 -print)" || cache_unsafe_mode="find-failed"
  if [[ -n "${cache_unsafe_mode}" ]]; then
    echo "WORKCELL_VALIDATOR_CACHE_DIR must be owner-only" >&2
    exit 1
  fi
  validator_cache="/workcell-validator-cache"
  cache_mount_args=(--mount "$(workcell_ci_workspace_mount_spec "${cache_dir}" false "${validator_cache}")")
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
  -e WORKCELL_VALIDATOR_WORKSPACE_COPY="${validator_workspace_copy}" \
  --mount "${validator_workspace_mount}" \
  --mount "${validator_passwd_mount}" \
  ${cache_mount_args[@]+"${cache_mount_args[@]}"} \
  -w /workspace \
  "${VALIDATOR_IMAGE}" \
  -lc '
    set -euo pipefail
    mkdir -p "${HOME}" "${XDG_CACHE_HOME}" "${GOCACHE}" "${GOMODCACHE}" "${CARGO_TARGET_DIR}" "${TMPDIR}"
    if [[ -n "${WORKCELL_VALIDATOR_WORKSPACE_COPY}" ]]; then
      # -d keeps symlinks and hard links as they are, and the mode and
      # timestamp list omits ownership on purpose: as uid 0 a preserving copy
      # would reproduce the bind owner and land back on the dubious-ownership
      # refusal this copy exists to remove.
      mkdir -p "${WORKCELL_VALIDATOR_WORKSPACE_COPY}"
      cp -dR --preserve=mode,timestamps /workspace/. "${WORKCELL_VALIDATOR_WORKSPACE_COPY}/"
      cd "${WORKCELL_VALIDATOR_WORKSPACE_COPY}"
    fi
    ./scripts/validate-repo.sh
  '
