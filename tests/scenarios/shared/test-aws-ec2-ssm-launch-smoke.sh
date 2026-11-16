#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=tests/scenarios/shared/test-aws-ec2-ssm-launch-smoke.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"
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

SECURITY_GROUP_IDS=()
while IFS= read -r security_group_id; do
  [[ -n "${security_group_id}" ]] && SECURITY_GROUP_IDS+=("${security_group_id}")
done < <(jq -r '.Reservations[0].Instances[0].SecurityGroups[].GroupId' "${TMP_DIR}/instance.json")
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
