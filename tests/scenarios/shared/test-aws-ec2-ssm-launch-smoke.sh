#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-aws-ec2-ssm-launch-smoke.XXXXXX")"
TARGET_ID="${WORKCELL_AWS_EC2_SSM_TARGET_ID:-${INSTANCE_ID:-}}"
AWS_REGION_SELECTED="${WORKCELL_AWS_EC2_SSM_REGION:-${AWS_REGION:-${AWS_DEFAULT_REGION:-}}}"

cleanup() {
  local status=$?
  if [[ "${status}" -ne 0 ]]; then
    for file in \
      aws-identity.json \
      instance.json \
      instance-profile.json \
      role-policies.json \
      ssm-instance.json \
      session-manager.out \
      workcell-doctor.stdout workcell-doctor.stderr \
      workcell-dry-run.stdout workcell-dry-run.stderr; do
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

aws_json() {
  AWS_PAGER='' aws --region "${AWS_REGION_SELECTED}" "$@" --output json
}

require_tool aws
require_tool session-manager-plugin
require_tool jq

[[ -n "${TARGET_ID}" ]] || fail "Set WORKCELL_AWS_EC2_SSM_TARGET_ID to the SSM-managed EC2 instance id."
if [[ -z "${AWS_REGION_SELECTED}" ]]; then
  AWS_REGION_SELECTED="$(AWS_PAGER='' aws configure get region 2>/dev/null || true)"
fi
[[ -n "${AWS_REGION_SELECTED}" ]] || fail "Set WORKCELL_AWS_EC2_SSM_REGION or AWS_REGION."

HOME_DIR="${TMP_DIR}/home"
WORKSPACE="${TMP_DIR}/workspace"
mkdir -p "${HOME_DIR}/.config" "${WORKSPACE}"
git -C "${WORKSPACE}" init -q
printf 'aws ec2 ssm certification workspace\n' >"${WORKSPACE}/README.md"
WORKSPACE="$(cd "${WORKSPACE}" && pwd -P)"

aws_json sts get-caller-identity >"${TMP_DIR}/aws-identity.json"

aws_json ec2 describe-instances \
  --instance-ids "${TARGET_ID}" \
  >"${TMP_DIR}/instance.json"
jq -e '(.Reservations | length == 1) and (.Reservations[0].Instances | length == 1)' "${TMP_DIR}/instance.json" >/dev/null
[[ "$(jq -r '.Reservations[0].Instances[0].State.Name' "${TMP_DIR}/instance.json")" == "running" ]] ||
  fail "EC2 target ${TARGET_ID} is not running."

mapfile -t SECURITY_GROUP_IDS < <(jq -r '.Reservations[0].Instances[0].SecurityGroups[].GroupId' "${TMP_DIR}/instance.json")
((${#SECURITY_GROUP_IDS[@]} > 0)) || fail "EC2 target ${TARGET_ID} has no security groups."
for sg_id in "${SECURITY_GROUP_IDS[@]}"; do
  aws_json ec2 describe-security-groups --group-ids "${sg_id}" >"${TMP_DIR}/security-group-${sg_id}.json"
  jq -e '.SecurityGroups[0].IpPermissions == []' "${TMP_DIR}/security-group-${sg_id}.json" >/dev/null ||
    fail "Security group ${sg_id} must have no inbound rules for AWS EC2 SSM certification."
done

INSTANCE_PROFILE_ARN="$(jq -r '.Reservations[0].Instances[0].IamInstanceProfile.Arn // empty' "${TMP_DIR}/instance.json")"
[[ -n "${INSTANCE_PROFILE_ARN}" ]] || fail "EC2 target ${TARGET_ID} has no IAM instance profile."
INSTANCE_PROFILE_NAME="${INSTANCE_PROFILE_ARN##*/}"
aws_json iam get-instance-profile \
  --instance-profile-name "${INSTANCE_PROFILE_NAME}" \
  >"${TMP_DIR}/instance-profile.json"
ROLE_NAME="$(jq -r '.InstanceProfile.Roles[0].RoleName // empty' "${TMP_DIR}/instance-profile.json")"
[[ -n "${ROLE_NAME}" ]] || fail "Instance profile ${INSTANCE_PROFILE_NAME} has no role."
aws_json iam list-attached-role-policies \
  --role-name "${ROLE_NAME}" \
  >"${TMP_DIR}/role-policies.json"
jq -e 'any(.AttachedPolicies[]?; .PolicyArn == "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore")' \
  "${TMP_DIR}/role-policies.json" >/dev/null ||
  fail "Role ${ROLE_NAME} must attach AmazonSSMManagedInstanceCore."

aws_json ssm describe-instance-information \
  --filters "Key=InstanceIds,Values=${TARGET_ID}" \
  >"${TMP_DIR}/ssm-instance.json"
jq -e '.InstanceInformationList | length == 1' "${TMP_DIR}/ssm-instance.json" >/dev/null
[[ "$(jq -r '.InstanceInformationList[0].PingStatus' "${TMP_DIR}/ssm-instance.json")" == "Online" ]] ||
  fail "SSM target ${TARGET_ID} is not Online."
[[ "$(jq -r '.InstanceInformationList[0].ResourceType' "${TMP_DIR}/ssm-instance.json")" == "EC2Instance" ]] ||
  fail "SSM target ${TARGET_ID} is not an EC2 managed instance."

HOME="${HOME_DIR}" XDG_CONFIG_HOME="${HOME_DIR}/.config" \
  "${ROOT_DIR}/scripts/workcell" \
  --target aws-ec2-ssm \
  --target-id "${TARGET_ID}" \
  --agent codex \
  --workspace "${WORKSPACE}" \
  --no-default-injection-policy \
  --doctor \
  >"${TMP_DIR}/workcell-doctor.stdout" 2>"${TMP_DIR}/workcell-doctor.stderr"
grep -q '^target_kind=remote_vm$' "${TMP_DIR}/workcell-doctor.stdout"
grep -q '^target_provider=aws-ec2-ssm$' "${TMP_DIR}/workcell-doctor.stdout"
grep -q "^target_id=${TARGET_ID}\$" "${TMP_DIR}/workcell-doctor.stdout"
grep -q '^support_matrix_status=preview-only$' "${TMP_DIR}/workcell-doctor.stdout"
grep -q '^support_matrix_evidence=certification-only$' "${TMP_DIR}/workcell-doctor.stdout"
grep -q '^doctor_missing_host_tools=none$' "${TMP_DIR}/workcell-doctor.stdout"
grep -q '^doctor_recommended_next=review-aws-preview-rollout$' "${TMP_DIR}/workcell-doctor.stdout"

HOME="${HOME_DIR}" XDG_CONFIG_HOME="${HOME_DIR}/.config" \
  "${ROOT_DIR}/scripts/workcell" \
  --target aws-ec2-ssm \
  --target-id "${TARGET_ID}" \
  --agent codex \
  --workspace "${WORKSPACE}" \
  --no-default-injection-policy \
  --dry-run \
  >"${TMP_DIR}/workcell-dry-run.stdout" 2>"${TMP_DIR}/workcell-dry-run.stderr"
grep -q "^target_kind=remote_vm target_provider=aws-ec2-ssm target_id=${TARGET_ID} target_assurance_class=compat runtime_api=brokered workspace_transport=remote-materialization\$" \
  "${TMP_DIR}/workcell-dry-run.stderr"
grep -q '^remote_access_model=brokered remote_broker=aws-ssm-session-manager inbound_public_ssh=blocked live_smoke=certification-only$' \
  "${TMP_DIR}/workcell-dry-run.stderr"
grep -q '^remote_workspace_materialization=explicit reviewed_host_copy=1$' \
  "${TMP_DIR}/workcell-dry-run.stderr"
grep -Eq "^remote-preview-plan target=aws-ec2-ssm broker=aws-ssm-session-manager target_id=${TARGET_ID} launch_gate=certification-only workspace=${WORKSPACE}\$" \
  "${TMP_DIR}/workcell-dry-run.stdout"

set +e
AWS_PAGER='' aws --region "${AWS_REGION_SELECTED}" ssm start-session \
  --target "${TARGET_ID}" \
  --document-name AWS-StartInteractiveCommand \
  --parameters '{"command":["sh -lc '\''echo workcell-session-manager-ok; systemctl is-active amazon-ssm-agent; hostname; exit'\''"]}' \
  >"${TMP_DIR}/session-manager.out" 2>&1
session_rc=$?
set -e
[[ "${session_rc}" -eq 0 ]] || fail "Session Manager command failed with exit code ${session_rc}."
grep -q 'workcell-session-manager-ok' "${TMP_DIR}/session-manager.out"
grep -q '^active' "${TMP_DIR}/session-manager.out"

echo "AWS EC2 SSM launch smoke passed for ${TARGET_ID} in ${AWS_REGION_SELECTED}"
