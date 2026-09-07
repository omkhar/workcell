#!/usr/bin/env -S BASH_ENV= ENV= bash
# Run the Go fuzz targets inside the validator image, which already ships the
# pinned Go toolchain, so the scheduled fuzz lane exercises the reviewed
# toolchain rather than the runner's ambient Go. Mirrors
# run-mutation-in-validator.sh. A crash writes its reproducer under the
# bind-mounted workspace (internal/<pkg>/testdata/fuzz/<Target>/<hash>), so it
# survives on the host for the workflow's failure-only artifact upload.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
source "${ROOT_DIR}/scripts/ci/lib/local-docker-parity.sh"
source "${ROOT_DIR}/scripts/ci/lib/validator-passwd.sh"
VALIDATOR_IMAGE="${WORKCELL_VALIDATOR_IMAGE:-}"
WORKSPACE="${WORKCELL_VALIDATOR_WORKSPACE:-${ROOT_DIR}}"
FUZZTIME="${WORKCELL_FUZZTIME:-3m}"

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
  -e HOME="${validator_home}" \
  -e XDG_CACHE_HOME="${validator_cache}" \
  -e GOCACHE="${validator_cache}/go-build" \
  -e GOMODCACHE="${validator_cache}/go-mod" \
  -e TMPDIR="${validator_tmp}" \
  -e WORKCELL_FUZZTIME="${FUZZTIME}" \
  --mount "${validator_workspace_mount}" \
  --mount "${validator_passwd_mount}" \
  -w /workspace \
  "${VALIDATOR_IMAGE}" \
  -lc '
    set -euo pipefail
    mkdir -p "${HOME}" "${XDG_CACHE_HOME}" "${GOCACHE}" "${GOMODCACHE}" "${TMPDIR}"
    go mod download
    # Each target runs on its own seed corpus, mutated for WORKCELL_FUZZTIME.
    # Names are anchored because FuzzParse is a prefix of two other targets and
    # -fuzz requires exactly one match.
    targets=(
      "./internal/metadatautil/ FuzzExtractWorkflowUses"
      "./internal/metadatautil/ FuzzParseToolPins"
      "./internal/metadatautil/ FuzzValidateControlPlaneManifest"
      "./internal/tomlsubset/ FuzzParse"
      "./internal/injection/ FuzzIsAllowedSystemSymlink"
      "./internal/injection/ FuzzParseSSHDirective"
    )
    for entry in "${targets[@]}"; do
      pkg="${entry%% *}"
      name="${entry##* }"
      echo "[fuzz] ${name} (${WORKCELL_FUZZTIME})"
      go test "${pkg}" -run "^$" -fuzz="^${name}$" -fuzztime="${WORKCELL_FUZZTIME}"
    done
  '
