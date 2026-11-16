#!/bin/bash -p
# Stage the go.mod toolchain at a root-owned /usr/local/go on a GitHub-hosted
# runner. The runner image makes /opt, and with it the tool cache that its
# /usr/bin/go points into, world-writable, so resolve_exact_toolchain_go_bin in
# scripts/lib/go-run-env.sh refuses every go there. The checksum-verified
# GOTOOLCHAIN switch fetches the exact toolchain; the copy is then owned by
# root and writable by no one else. The runner VM is single-use, so the go
# that drives the switch is trusted for this one fetch only.
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/ci/stage-trusted-go.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"
target=/usr/local/go
toolchain="$(/usr/bin/awk '$1 == "toolchain" { print $2; exit }' "${ROOT_DIR}/go.mod")"
[[ "${toolchain}" =~ ^go[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
  echo "go.mod must declare one exact Go toolchain." >&2
  exit 1
}
if [[ -e "${target}" ]]; then
  echo "${target} already exists; refusing to replace it." >&2
  exit 1
fi
# require_toolchain GO ends the script unless GO runs and reports the
# go.mod toolchain.
require_toolchain() {
  local version=""
  if ! version="$(GOTOOLCHAIN=local "$1" env GOVERSION)"; then
    exit 1
  fi
  if [[ "${version}" != "${toolchain}" ]]; then
    echo "The toolchain at $1 is not ${toolchain}." >&2
    exit 1
  fi
}
if ! goroot="$(GOTOOLCHAIN="${toolchain}" go env GOROOT)"; then
  exit 1
fi
require_toolchain "${goroot}/bin/go"
# Build and check the tree in a directory beside the target that only root
# can enter, then rename it into place, so a partial copy is never found at
# the target. cp -a keeps the runner's ownership, so the directory opens to
# the runner (mktemp makes it 0700) only after the tree is root's and
# read-only, for the version check.
stage="$(sudo mktemp -d "${target%/*}/.go-stage.XXXXXX")"
sudo cp -a "${goroot}" "${stage}/go"
sudo chown -R root:root "${stage}/go"
sudo chmod -R go-w "${stage}/go"
sudo chmod 0755 "${stage}"
require_toolchain "${stage}/go/bin/go"
sudo sync
sudo mv -T "${stage}/go" "${target}"
sudo rmdir "${stage}"
sudo sync
require_toolchain "${target}/bin/go"
echo "Staged ${toolchain} at ${target}."
