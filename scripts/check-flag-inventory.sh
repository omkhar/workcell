#!/bin/bash -p
# Check the option inventory of each certified adapter CLI in the runtime image.
#
# Usage: check-flag-inventory.sh [--write]
#
# For each certified adapter, the script runs `<binary> --help` and
# `<binary> <subcommand> --help` for each [flags] subcommands entry in
# adapters/<id>/adapter.toml. It runs them in WORKCELL_IMAGE_TAG (default
# workcell:smoke) as the image's agent user with --network none. It fails when
# tests/fixtures/flags/<id>.txt differs from the rendered inventory, or when a
# fixture flag is in neither [flags] allow nor deny. --write updates the
# fixtures instead of comparing them. Container smoke runs it after the build.
# shellcheck source=scripts/lib/trusted-entrypoint.sh
# shellcheck disable=SC2312 # repo-wide bootstrap; the path is the running script's own directory
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/trusted-entrypoint.sh"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "check-flag-inventory-entrypoint-ok"
  exit 0
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE_TAG="${WORKCELL_IMAGE_TAG:-workcell:smoke}"
DOCKER_CONTEXT_NAME="${WORKCELL_CONTAINER_SMOKE_DOCKER_CONTEXT:-}"
GO_BIN="${WORKCELL_GO_BIN:-go}"
write=0
case "${1:-}" in
  "") ;;
  --write) write=1 ;;
  *)
    echo "Usage: $0 [--write]" >&2
    exit 2
    ;;
esac

require_tool docker
require_tool "${GO_BIN}"

docker_cmd() {
  if [[ -n "${DOCKER_CONTEXT_NAME}" ]]; then
    docker --context "${DOCKER_CONTEXT_NAME}" "$@"
  else
    docker "$@"
  fi
}

citools() {
  (cd "${ROOT_DIR}" && "${GO_BIN}" run ./cmd/workcell-citools flag-inventory "$@")
}

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/workcell-flag-inventory.XXXXXX")"
trap 'rm -rf "${work_dir}"' EXIT
cat >"${work_dir}/probe.sh" <<'EOF'
binary="$1"
shift
"${binary}" --help
for subcommand in "$@"; do
  "${binary}" "${subcommand}" --help
done
EOF
plan="$(citools plan "${ROOT_DIR}")"
status=0
# Each plan line is: id binary [subcommand...].
while read -r -a fields; do
  id="${fields[0]}"
  binary="${fields[1]}"
  help_path="${work_dir}/${id}.help"
  # The Copilot wrapper needs a token handoff for every subcommand but help;
  # the probe never authenticates, so it opts out of the handoff.
  if ! docker_cmd run --rm -i --network none \
    -e WORKCELL_COPILOT_AUTH_REQUIRED=0 \
    --entrypoint /bin/bash "${IMAGE_TAG}" \
    -eus -- "${fields[@]:1}" <"${work_dir}/probe.sh" >"${help_path}" 2>&1; then
    echo "Flag inventory: ${binary} --help failed in ${IMAGE_TAG}:" >&2
    cat "${help_path}" >&2
    exit 1
  fi
  fixture="${ROOT_DIR}/tests/fixtures/flags/${id}.txt"
  citools render "${ROOT_DIR}" "${id}" "${help_path}" >"${work_dir}/${id}.txt"
  if [[ "${write}" -eq 1 ]]; then
    mkdir -p "$(dirname "${fixture}")"
    staged="$(mktemp "${fixture}.XXXXXX")"
    cp "${work_dir}/${id}.txt" "${staged}"
    chmod 644 "${staged}"
    mv -f -- "${staged}" "${fixture}"
  elif [[ -L "${ROOT_DIR}/tests" || -L "${ROOT_DIR}/tests/fixtures" || -L "${fixture%/*}" || -L "${fixture}" || ! -f "${fixture}" ]]; then
    # diff follows a symlink and would print its target into the CI log.
    echo "Flag inventory: ${fixture} is not a regular file under real directories; refusing to diff a missing, special or symlinked fixture. Run scripts/check-flag-inventory.sh --write." >&2
    status=1
  elif ! diff -u "${fixture}" "${work_dir}/${id}.txt" >&2; then
    echo "Flag inventory: ${fixture} does not match the ${binary} CLI in ${IMAGE_TAG}. Run scripts/check-flag-inventory.sh --write, then classify each new flag in adapters/${id}/adapter.toml [flags]." >&2
    status=1
  fi
done <<<"${plan}"
citools check "${ROOT_DIR}"
exit "${status}"
