# AWS EC2 SSM Preview

Workcell now exposes a preview-only `remote_vm/aws-ec2-ssm/compat` target
selection path. This is intentionally narrower than the reviewed local Colima
boundary:

- repo-required support today is deterministic target selection, diagnostics,
  state-root routing, and shared remote-VM conformance reuse
- live AWS execution remains certification-only
- the reviewed live path must use brokered AWS Systems Manager Session Manager
  access; inbound public SSH is out of bounds for the supported path

Use this page with:

- [docs/remote-vm-contract.md](remote-vm-contract.md)
- [docs/validation-scenarios.md](validation-scenarios.md)
- [policy/host-support-matrix.tsv](../policy/host-support-matrix.tsv)

## Canonical Preview Plan

Inspect the reviewed broker plan without attempting live remote execution:

```bash
workcell \
  --target aws-ec2-ssm \
  --target-id i-1234567890abcdef0 \
  --agent codex \
  --workspace /path/to/repo \
  --dry-run
```

The dry-run path emits:

- `target_kind=remote_vm`
- `target_provider=aws-ec2-ssm`
- `target_assurance_class=compat`
- `runtime_api=brokered`
- `workspace_transport=remote-materialization`
- `remote_access_model=brokered`
- `remote_broker=aws-ssm-session-manager`
- `inbound_public_ssh=blocked`
- `live_smoke=certification-only`

`workcell --doctor` exposes the same support boundary through
`support_matrix_*` plus the required host tools.

## Host Prerequisites

The preview target expects these host-side tools and dependencies:

- `aws`
- `session-manager-plugin`
- an EC2 instance managed by AWS Systems Manager
- an instance profile that includes `AmazonSSMManagedInstanceCore`
- IAM permissions for `ec2:DescribeInstances`,
  `ssm:DescribeInstanceInformation`, `ssm:StartSession`,
  `ssm:ResumeSession`, and `ssm:TerminateSession`

These are host prerequisites. They are not mounted into the reviewed local
runtime boundary.

## Live Certification Gate

Live AWS use is intentionally blocked on the default launch path until the
operator performs the separate certification run on reviewed infrastructure.

The certification lane must prove:

- the selected target is reachable through Session Manager
- no inbound public SSH is required
- remote workspace materialization stays explicit and host-auditable
- the brokered lifecycle and audit story match the shared `remote_vm`
  contract

Run the certification smoke with an already reviewed SSM-managed EC2 target:

```bash
WORKCELL_AWS_EC2_SSM_REGION=us-east-1 \
WORKCELL_AWS_EC2_SSM_TARGET_ID=i-1234567890abcdef0 \
  bash ./tests/scenarios/shared/test-aws-ec2-ssm-launch-smoke.sh
```

The smoke fails unless the target is running, SSM-online, attached to a role
with `AmazonSSMManagedInstanceCore`, has no inbound security-group rules, can
run a brokered Session Manager command, and matches the Workcell preview broker
plan.

Do not sign a commit that claims supported AWS preview delivery until that
live certification succeeds.

## Rollback

Rollback is explicit:

1. stop using `--target aws-ec2-ssm`
2. return to the reviewed `--target colima` path
3. remove any AWS preview target state under
   `~/.local/state/workcell/targets/remote_vm/aws-ec2-ssm/`

There is no silent fallback from the AWS preview target onto Colima or Docker
Desktop.
