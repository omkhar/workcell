#!/usr/bin/env -S BASH_ENV= ENV= bash
# shellcheck shell=bash
# Shared /etc/passwd synthesis for validator-container lanes.
#
# Every lane runs the workload as the caller's uid to keep the bind-mounted
# workspace writable, but that uid has no /etc/passwd entry, so glibc
# getpwuid() fails and anything resolving the invoking user dies with "No user
# exists for uid <n>".  ssh-keygen is one of those, and git shells out to it for
# both `gpg.format = ssh` signing and verification, so the pre-push hook tests
# cannot sign or verify a commit.  Give the container a passwd file carrying an
# entry for the runtime uid, appended only when the image lacks one so an
# existing uid keeps its own home.  The home field matches the HOME the caller
# exports, so identity- and env-based home discovery agree on one path.
#
# The file is created under the workspace because that bind is preflighted by
# the caller.  A host temporary directory is not always visible to the daemon:
# on the documented macOS Colima path the daemon runs in a VM that does not
# mount ${TMPDIR}, so the bind source would be missing.
#
# Usage:
#   passwd_file="$(workcell_ci_validator_passwd_file \
#     DOCKER IMAGE UID GID HOME WORKSPACE)"
# DOCKER is the docker invocation to read the image's own /etc/passwd with:
# the context-aware workcell_ci_docker wrapper in the CI lanes, plain docker in
# scripts/build-and-test.sh.  The caller mounts the result read-only at
# /etc/passwd and removes it when the run is done.
workcell_ci_validator_passwd_file() {
  local docker_command="$1"
  local image="$2"
  local uid="$3"
  local gid="$4"
  local home="$5"
  local workspace="$6"
  local file=""

  mkdir -p "${workspace}/tmp" || return
  file="$(mktemp "${workspace}/tmp/workcell-validator-passwd.XXXXXX")" || return
  "${docker_command}" run --rm --entrypoint /bin/bash "${image}" \
    -lc 'cat /etc/passwd' >"${file}" || return
  if ! awk -F: -v uid="${uid}" '$3 == uid { found = 1 } END { exit !found }' \
    "${file}"; then
    printf 'workcell-ci:x:%s:%s:workcell ci:%s:/bin/bash\n' \
      "${uid}" "${gid}" "${home}" >>"${file}"
  fi
  chmod 0444 "${file}" || return
  printf '%s\n' "${file}"
}
