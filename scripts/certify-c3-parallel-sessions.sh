#!/bin/bash -p
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 Omkhar Arasaratnam
#
# Thin host entrypoint for the Go-owned C3 strict-path certifier.
set -euo pipefail
set +C +f +v +x
shopt -u failglob nullglob nocaseglob nocasematch
unset CDPATH GLOBIGNORE

readonly TRUSTED_HOST_PATH="/opt/homebrew/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
[[ "${HOME:-}" == /* ]] || {
  echo "certify-c3: HOME must be an absolute path" >&2
  exit 2
}
ROOT_DIR="$(CDPATH='' cd -P -- "${BASH_SOURCE[0]%/*}/.." && pwd -P)"

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
