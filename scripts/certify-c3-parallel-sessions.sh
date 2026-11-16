#!/bin/bash -p
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 Omkhar Arasaratnam
#
# Thin host entrypoint for the Go-owned C3 strict-path certifier.
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/certify-c3-parallel-sessions.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail
set +C +f +v +x
shopt -u failglob nullglob nocaseglob nocasematch
unset CDPATH GLOBIGNORE

readonly TRUSTED_HOST_PATH="/opt/homebrew/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
[[ "${HOME:-}" == /* ]] || {
  echo "certify-c3: HOME must be an absolute path" >&2
  exit 2
}
ROOT_DIR="${workcell_entry_root}"

# shellcheck disable=SC2016
exec /usr/bin/env -i \
  PATH="${TRUSTED_HOST_PATH}" \
  HOME="${HOME}" \
  TMPDIR="${TMPDIR:-/tmp}" \
  LC_ALL=C \
  LANG=C \
  GOENV=off \
  GOWORK=off \
  GOTOOLCHAIN=local \
  GOFLAGS=-mod=readonly \
  /bin/bash -p -c '
    set -euo pipefail
    readonly ROOT_DIR="$1"
    shift
    # shellcheck source=/dev/null
    source "${ROOT_DIR}/scripts/lib/go-run-env.sh"
    if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
      [[ "$#" -eq 1 ]] || exit 2
      go_bin="$(resolve_go_bin)"
      exec "${go_bin}" env GOENV GOWORK GOFLAGS GOTOOLCHAIN
    fi
    usage() {
      echo "Usage: certify-c3-parallel-sessions.sh --workspace PATH [--precommit-control-tree SHA]"
    }
    arguments=("$@")
    workspace_seen=0
    while (($# > 0)); do
      case "$1" in
        -h | --help)
          usage
          exit 0
          ;;
        --workspace)
          [[ "$#" -ge 2 && -n "$2" ]] || {
            usage >&2
            exit 2
          }
          workspace_seen=1
          shift 2
          ;;
        --workspace=*)
          [[ -n "${1#*=}" ]] || {
            usage >&2
            exit 2
          }
          workspace_seen=1
          shift
          ;;
        --precommit-control-tree | --root)
          [[ "$#" -ge 2 && -n "$2" ]] || {
            usage >&2
            exit 2
          }
          shift 2
          ;;
        --precommit-control-tree=* | --root=*)
          [[ -n "${1#*=}" ]] || {
            usage >&2
            exit 2
          }
          shift
          ;;
        *)
          echo "certify-c3: unknown argument: $1" >&2
          usage >&2
          exit 2
          ;;
      esac
    done
    ((workspace_seen == 1)) || {
      usage >&2
      exit 2
    }
    set -- "${arguments[@]}"
    exec_go_run_in_repo "${ROOT_DIR}" ./cmd/workcell-c3-certify "$@" --root "${ROOT_DIR}"
  ' workcell-c3-sanitized "${ROOT_DIR}" "$@"
