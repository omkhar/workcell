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
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/check-flag-inventory.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
# shellcheck source=scripts/lib/trusted-entrypoint.sh
# shellcheck disable=SC2312 # repo-wide bootstrap; the path is the running script's own directory
source "${workcell_entry_root}/scripts/lib/trusted-entrypoint.sh"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "${workcell_entry_root}/scripts/check-flag-inventory.sh" >/dev/null
  echo "check-flag-inventory-entrypoint-ok"
  exit 0
fi

ROOT_DIR="${workcell_entry_root}"
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
  if [[ "${write}" -eq 1 ]]; then
    # The Go writer refuses symlinked parents, so the write stays in the checkout.
    citools write "${ROOT_DIR}" "${id}" "${help_path}"
    continue
  fi
  # The Go side reads the fixture without following a symlink.
  if ! citools compare "${ROOT_DIR}" "${id}" "${help_path}"; then
    echo "Flag inventory: ${fixture} is not a regular file or does not match the ${binary} CLI in ${IMAGE_TAG}. Run scripts/check-flag-inventory.sh --write, then classify each new flag in adapters/${id}/adapter.toml [flags]." >&2
    status=1
  fi
done <<<"${plan}"
citools check "${ROOT_DIR}"
exit "${status}"
