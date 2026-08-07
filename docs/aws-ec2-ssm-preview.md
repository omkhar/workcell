# AWS EC2 SSM Preview

## Status

Workcell exposes a dry-run broker plan for
`remote_vm/aws-ec2-ssm/compat`. The host-support matrix marks this target
`preview-only` and `blocked` on macOS arm64.

Workcell blocks operator launch. Workcell does not implement live remote
launch. Live certification validates AWS SSM broker access and network posture
only. It does not validate a Workcell-managed remote session.

## Dry-Run Command

```bash
workcell \
  --target aws-ec2-ssm \
  --target-id i-1234567890abcdef0 \
  --agent codex \
  --workspace /path/to/repo \
  --dry-run
```

The plan reports `runtime_api=brokered` and
`remote_broker=aws-ssm-session-manager`. It also reports
`inbound_public_ssh=blocked` and `live_smoke=certification-only`.

## Required Tools and Infrastructure

The host must have `aws`, `session-manager-plugin`, and `jq`. The target must be
an EC2 instance that AWS Systems Manager manages.

The instance role must include `AmazonSSMManagedInstanceCore`. The preview
broker plan requires these IAM actions:

- `ec2:DescribeInstances`
- `ssm:DescribeInstanceInformation`
- `ssm:StartSession`
- `ssm:ResumeSession`
- `ssm:TerminateSession`

The certification identity also needs these inspection permissions:

- `sts:GetCallerIdentity`
- `ec2:DescribeSecurityGroups`
- `iam:GetInstanceProfile`
- `iam:ListAttachedRolePolicies`

## Certification Scope

The certification script runs outside the Workcell runtime boundary. It uses
host AWS credentials. Workcell does not stage or isolate these credentials.

The target must have the `running` state and the SSM status `Online`. Its
security groups must have no inbound rules. The direct broker command must
succeed.

The script also checks Workcell diagnostics and the dry-run broker plan. It
does not copy a workspace or start a Workcell remote session.

## Certification Command

```bash
WORKCELL_AWS_EC2_SSM_REGION=us-east-1 \
WORKCELL_AWS_EC2_SSM_TARGET_ID=i-1234567890abcdef0 \
  bash ./tests/scenarios/shared/test-aws-ec2-ssm-launch-smoke.sh
```

Do not sign a commit that changes the AWS preview claim until live
certification succeeds.

## Rollback

Do not use `--target aws-ec2-ssm`. Select a supported target from the
host-support matrix.

Workcell does not provision or remove the cloud VM. If provider cleanup is
necessary, the operator must use AWS tools.

Workcell must not select Colima or Docker Desktop automatically.

## Authoritative Sources

- [Remote VM Contract](remote-vm-contract.md)
- [Host-Support Matrix](../policy/host-support-matrix.tsv)
- [Validation Scenarios](validation-scenarios.md)
