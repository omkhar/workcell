#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=tests/scenarios/shared/test-copilot-session-dry-run.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-copilot-session-dry-run.XXXXXX")"
WORKCELL_FUNCTIONS_COPY="${TMP_DIR}/workcell-copilot-session-functions"

cleanup() {
  chmod -R u+w "${TMP_DIR}" 2>/dev/null || true
  rm -f "${WORKCELL_FUNCTIONS_COPY}"
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

HOME_DIR="${TMP_DIR}/home"
AUTH_ROOT="${TMP_DIR}/auth"
WORKSPACE="${TMP_DIR}/workspace"
mkdir -p "${HOME_DIR}/.config" "${AUTH_ROOT}" "${WORKSPACE}"
git -C "${WORKSPACE}" init -q
printf 'copilot detached session dry-run workspace\n' >"${WORKSPACE}/README.md"
printf 'copilot-token-fixture\n' >"${AUTH_ROOT}/copilot-github-token.txt"
chmod 0600 "${AUTH_ROOT}/copilot-github-token.txt"

cat >"${AUTH_ROOT}/policy.toml" <<'EOF'
version = 1

[credentials.copilot_github_token]
source = "copilot-github-token.txt"
EOF

HOME="${HOME_DIR}" XDG_CONFIG_HOME="${HOME_DIR}/.config" \
  WORKCELL_VERIFY_INVARIANTS_SANITIZED_ENTRYPOINT=1 \
  WORKCELL_TEST_SUPPORT_MATRIX_HOST_OS=macos \
  WORKCELL_TEST_SUPPORT_MATRIX_HOST_ARCH=arm64 \
  WORKCELL_TEST_SUPPORT_MATRIX_HOST_DISTRO=none \
  WORKCELL_TEST_SUPPORT_MATRIX_HOST_DISTRO_VERSION=none \
  /bin/bash -p "${ROOT_DIR}/scripts/workcell" \
  session start \
  --agent copilot \
  --workspace "${WORKSPACE}" \
  --session-workspace direct \
  --injection-policy "${AUTH_ROOT}/policy.toml" \
  --dry-run \
  >"${TMP_DIR}/session.stdout" 2>"${TMP_DIR}/session.stderr"

grep -q '^profile=.* mode=strict agent=copilot ' "${TMP_DIR}/session.stderr"
grep -q '^target_kind=local_vm target_provider=colima ' "${TMP_DIR}/session.stderr"
grep -q '^execution_path=managed-tier1 audit_log=' "${TMP_DIR}/session.stderr"
grep -q '^session_assurance_initial=managed-mutable$' "${TMP_DIR}/session.stderr"
grep -Fq -- "docker run -d -i -t" "${TMP_DIR}/session.stdout"
if grep -Fq -- "docker run --init -d -i -t" "${TMP_DIR}/session.stdout"; then
  echo "Copilot detached session kept Docker's init ahead of the managed session supervisor" >&2
  exit 1
fi
grep -Fq -- "-e WORKCELL_DETACHED_STDIN_PATH=/state/tmp/workcell/session-stdin" "${TMP_DIR}/session.stdout"
grep -Fq -- "workcell:local" "${TMP_DIR}/session.stdout"

if grep -Fq -- "copilot-token-fixture" "${TMP_DIR}/session.stdout" "${TMP_DIR}/session.stderr"; then
  echo "Copilot detached dry-run leaked token material" >&2
  exit 1
fi
if grep -Fq -- "--env-file" "${TMP_DIR}/session.stdout"; then
  echo "Copilot detached dry-run should not create or print a Docker env-file" >&2
  exit 1
fi
if grep -Fq -- ":/opt/workcell/copilot-token-handoff:rw" "${TMP_DIR}/session.stdout"; then
  echo "Copilot detached dry-run should not create or print a token handoff mount" >&2
  exit 1
fi
if grep -Fq -- "copilot-github-token.txt" "${TMP_DIR}/session.stdout"; then
  echo "Copilot detached dry-run should not mount the staged token file" >&2
  exit 1
fi

sed '/^if \[\[ \$# -gt 0 \]\]; then$/,$d' "${ROOT_DIR}/scripts/workcell" >"${WORKCELL_FUNCTIONS_COPY}"
# The copy is not scripts/workcell, so its entry guard would refuse it: give
# it the tree root that this script already uses instead.
sed -i.bak -e "s|^workcell_entry_root=.*|workcell_entry_root=\"${ROOT_DIR}\"|" -e "s|^ROOT_DIR=.*|ROOT_DIR=\"${ROOT_DIR}\"|" "${WORKCELL_FUNCTIONS_COPY}"
rm -f "${WORKCELL_FUNCTIONS_COPY}.bak"
bash -lc '
  set -euo pipefail
  source "$1"
  trap - EXIT
  unset XDG_CACHE_HOME

  fixture_root="$2"
  real_home="${fixture_root}/handoff-home"
  bundle_root="${fixture_root}/handoff-bundle"
  token_source="${bundle_root}/direct-mounts/credentials/copilot-github-token.txt"
  fake_docker="${fixture_root}/fake-docker"
  expected_handoff_parent=""
  mkdir -p "$(dirname "${token_source}")" "$(dirname "${fake_docker}")"
  printf "%s\n" "copilot-session-handoff-token" >"${token_source}"
  chmod 0600 "${token_source}"
  cat >"${fake_docker}" <<'"'"'EOF_DOCKER'"'"'
#!/bin/sh
if [ "$1" = "inspect" ]; then
  printf "%s\n" "running"
  exit 0
fi
exit 0
EOF_DOCKER
  chmod 0700 "${fake_docker}"

  AGENT=copilot
  PREPARE_ONLY=0
  ALLOW_ARBITRARY_COMMAND=0
  MODE=strict
  DRY_RUN=0
  REAL_HOME="${real_home}"
  INJECTION_BUNDLE_ROOT="${bundle_root}"
  INJECTION_CREDENTIAL_KEYS=copilot_github_token
  INJECTION_CREDENTIAL_RESOLUTION_STATES=copilot_github_token:source
  DIRECT_SOURCE_MOUNTS=(-v "${token_source}:/opt/workcell/host-inputs/credentials/copilot-github-token.txt:ro")

  prepare_copilot_token_handoff_mount copilot -p smoke
  expected_handoff_parent="$(resolve_host_path "$(colima_staging_cache_root)/workcell-token-handoff")"
  if [[ -e "${token_source}" ]]; then
    echo "Copilot handoff did not remove the staged direct-mount token copy" >&2
    exit 1
  fi
  if [[ "${#DIRECT_SOURCE_MOUNTS[@]}" -ne 0 ]]; then
    echo "Copilot handoff did not remove the staged direct token mount from docker args" >&2
    exit 1
  fi
  if [[ "${COPILOT_TOKEN_HANDOFF_DIR}" != "${expected_handoff_parent}"/copilot-token-handoff.* ]]; then
    echo "Copilot handoff directory was not created under the dedicated token-handoff parent" >&2
    exit 1
  fi
  if [[ ! -f "${COPILOT_TOKEN_HANDOFF_FILE}" ]]; then
    echo "Copilot handoff did not create the expected host handoff token file" >&2
    exit 1
  fi
  if [[ -e "${COPILOT_TOKEN_HANDOFF_CONSUMED_FILE}" ]]; then
    echo "Copilot handoff consumed marker existed before runtime consumption" >&2
    exit 1
  fi
  if [[ "$(cat "${COPILOT_TOKEN_HANDOFF_FILE}")" != "copilot-session-handoff-token" ]]; then
    echo "Copilot handoff token file content did not match the staged source" >&2
    exit 1
  fi

  HOST_DOCKER_BIN="${fake_docker}"
  CONTAINER_NAME=workcell-copilot-session-handoff-test
  WORKCELL_COPILOT_TOKEN_HANDOFF_TIMEOUT_SECONDS=3
  ( sleep 0.2; rm -f -- "${COPILOT_TOKEN_HANDOFF_FILE}"; : >"${COPILOT_TOKEN_HANDOFF_CONSUMED_FILE}" ) &
  wait_for_copilot_token_handoff_consumed
  wait
  if [[ -e "${COPILOT_TOKEN_HANDOFF_FILE}" ]]; then
    echo "Copilot handoff wait returned before the runtime consumed the token file" >&2
    exit 1
  fi
  if [[ ! -e "${COPILOT_TOKEN_HANDOFF_CONSUMED_FILE}" ]]; then
    echo "Copilot handoff wait returned without the runtime consumed marker" >&2
    exit 1
  fi

  rm -rf -- "${COPILOT_TOKEN_HANDOFF_DIR}"
  COPILOT_TOKEN_HANDOFF_DIR=""
  COPILOT_TOKEN_HANDOFF_FILE=""
  COPILOT_TOKEN_HANDOFF_CONSUMED_FILE=""

  printf "%s\n" "duplicate-token" >"${token_source}"
  chmod 0600 "${token_source}"
  duplicate_source="${bundle_root}/direct-mounts/credentials/copilot-github-token-copy.txt"
  printf "%s\n" "duplicate-token" >"${duplicate_source}"
  chmod 0600 "${duplicate_source}"
  DIRECT_SOURCE_MOUNTS=(
    -v "${token_source}:/opt/workcell/host-inputs/credentials/copilot-github-token.txt:ro"
    -v "${duplicate_source}:/opt/workcell/host-inputs/credentials/copilot-github-token.txt:ro"
  )
  if ( prepare_copilot_token_handoff_mount copilot -p smoke ) >"${fixture_root}/duplicate.stdout" 2>"${fixture_root}/duplicate.stderr"; then
    echo "Copilot handoff accepted duplicate staged token mounts" >&2
    exit 1
  fi
  grep -q "expected exactly one staged copilot_github_token input, found 2" "${fixture_root}/duplicate.stderr"

  outside_token="${fixture_root}/outside-copilot-token.txt"
  printf "%s\n" "outside-token" >"${outside_token}"
  chmod 0600 "${outside_token}"
  DIRECT_SOURCE_MOUNTS=(-v "${outside_token}:/opt/workcell/host-inputs/credentials/copilot-github-token.txt:ro")
  if ( prepare_copilot_token_handoff_mount copilot -p smoke ) >"${fixture_root}/outside.stdout" 2>"${fixture_root}/outside.stderr"; then
    echo "Copilot handoff accepted a token source outside the injection bundle" >&2
    exit 1
  fi
  grep -q "expected the staged token copy under the injection bundle direct-mounts directory" "${fixture_root}/outside.stderr"

  : >"${token_source}"
  chmod 0600 "${token_source}"
  DIRECT_SOURCE_MOUNTS=(-v "${token_source}:/opt/workcell/host-inputs/credentials/copilot-github-token.txt:ro")
  if ( prepare_copilot_token_handoff_mount copilot -p smoke ) >"${fixture_root}/empty.stdout" 2>"${fixture_root}/empty.stderr"; then
    echo "Copilot handoff accepted an empty staged token" >&2
    exit 1
  fi
  grep -q "Copilot auth token is empty" "${fixture_root}/empty.stderr"

  COPILOT_TOKEN_HANDOFF_DIR="${fixture_root}/timeout-handoff"
  COPILOT_TOKEN_HANDOFF_FILE="${COPILOT_TOKEN_HANDOFF_DIR}/copilot-github-token.txt"
  COPILOT_TOKEN_HANDOFF_CONSUMED_FILE="${COPILOT_TOKEN_HANDOFF_DIR}/copilot-token-consumed"
  mkdir -p "${COPILOT_TOKEN_HANDOFF_DIR}"
  printf "%s\n" "timeout-token" >"${COPILOT_TOKEN_HANDOFF_FILE}"
  HOST_DOCKER_BIN="${fake_docker}"
  CONTAINER_NAME=workcell-copilot-session-timeout-test
  WORKCELL_COPILOT_TOKEN_HANDOFF_TIMEOUT_SECONDS=1
  if wait_for_copilot_token_handoff_consumed >"${fixture_root}/timeout.stdout" 2>"${fixture_root}/timeout.stderr"; then
    echo "Copilot handoff wait succeeded without a consumed marker" >&2
    exit 1
  fi
  grep -q "Timed out waiting for the managed Copilot wrapper" "${fixture_root}/timeout.stderr"

  cat >"${fixture_root}/fake-docker-stopped" <<'"'"'EOF_DOCKER_STOPPED'"'"'
#!/bin/sh
if [ "$1" = "inspect" ]; then
  printf "%s\n" "exited"
  exit 0
fi
exit 0
EOF_DOCKER_STOPPED
  chmod 0700 "${fixture_root}/fake-docker-stopped"
  HOST_DOCKER_BIN="${fixture_root}/fake-docker-stopped"
  CONTAINER_NAME=workcell-copilot-session-stopped-test
  if wait_for_copilot_token_handoff_consumed >"${fixture_root}/stopped.stdout" 2>"${fixture_root}/stopped.stderr"; then
    echo "Copilot handoff wait succeeded after the container stopped" >&2
    exit 1
  fi
  grep -q "was not consumed before the runtime container stopped" "${fixture_root}/stopped.stderr"
  cleanup_handoff_dir="${COPILOT_TOKEN_HANDOFF_DIR}"
  cleanup 0
  if [[ -e "${cleanup_handoff_dir}" ]]; then
    echo "Copilot handoff cleanup left the host handoff directory behind" >&2
    exit 1
  fi
' _ "${WORKCELL_FUNCTIONS_COPY}" "${TMP_DIR}"

if grep -R -Fq -- "copilot-session-handoff-token" "${TMP_DIR}"; then
  echo "Copilot detached token handoff test left token material in scenario temp files" >&2
  exit 1
fi

echo "Copilot detached session dry-run and handoff scenario passed"
