# shellcheck shell=bash
# Common sanitized-entrypoint preamble for host-side scripts.
# Source this file immediately after the shebang line of scripts that require
# a trusted PATH environment and clean process environment.
# Callers must use a #!/bin/bash -p shebang.
#
# On the env -i re-exec this preamble forwards:
#   - PATH                       set to TRUSTED_HOST_PATH
#   - HOME, TMPDIR               with safe fallbacks
#   - WORKCELL_*                 every var in the project's namespace, so a
#                                caller can pass tunables like
#                                WORKCELL_KEEP_VALIDATOR_IMAGE without each
#                                script enumerating its own forwarded set
#   - WORKCELL_SANITIZED_ENTRYPOINT=1   sentinel that breaks the re-exec loop
#
# After re-exec the preamble exports PATH=TRUSTED_HOST_PATH and defines
# require_tool().  Scripts that need additional path entries (for example
# ${HOME}/.cargo/bin) can override PATH after sourcing this file.
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/local/go/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  workcell_trusted_exec_args=(
    /usr/bin/env -i
    "PATH=${TRUSTED_HOST_PATH}"
    "HOME=${HOME:-/tmp}"
    "TMPDIR=${TMPDIR:-/tmp}"
    "WORKCELL_SANITIZED_ENTRYPOINT=1"
  )
  while IFS= read -r workcell_trusted_env_line; do
    workcell_trusted_env_name="${workcell_trusted_env_line%%=*}"
    case "${workcell_trusted_env_name}" in
      WORKCELL_*) ;;
      *) continue ;;
    esac
    if [[ "${workcell_trusted_env_name}" == "WORKCELL_SANITIZED_ENTRYPOINT" ]]; then
      continue
    fi
    workcell_trusted_exec_args+=("${workcell_trusted_env_line}")
  done < <(/usr/bin/env)
  workcell_trusted_exec_args+=(/bin/bash -p "$0" "$@")
  exec "${workcell_trusted_exec_args[@]}"
fi
set -euo pipefail
export PATH="${TRUSTED_HOST_PATH}"

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}
