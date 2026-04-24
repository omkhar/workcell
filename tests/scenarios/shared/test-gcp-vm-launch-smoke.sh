#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-gcp-vm-launch-smoke.XXXXXX")"
TARGET_ID="${WORKCELL_GCP_VM_TARGET_ID:-${GCP_INSTANCE_NAME:-}}"
GCP_ZONE_SELECTED="${WORKCELL_GCP_VM_ZONE:-${GCP_ZONE:-}}"
GCP_PROJECT_SELECTED="${WORKCELL_GCP_VM_PROJECT:-${GCP_PROJECT:-${GOOGLE_CLOUD_PROJECT:-}}}"

cleanup() {
  local status=$?
  if [[ "${status}" -ne 0 ]]; then
    for file in \
      gcloud-account.json \
      instance.json \
      workcell-doctor.stdout workcell-doctor.stderr \
      workcell-dry-run.stdout workcell-dry-run.stderr \
      iap-ssh.out; do
      [[ -f "${TMP_DIR}/${file}" ]] || continue
      printf -- '--- %s ---\n' "${file}" >&2
      cat "${TMP_DIR}/${file}" >&2
    done
  fi
  chmod -R u+w "${TMP_DIR}" 2>/dev/null || true
  rm -rf "${TMP_DIR}"
  exit "${status}"
}
trap cleanup EXIT

fail() {
  echo "$*" >&2
  exit 1
}

require_tool() {
  local tool="$1"
  command -v "${tool}" >/dev/null 2>&1 || fail "Missing required host tool: ${tool}"
}

gcloud_json() {
  CLOUDSDK_CORE_DISABLE_PROMPTS=1 gcloud "$@" --format=json
}

require_tool gcloud
require_tool jq

[[ -n "${TARGET_ID}" ]] || fail "Set WORKCELL_GCP_VM_TARGET_ID to the reviewed GCE instance name."
if [[ -z "${GCP_ZONE_SELECTED}" ]]; then
  GCP_ZONE_SELECTED="$(gcloud config get-value compute/zone 2>/dev/null || true)"
fi
[[ -n "${GCP_ZONE_SELECTED}" && "${GCP_ZONE_SELECTED}" != "(unset)" ]] || fail "Set WORKCELL_GCP_VM_ZONE or GCP_ZONE."
if [[ -z "${GCP_PROJECT_SELECTED}" ]]; then
  GCP_PROJECT_SELECTED="$(gcloud config get-value project 2>/dev/null || true)"
fi
[[ -n "${GCP_PROJECT_SELECTED}" && "${GCP_PROJECT_SELECTED}" != "(unset)" ]] || fail "Set WORKCELL_GCP_VM_PROJECT, GCP_PROJECT, GOOGLE_CLOUD_PROJECT, or gcloud config project."

HOME_DIR="${TMP_DIR}/home"
WORKSPACE="${TMP_DIR}/workspace"
mkdir -p "${HOME_DIR}/.config" "${WORKSPACE}"
git -C "${WORKSPACE}" init -q
printf 'gcp vm certification workspace\n' >"${WORKSPACE}/README.md"
WORKSPACE="$(cd "${WORKSPACE}" && pwd -P)"

gcloud_json auth list --filter=status:ACTIVE >"${TMP_DIR}/gcloud-account.json"
jq -e '. | length >= 1' "${TMP_DIR}/gcloud-account.json" >/dev/null ||
  fail "gcloud has no active account."

gcloud_json compute instances describe "${TARGET_ID}" \
  --zone "${GCP_ZONE_SELECTED}" \
  --project "${GCP_PROJECT_SELECTED}" \
  >"${TMP_DIR}/instance.json"
[[ "$(jq -r '.status' "${TMP_DIR}/instance.json")" == "RUNNING" ]] ||
  fail "GCP target ${TARGET_ID} is not RUNNING."
jq -e '[.networkInterfaces[]?.accessConfigs[]? | select(.natIP != null)] | length == 0' \
  "${TMP_DIR}/instance.json" >/dev/null ||
  fail "GCP target ${TARGET_ID} must not expose an external NAT IP for certification."

HOME="${HOME_DIR}" XDG_CONFIG_HOME="${HOME_DIR}/.config" \
  "${ROOT_DIR}/scripts/workcell" \
  --target gcp-vm \
  --target-id "${TARGET_ID}" \
  --agent codex \
  --workspace "${WORKSPACE}" \
  --no-default-injection-policy \
  --doctor \
  >"${TMP_DIR}/workcell-doctor.stdout" 2>"${TMP_DIR}/workcell-doctor.stderr"
grep -q '^target_kind=remote_vm$' "${TMP_DIR}/workcell-doctor.stdout"
grep -q '^target_provider=gcp-vm$' "${TMP_DIR}/workcell-doctor.stdout"
grep -q "^target_id=${TARGET_ID}\$" "${TMP_DIR}/workcell-doctor.stdout"
grep -q '^support_matrix_status=preview-only$' "${TMP_DIR}/workcell-doctor.stdout"
grep -q '^support_matrix_evidence=certification-only$' "${TMP_DIR}/workcell-doctor.stdout"
grep -q '^doctor_missing_host_tools=none$' "${TMP_DIR}/workcell-doctor.stdout"
grep -q '^doctor_recommended_next=review-gcp-preview-rollout$' "${TMP_DIR}/workcell-doctor.stdout"

HOME="${HOME_DIR}" XDG_CONFIG_HOME="${HOME_DIR}/.config" \
  "${ROOT_DIR}/scripts/workcell" \
  --target gcp-vm \
  --target-id "${TARGET_ID}" \
  --agent codex \
  --workspace "${WORKSPACE}" \
  --no-default-injection-policy \
  --dry-run \
  >"${TMP_DIR}/workcell-dry-run.stdout" 2>"${TMP_DIR}/workcell-dry-run.stderr"
grep -q "^target_kind=remote_vm target_provider=gcp-vm target_id=${TARGET_ID} target_assurance_class=compat runtime_api=brokered workspace_transport=remote-materialization\$" \
  "${TMP_DIR}/workcell-dry-run.stderr"
grep -q '^remote_access_model=brokered remote_broker=gcp-iap-ssh inbound_public_ssh=blocked live_smoke=certification-only$' \
  "${TMP_DIR}/workcell-dry-run.stderr"
grep -q '^remote_workspace_materialization=explicit reviewed_host_copy=1$' \
  "${TMP_DIR}/workcell-dry-run.stderr"
grep -Eq "^remote-preview-plan target=gcp-vm broker=gcp-iap-ssh target_id=${TARGET_ID} launch_gate=certification-only workspace=${WORKSPACE}\$" \
  "${TMP_DIR}/workcell-dry-run.stdout"

set +e
CLOUDSDK_CORE_DISABLE_PROMPTS=1 gcloud compute ssh "${TARGET_ID}" \
  --zone "${GCP_ZONE_SELECTED}" \
  --project "${GCP_PROJECT_SELECTED}" \
  --tunnel-through-iap \
  --command "printf 'workcell-gcp-iap-ok\\n'; hostname" \
  -- -o BatchMode=yes -o StrictHostKeyChecking=accept-new \
  >"${TMP_DIR}/iap-ssh.out" 2>&1
session_rc=$?
set -e
[[ "${session_rc}" -eq 0 ]] || fail "GCP IAP SSH command failed with exit code ${session_rc}."
grep -q 'workcell-gcp-iap-ok' "${TMP_DIR}/iap-ssh.out"

echo "GCP VM launch smoke passed for ${TARGET_ID} in ${GCP_PROJECT_SELECTED}/${GCP_ZONE_SELECTED}"
