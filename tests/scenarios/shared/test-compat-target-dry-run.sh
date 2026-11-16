#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=tests/scenarios/shared/test-compat-target-dry-run.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-compat-target-scenario.XXXXXX")"
cleanup() {
  chmod -R u+w "${TMP_DIR}" 2>/dev/null || true
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

HOME_DIR="${TMP_DIR}/home"
WORKSPACE="${TMP_DIR}/workspace"
mkdir -p "${HOME_DIR}/.config" "${WORKSPACE}"
git -C "${WORKSPACE}" init -q
printf 'scenario workspace\n' >"${WORKSPACE}/README.md"

resolve_host_docker_optional() {
  local candidate=""
  for candidate in \
    /opt/homebrew/bin/docker \
    /usr/local/bin/docker \
    /Applications/Docker.app/Contents/Resources/bin/docker \
    "$(command -v docker 2>/dev/null || true)"; do
    [[ -n "${candidate}" ]] || continue
    [[ -x "${candidate}" ]] || continue
    printf '%s\n' "${candidate}"
    return 0
  done
  return 1
}

docker_desktop_missing_state() {
  local docker_bin=""
  if ! docker_bin="$(resolve_host_docker_optional)"; then
    printf 'docker\n'
    return 0
  fi
  if "${docker_bin}" context inspect desktop-linux >/dev/null 2>&1 &&
    "${docker_bin}" --context desktop-linux info >/dev/null 2>&1; then
    printf 'none\n'
  else
    printf 'docker-desktop-context\n'
  fi
}

run_with_support_override() {
  local label="$1"
  local host_os="$2"
  local host_arch="$3"
  local host_distro="$4"
  local host_distro_version="$5"
  shift 5
  HOME="${HOME_DIR}" XDG_CONFIG_HOME="${HOME_DIR}/.config" \
    WORKCELL_VERIFY_INVARIANTS_SANITIZED_ENTRYPOINT=1 \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_OS="${host_os}" \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_ARCH="${host_arch}" \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_DISTRO="${host_distro}" \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_DISTRO_VERSION="${host_distro_version}" \
    /bin/bash -p "${ROOT_DIR}/scripts/workcell" \
    "$@" \
    --workspace "${WORKSPACE}" \
    --no-default-injection-policy \
    >"${TMP_DIR}/${label}.stdout" 2>"${TMP_DIR}/${label}.stderr"
}

run_with_support_override_expect_failure() {
  local expected_rc="$1"
  local label="$2"
  local host_os="$3"
  local host_arch="$4"
  local host_distro="$5"
  local host_distro_version="$6"
  shift 6
  set +e
  HOME="${HOME_DIR}" XDG_CONFIG_HOME="${HOME_DIR}/.config" \
    WORKCELL_VERIFY_INVARIANTS_SANITIZED_ENTRYPOINT=1 \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_OS="${host_os}" \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_ARCH="${host_arch}" \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_DISTRO="${host_distro}" \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_DISTRO_VERSION="${host_distro_version}" \
    /bin/bash -p "${ROOT_DIR}/scripts/workcell" \
    "$@" \
    --workspace "${WORKSPACE}" \
    --no-default-injection-policy \
    >"${TMP_DIR}/${label}.stdout" 2>"${TMP_DIR}/${label}.stderr"
  local rc=$?
  set -e
  if [[ "${rc}" -ne "${expected_rc}" ]]; then
    echo "Unexpected exit code for ${label}: ${rc} (expected ${expected_rc})" >&2
    cat "${TMP_DIR}/${label}.stderr" >&2
    exit 1
  fi
}

EXPECTED_ALLOWED_MISSING="$(docker_desktop_missing_state)"

run_with_support_override \
  "compat-doctor-supported" \
  macos \
  arm64 \
  none \
  none \
  --target docker-desktop \
  --agent codex \
  --doctor
grep -q '^target_kind=local_compat$' "${TMP_DIR}/compat-doctor-supported.stdout"
grep -q '^target_provider=docker-desktop$' "${TMP_DIR}/compat-doctor-supported.stdout"
grep -q '^target_id=desktop-linux$' "${TMP_DIR}/compat-doctor-supported.stdout"
grep -q '^target_assurance_class=compat$' "${TMP_DIR}/compat-doctor-supported.stdout"
grep -q '^host_distro=none$' "${TMP_DIR}/compat-doctor-supported.stdout"
grep -q '^host_distro_version=none$' "${TMP_DIR}/compat-doctor-supported.stdout"
grep -q '^support_matrix_status=supported$' "${TMP_DIR}/compat-doctor-supported.stdout"
grep -q '^support_matrix_launch=allowed$' "${TMP_DIR}/compat-doctor-supported.stdout"
grep -q '^support_matrix_reason=apple-silicon-macos-docker-desktop-compat-reviewed-launch-host$' "${TMP_DIR}/compat-doctor-supported.stdout"
grep -q '^profile_dir=none$' "${TMP_DIR}/compat-doctor-supported.stdout"
grep -Eq '^target_state_dir=.*/targets/local_compat/docker-desktop/wcl-workspace-[a-f0-9]+$' "${TMP_DIR}/compat-doctor-supported.stdout"
grep -Eq '^profile_marker=.*/targets/local_compat/docker-desktop/wcl-workspace-[a-f0-9]+/workcell\.managed$' "${TMP_DIR}/compat-doctor-supported.stdout"
grep -Eq '^image_marker=.*/targets/local_compat/docker-desktop/wcl-workspace-[a-f0-9]+/workcell\.image-ready$' "${TMP_DIR}/compat-doctor-supported.stdout"
if [[ "${EXPECTED_ALLOWED_MISSING}" == "none" ]]; then
  DOCTOR_RECOMMENDED_NEXT="$(sed -n 's/^doctor_recommended_next=//p' "${TMP_DIR}/compat-doctor-supported.stdout")"
  grep -q '^doctor_missing_host_tools=none$' "${TMP_DIR}/compat-doctor-supported.stdout"
  [[ "${DOCTOR_RECOMMENDED_NEXT}" == *"workcell"* ]]
  [[ "${DOCTOR_RECOMMENDED_NEXT}" == *"--prepare"* ]]
  [[ "${DOCTOR_RECOMMENDED_NEXT}" == *"--agent codex"* ]]
  [[ "${DOCTOR_RECOMMENDED_NEXT}" == *"--target docker-desktop"* ]]
  [[ "${DOCTOR_RECOMMENDED_NEXT}" == *"--workspace "* ]]
else
  grep -q "^doctor_missing_host_tools=${EXPECTED_ALLOWED_MISSING}\$" "${TMP_DIR}/compat-doctor-supported.stdout"
  grep -q '^doctor_recommended_next=install-host-tools$' "${TMP_DIR}/compat-doctor-supported.stdout"
fi

run_with_support_override \
  "compat-dry-run-supported" \
  macos \
  arm64 \
  none \
  none \
  --target docker-desktop \
  --agent codex \
  --dry-run
grep -q '^target_kind=local_compat target_provider=docker-desktop target_id=desktop-linux target_assurance_class=compat runtime_api=docker workspace_transport=workspace-mount$' "${TMP_DIR}/compat-dry-run-supported.stderr"
grep -Eq '^execution_path=managed-tier1 audit_log=.*/targets/local_compat/docker-desktop/wcl-workspace-[a-f0-9]+/workcell\.audit\.log$' "${TMP_DIR}/compat-dry-run-supported.stderr"

run_with_support_override \
  "compat-dry-run-supported-copilot" \
  macos \
  arm64 \
  none \
  none \
  --target docker-desktop \
  --agent copilot \
  --dry-run
grep -q '^target_kind=local_compat target_provider=docker-desktop target_id=desktop-linux target_assurance_class=compat runtime_api=docker workspace_transport=workspace-mount$' "${TMP_DIR}/compat-dry-run-supported-copilot.stderr"
grep -q '^profile=.* mode=strict agent=copilot ' "${TMP_DIR}/compat-dry-run-supported-copilot.stderr"
grep -Eq '^execution_path=managed-tier1 audit_log=.*/targets/local_compat/docker-desktop/wcl-workspace-[a-f0-9]+/workcell\.audit\.log$' "${TMP_DIR}/compat-dry-run-supported-copilot.stderr"

if [[ "${EXPECTED_ALLOWED_MISSING}" == "none" ]]; then
  PATH="/usr/bin:/bin:/usr/sbin:/sbin" run_with_support_override \
    "compat-dry-run-supported-no-path-docker" \
    macos \
    arm64 \
    none \
    none \
    --target docker-desktop \
    --agent codex \
    --dry-run
  grep -q '^target_kind=local_compat target_provider=docker-desktop target_id=desktop-linux target_assurance_class=compat runtime_api=docker workspace_transport=workspace-mount$' "${TMP_DIR}/compat-dry-run-supported-no-path-docker.stderr"
  grep -Eq '^execution_path=managed-tier1 audit_log=.*/targets/local_compat/docker-desktop/wcl-workspace-[a-f0-9]+/workcell\.audit\.log$' "${TMP_DIR}/compat-dry-run-supported-no-path-docker.stderr"
fi

run_with_support_override \
  "compat-doctor-blocked" \
  linux \
  arm64 \
  debian \
  13 \
  --target docker-desktop \
  --agent codex \
  --doctor
grep -q '^support_matrix_status=unsupported$' "${TMP_DIR}/compat-doctor-blocked.stdout"
grep -q '^support_matrix_launch=blocked$' "${TMP_DIR}/compat-doctor-blocked.stdout"
grep -q '^host_distro=debian$' "${TMP_DIR}/compat-doctor-blocked.stdout"
grep -q '^host_distro_version=13$' "${TMP_DIR}/compat-doctor-blocked.stdout"
grep -q '^doctor_recommended_next=use-supported-host$' "${TMP_DIR}/compat-doctor-blocked.stdout"

run_with_support_override_expect_failure \
  2 \
  "compat-dry-run-blocked" \
  linux \
  arm64 \
  debian \
  13 \
  --target docker-desktop \
  --agent codex \
  --dry-run
grep -q 'Workcell launch is not supported' "${TMP_DIR}/compat-dry-run-blocked.stderr"
grep -q 'linux/arm64/debian/13' "${TMP_DIR}/compat-dry-run-blocked.stderr"
grep -q 'local_compat/docker-desktop/compat' "${TMP_DIR}/compat-dry-run-blocked.stderr"
grep -q 'Docker Desktop target remains a lower-assurance compat path' "${TMP_DIR}/compat-dry-run-blocked.stderr"

echo "Compat target dry-run scenario passed"
