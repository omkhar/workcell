#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
source "${ROOT_DIR}/scripts/ci/lib/local-docker-parity.sh"
VALIDATOR_IMAGE="${WORKCELL_VALIDATOR_IMAGE:-}"
VALIDATE_PROFILE="${WORKCELL_VALIDATE_REPO_PROFILE:-release-preflight}"
WORKSPACE="${WORKCELL_VALIDATOR_WORKSPACE:-${ROOT_DIR}}"
SKIP_HEAVY_SHELLCHECK="${WORKCELL_SKIP_HEAVY_HOST_SHELLCHECK:-0}"

validator_passwd=""
cleanup() {
  [[ -z "${validator_passwd}" ]] || rm -f "${validator_passwd}"
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
WORKSPACE="$(cd "${WORKSPACE}" && pwd -P)"

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

# The workload runs as the caller's uid to keep the bind-mounted workspace
# writable, but that uid has no /etc/passwd entry, so glibc getpwuid() fails
# and anything resolving the invoking user dies with "No user exists for uid
# <n>".  ssh-keygen is one of those, and git shells out to it for both
# `gpg.format = ssh` signing and verification, so the pre-push hook tests
# cannot sign or verify a commit.  Give the container a passwd file carrying
# an entry for the runtime uid, appended only when the image lacks one so an
# existing uid keeps its own home.  The home field matches HOME below, so
# identity- and env-based home discovery agree on one path.
validator_passwd="$(mktemp "${TMPDIR:-/tmp}/workcell-validator-passwd.XXXXXX")"
workcell_ci_docker run --rm --entrypoint /bin/bash "${VALIDATOR_IMAGE}" \
  -lc 'cat /etc/passwd' >"${validator_passwd}"
if ! awk -F: -v uid="${validator_uid}" '$3 == uid { found = 1 } END { exit !found }' \
  "${validator_passwd}"; then
  printf 'workcell-ci:x:%s:%s:workcell ci:%s:/bin/bash\n' \
    "${validator_uid}" "${validator_gid}" "${validator_home}" >>"${validator_passwd}"
fi
chmod 0444 "${validator_passwd}"

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
  --mount "type=bind,source=${validator_passwd},target=/etc/passwd,readonly" \
  -w /workspace \
  "${VALIDATOR_IMAGE}" \
  -lc '
    set -euo pipefail
    mkdir -p "${HOME}" "${XDG_CACHE_HOME}" "${GOCACHE}" "${GOMODCACHE}" "${CARGO_TARGET_DIR}" "${TMPDIR}"
    ./scripts/validate-repo.sh
  '
