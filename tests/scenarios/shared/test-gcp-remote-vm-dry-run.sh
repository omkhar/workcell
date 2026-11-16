#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=tests/scenarios/shared/test-gcp-remote-vm-dry-run.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-gcp-remote-vm-scenario.XXXXXX")"
cleanup() {
  chmod -R u+w "${TMP_DIR}" 2>/dev/null || true
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

HOME_DIR="${TMP_DIR}/home"
WORKSPACE="${TMP_DIR}/workspace"
TARGET_ID="workcell-phase8-cert"
mkdir -p "${HOME_DIR}/.config" "${WORKSPACE}"
git -C "${WORKSPACE}" init -q
printf 'scenario workspace\n' >"${WORKSPACE}/README.md"

is_trusted_host_tool_path() {
  local candidate="$1"
  case "${candidate}" in
    /usr/bin | /usr/bin/* | /bin | /bin/* | /usr/sbin | /usr/sbin/* | /sbin | /sbin/* | /usr/local/bin | /usr/local/bin/* | /usr/local/Cellar | /usr/local/Cellar/* | /usr/local/Caskroom/google-cloud-sdk | /usr/local/Caskroom/google-cloud-sdk/* | /usr/local/google-cloud-sdk | /usr/local/google-cloud-sdk/* | /usr/local/share/google-cloud-sdk | /usr/local/share/google-cloud-sdk/* | /opt/homebrew/bin | /opt/homebrew/bin/* | /opt/homebrew/Cellar | /opt/homebrew/Cellar/* | /opt/homebrew/Caskroom/google-cloud-sdk | /opt/homebrew/Caskroom/google-cloud-sdk/* | /opt/homebrew/share/google-cloud-sdk | /opt/homebrew/share/google-cloud-sdk/*)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

canonicalize_host_tool_path() {
  local candidate="$1"
  if command -v realpath >/dev/null 2>&1; then
    realpath "${candidate}"
    return
  fi
  if command -v perl >/dev/null 2>&1; then
    perl -MCwd=abs_path -e 'print abs_path($ARGV[0]) || $ARGV[0], "\n"' "${candidate}"
    return
  fi
  printf '%s\n' "${candidate}"
}

resolve_host_tool_optional() {
  local candidate=""
  local canonical_candidate=""
  local name="$1"
  shift
  for candidate in "$@" "$(command -v "${name}" 2>/dev/null || true)"; do
    [[ -n "${candidate}" ]] || continue
    [[ -x "${candidate}" ]] || continue
    canonical_candidate="$(canonicalize_host_tool_path "${candidate}")"
    is_trusted_host_tool_path "${candidate}" || continue
    is_trusted_host_tool_path "${canonical_candidate}" || continue
    printf '%s\n' "${canonical_candidate}"
    return 0
  done
  return 1
}

gcp_preview_missing_state() {
  local -a missing=()

  resolve_host_tool_optional gcloud /opt/homebrew/bin/gcloud /usr/local/bin/gcloud /usr/bin/gcloud >/dev/null || missing+=(gcloud)
  if ((${#missing[@]} == 0)); then
    printf 'none\n'
    return 0
  fi
  local IFS=','
  printf '%s\n' "${missing[*]}"
}

run_with_support_override() {
  local label="$1"
  local host_os="$2"
  local host_arch="$3"
  shift 3
  HOME="${HOME_DIR}" XDG_CONFIG_HOME="${HOME_DIR}/.config" \
    WORKCELL_VERIFY_INVARIANTS_SANITIZED_ENTRYPOINT=1 \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_OS="${host_os}" \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_ARCH="${host_arch}" \
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
  shift 4
  set +e
  HOME="${HOME_DIR}" XDG_CONFIG_HOME="${HOME_DIR}/.config" \
    WORKCELL_VERIFY_INVARIANTS_SANITIZED_ENTRYPOINT=1 \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_OS="${host_os}" \
    WORKCELL_TEST_SUPPORT_MATRIX_HOST_ARCH="${host_arch}" \
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

EXPECTED_MISSING="$(gcp_preview_missing_state)"

run_with_support_override \
  "gcp-preview-doctor" \
  macos \
  arm64 \
  --target gcp-vm \
  --target-id "${TARGET_ID}" \
  --agent codex \
  --doctor
grep -q '^target_kind=remote_vm$' "${TMP_DIR}/gcp-preview-doctor.stdout"
grep -q '^target_provider=gcp-vm$' "${TMP_DIR}/gcp-preview-doctor.stdout"
grep -q "^target_id=${TARGET_ID}\$" "${TMP_DIR}/gcp-preview-doctor.stdout"
grep -q '^target_assurance_class=compat$' "${TMP_DIR}/gcp-preview-doctor.stdout"
grep -q '^support_matrix_status=preview-only$' "${TMP_DIR}/gcp-preview-doctor.stdout"
grep -q '^support_matrix_launch=blocked$' "${TMP_DIR}/gcp-preview-doctor.stdout"
grep -q '^support_matrix_evidence=certification-only$' "${TMP_DIR}/gcp-preview-doctor.stdout"
grep -q '^support_matrix_reason=apple-silicon-macos-gcp-vm-preview-certification-only$' "${TMP_DIR}/gcp-preview-doctor.stdout"
grep -q '^runtime_api=brokered$' "${TMP_DIR}/gcp-preview-doctor.stdout"
grep -q '^workspace_transport=remote-materialization$' "${TMP_DIR}/gcp-preview-doctor.stdout"
grep -q '^profile_dir=none$' "${TMP_DIR}/gcp-preview-doctor.stdout"
grep -Eq "^target_state_dir=.*/targets/remote_vm/gcp-vm/${TARGET_ID}\$" "${TMP_DIR}/gcp-preview-doctor.stdout"
grep -Eq "^profile_marker=.*/targets/remote_vm/gcp-vm/${TARGET_ID}/workcell\\.managed\$" "${TMP_DIR}/gcp-preview-doctor.stdout"
grep -Eq "^image_marker=.*/targets/remote_vm/gcp-vm/${TARGET_ID}/workcell\\.image-ready\$" "${TMP_DIR}/gcp-preview-doctor.stdout"
if [[ "${EXPECTED_MISSING}" == "none" ]]; then
  grep -q '^doctor_missing_host_tools=none$' "${TMP_DIR}/gcp-preview-doctor.stdout"
  grep -q '^doctor_recommended_next=review-gcp-preview-rollout$' "${TMP_DIR}/gcp-preview-doctor.stdout"
else
  grep -q "^doctor_missing_host_tools=${EXPECTED_MISSING}\$" "${TMP_DIR}/gcp-preview-doctor.stdout"
  grep -q '^doctor_recommended_next=install-host-tools$' "${TMP_DIR}/gcp-preview-doctor.stdout"
fi

run_with_support_override \
  "gcp-preview-dry-run" \
  macos \
  arm64 \
  --target gcp-vm \
  --target-id "${TARGET_ID}" \
  --agent codex \
  --dry-run
grep -q "^target_kind=remote_vm target_provider=gcp-vm target_id=${TARGET_ID} target_assurance_class=compat runtime_api=brokered workspace_transport=remote-materialization\$" "${TMP_DIR}/gcp-preview-dry-run.stderr"
grep -q '^remote_access_model=brokered remote_broker=gcp-iap-ssh inbound_public_ssh=blocked live_smoke=certification-only$' "${TMP_DIR}/gcp-preview-dry-run.stderr"
grep -Eq "^execution_path=remote-preview-broker-plan audit_log=.*/targets/remote_vm/gcp-vm/${TARGET_ID}/workcell\\.audit\\.log rollout_doc=docs/gcp-vm-preview\\.md\$" "${TMP_DIR}/gcp-preview-dry-run.stderr"
grep -Eq "^remote-preview-plan target=gcp-vm broker=gcp-iap-ssh target_id=${TARGET_ID} launch_gate=certification-only workspace=.*/workspace\$" "${TMP_DIR}/gcp-preview-dry-run.stdout"

run_with_support_override_expect_failure \
  2 \
  "gcp-preview-live-blocked" \
  macos \
  arm64 \
  --target gcp-vm \
  --target-id "${TARGET_ID}" \
  --agent codex
grep -q 'The GCP remote VM target is a preview-only brokered path' "${TMP_DIR}/gcp-preview-live-blocked.stderr"
grep -q 'docs/gcp-vm-preview.md' "${TMP_DIR}/gcp-preview-live-blocked.stderr"

run_with_support_override \
  "gcp-preview-doctor-unsupported" \
  linux \
  arm64 \
  --target gcp-vm \
  --target-id "${TARGET_ID}" \
  --agent codex \
  --doctor
grep -q '^support_matrix_status=unsupported$' "${TMP_DIR}/gcp-preview-doctor-unsupported.stdout"
grep -q '^support_matrix_launch=blocked$' "${TMP_DIR}/gcp-preview-doctor-unsupported.stdout"
grep -q '^doctor_recommended_next=use-supported-host$' "${TMP_DIR}/gcp-preview-doctor-unsupported.stdout"

echo "GCP remote VM dry-run scenario passed"
