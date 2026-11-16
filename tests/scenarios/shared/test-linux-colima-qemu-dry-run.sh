#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=tests/scenarios/shared/test-linux-colima-qemu-dry-run.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-linux-colima-qemu-scenario.XXXXXX")"
cleanup() {
  chmod -R u+w "${TMP_DIR}" 2>/dev/null || true
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

HOME_DIR="${TMP_DIR}/home"
WORKSPACE="${TMP_DIR}/workspace"
KVM_READY="${TMP_DIR}/kvm-ready"
KVM_MISSING="${TMP_DIR}/kvm-missing"
KVM_DENIED="${TMP_DIR}/kvm-denied"
mkdir -p "${HOME_DIR}/.config" "${WORKSPACE}"
git -C "${WORKSPACE}" init -q
printf 'scenario workspace\n' >"${WORKSPACE}/README.md"
: >"${KVM_READY}"
: >"${KVM_DENIED}"
chmod 000 "${KVM_DENIED}"

# run_host <expected_rc> <label> <host_os> <kvm_device> <args...>
run_host() {
  local expected_rc="$1"
  local label="$2"
  local host_os="$3"
  local kvm_device="$4"
  local host_arch="amd64" host_distro="ubuntu" host_distro_version="24.04"
  local rc=0
  shift 4
  if [[ "${host_os}" == "macos" ]]; then
    host_arch="arm64" host_distro="none" host_distro_version="none"
  fi
  set +e
  HOME="${HOME_DIR}" XDG_CONFIG_HOME="${HOME_DIR}/.config" \
    WORKCELL_VERIFY_INVARIANTS_SANITIZED_ENTRYPOINT=1 \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_OS="${host_os}" \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_ARCH="${host_arch}" \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_DISTRO="${host_distro}" \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_DISTRO_VERSION="${host_distro_version}" \
    WORKCELL_TEST_KVM_DEVICE="${kvm_device}" \
    /bin/bash -p "${ROOT_DIR}/scripts/workcell" \
    --agent codex \
    "$@" \
    --workspace "${WORKSPACE}" \
    --no-default-injection-policy \
    >"${TMP_DIR}/${label}.stdout" 2>"${TMP_DIR}/${label}.stderr"
  rc=$?
  set -e
  if [[ "${rc}" -ne "${expected_rc}" ]]; then
    echo "Unexpected exit code for ${label}: ${rc} (expected ${expected_rc})" >&2
    cat "${TMP_DIR}/${label}.stderr" >&2
    exit 1
  fi
}

expect() {
  local label="$1" stream="$2" pattern="$3"
  if ! grep -Eq -- "${pattern}" "${TMP_DIR}/${label}.${stream}"; then
    echo "Expected ${label} ${stream} to match: ${pattern}" >&2
    cat "${TMP_DIR}/${label}.${stream}" >&2
    exit 1
  fi
}

refute() {
  local label="$1" stream="$2" pattern="$3"
  if grep -Eq -- "${pattern}" "${TMP_DIR}/${label}.${stream}"; then
    echo "Expected ${label} ${stream} not to match: ${pattern}" >&2
    cat "${TMP_DIR}/${label}.${stream}" >&2
    exit 1
  fi
}

# Linux selects qemu with 9p and reports a usable KVM device.
run_host 0 linux-doctor-ready linux "${KVM_READY}" --doctor
expect linux-doctor-ready stdout '^host_os=linux$'
expect linux-doctor-ready stdout '^doctor_colima_vm_type=qemu$'
expect linux-doctor-ready stdout '^doctor_colima_mount_type=9p$'
expect linux-doctor-ready stdout '^doctor_kvm=ready$'

# Doctor names the KVM cause.
run_host 0 linux-doctor-missing linux "${KVM_MISSING}" --doctor
expect linux-doctor-missing stdout '^doctor_kvm=missing$'
if [[ "$(id -u)" -ne 0 ]]; then
  run_host 0 linux-doctor-denied linux "${KVM_DENIED}" --doctor
  expect linux-doctor-denied stdout '^doctor_kvm=denied$'
fi

# macOS keeps vz with virtiofs and does not need KVM.
run_host 0 macos-doctor macos "${KVM_MISSING}" --doctor
expect macos-doctor stdout '^doctor_colima_vm_type=vz$'
expect macos-doctor stdout '^doctor_colima_mount_type=virtiofs$'
expect macos-doctor stdout '^doctor_kvm=not-required$'

# The reviewed host matrix still blocks Linux strict launch until live
# certification promotes it, so both dry-runs exit 2 with no launch plan.
# With KVM, the dry-run names no KVM cause.
run_host 2 linux-dry-run-ready linux "${KVM_READY}" --dry-run
expect linux-dry-run-ready stderr 'Workcell launch is not supported for linux/amd64/ubuntu/24.04'
refute linux-dry-run-ready stderr 'needs KVM'
refute linux-dry-run-ready stdout 'run '

# Without KVM, the dry-run also names the KVM cause and refuses TCG.
run_host 2 linux-dry-run-no-kvm linux "${KVM_MISSING}" --dry-run
expect linux-dry-run-no-kvm stderr 'Workcell launch is not supported for linux/amd64/ubuntu/24.04'
expect linux-dry-run-no-kvm stderr 'needs KVM, but /dev/kvm is missing'
expect linux-dry-run-no-kvm stderr 'does not fall back to QEMU TCG'
refute linux-dry-run-no-kvm stdout 'run '

# The KVM gate does not apply to the macOS vz path.
run_host 0 macos-dry-run macos "${KVM_MISSING}" --dry-run
refute macos-dry-run stderr 'needs KVM'
expect macos-dry-run stdout 'run '

echo "Linux Colima QEMU dry-run scenario passed"
