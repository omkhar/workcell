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
  local directory=""
  local file=""

  # Containment is decided on canonical paths.  A symlinked `tmp`, or a
  # symlinked ancestor of it, would place the artifact, its mode change and its
  # removal outside the workspace the caller preflighted, while `mkdir -p` and
  # `mktemp` both follow it without complaint.  Bash cannot open and hold a
  # parent descriptor, so the gate compares the resolved directory with the
  # resolved workspace and refuses anything else.
  workspace="$(cd "${workspace}" && pwd -P)" || return
  directory="${workspace}/tmp"
  mkdir -p "${directory}" || return
  if [[ "$(cd "${directory}" && pwd -P)" != "${directory}" ]]; then
    echo "Validator passwd directory is not the canonical ${directory}" >&2
    return 1
  fi
  # The record is colon-delimited and newline-terminated, so a home carrying
  # either character would truncate the home field and shift every field after
  # it.  Both are legal in a Unix path, and the dev-side home comes from
  # ${TMPDIR}, so the field is checked rather than assumed.
  if [[ "${home}" == *:* || "${home}" == *$'\n'* ]]; then
    echo "Validator home is not a usable passwd field: ${home}" >&2
    return 1
  fi
  # Every failure after this point removes the file itself.  The caller only
  # learns the pathname from the value this function prints, so a failure that
  # returned early would leave an artifact its EXIT trap has no name for.
  file="$(mktemp "${directory}/workcell-validator-passwd.XXXXXX")" || return
  "${docker_command}" run --rm --entrypoint /bin/bash "${image}" \
    -lc 'cat /etc/passwd' >"${file}" || {
    rm -f "${file}"
    return 1
  }
  # "absent" gets its own exit status.  Folding every nonzero status into
  # "absent" would let a missing or failing awk append a second record for a uid
  # the image already has, and glibc would then resolve the uid to whichever
  # record comes first.  An error here fails the lane instead.
  local lookup=0
  awk -F: -v uid="${uid}" '$3 == uid { found = 1 } END { exit found ? 0 : 10 }' \
    "${file}" || lookup=$?
  case "${lookup}" in
    0) ;;
    10)
      printf 'workcell-ci:x:%s:%s:workcell ci:%s:/bin/bash\n' \
        "${uid}" "${gid}" "${home}" >>"${file}" || {
        rm -f "${file}"
        return 1
      }
      ;;
    *)
      echo "Validator passwd lookup failed with status ${lookup}" >&2
      rm -f "${file}"
      return 1
      ;;
  esac
  # Owner-only whenever the workload runs as the uid that creates the file,
  # which is every lane except the remapped-uid axis.  There, rootful Docker
  # keeps the numeric owner across the bind, so an owner-only file would be
  # unreadable to the workload and the axis would fail on its own harness
  # rather than on the repository.  Widening to world-readable for that case
  # discloses nothing: the content is a copy of the image's own /etc/passwd
  # plus one record naming a uid, gid and home that the container already
  # reports.  mktemp created the file 0600, so both modes also drop the write
  # bit the read-only mount does not need.
  if [[ "${uid}" == "$(id -u)" ]]; then
    chmod 0400 "${file}" || {
      rm -f "${file}"
      return 1
    }
  else
    chmod 0444 "${file}" || {
      rm -f "${file}"
      return 1
    }
  fi
  printf '%s\n' "${file}"
}
