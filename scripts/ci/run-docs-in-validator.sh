#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
source "${ROOT_DIR}/scripts/ci/lib/local-docker-parity.sh"
source "${ROOT_DIR}/scripts/ci/lib/validator-passwd.sh"
VALIDATOR_IMAGE="${WORKCELL_VALIDATOR_IMAGE:-}"
WORKSPACE="${WORKCELL_VALIDATOR_WORKSPACE:-${ROOT_DIR}}"

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
# GitHub-hosted runners are exclusive per-job, so the planted-symlink
# TOCTOU surface that motivates mktemp in scripts/build-and-test.sh is
# not reachable here.  Keep the predictable path for CI to preserve
# test-fixture stability across scenarios.
validator_home="/tmp/workcell-home-${validator_uid}"
validator_cache="${validator_home}/.cache"
validator_tmp="${validator_home}/.tmp"

setup_workcell_ci_docker

# Same synthesized passwd entry the validate lane mounts: the workload runs
# as a uid the image has no /etc/passwd record for, and every tool that
# resolves the invoking user fails without one.
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
  --mount "${validator_workspace_mount}" \
  --mount "${validator_passwd_mount}" \
  -w /workspace \
  -e HOME="${validator_home}" \
  -e XDG_CACHE_HOME="${validator_cache}" \
  -e GOCACHE="${validator_cache}/go-build" \
  -e GOMODCACHE="${validator_cache}/go-mod" \
  -e CARGO_TARGET_DIR="${validator_cache}/cargo-target" \
  -e TMPDIR="${validator_tmp}" \
  "${VALIDATOR_IMAGE}" \
  -lc '
    set -euo pipefail
    mkdir -p "${HOME}" "${XDG_CACHE_HOME}" "${GOCACHE}" "${GOMODCACHE}" "${CARGO_TARGET_DIR}" "${TMPDIR}"
    mapfile -d "" doc_files < <(
      find /workspace \
        -path /workspace/.git -prune -o \
        -path /workspace/dist -prune -o \
        -path /workspace/tmp -prune -o \
        -path /workspace/runtime/container/providers/node_modules -prune -o \
        -path /workspace/tools/markdownlint/node_modules -prune -o \
        -path /workspace/runtime/container/rust/vendor -prune -o \
        -path /workspace/runtime/container/rust/target -prune -o \
        -type f \( -name "*.md" -o -name "*.txt" -o -name "*.1" \) -print0 | sort -z
    )
    codespell --config /workspace/.codespellrc "${doc_files[@]}"
    mandoc -Tlint /workspace/man/workcell.1 >/dev/null
  '
