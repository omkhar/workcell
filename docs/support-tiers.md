# Support tiers and status vocabulary

This page defines the support vocabulary used across the README, ROADMAP,
`--doctor`, and `--inspect`. The canonical source for host, target, provider,
launch, evidence, and validation-lane claims is
[`policy/host-support-matrix.tsv`](../policy/host-support-matrix.tsv).

## Status

| Value | Meaning |
|---|---|
| `supported` | The matrix row is reviewed for an operator launch path when paired with the row's launch and evidence fields. This value does not create a support claim by itself. |
| `preview-only` | The row is documented for preview or certification-only work. It is not a supported operator launch claim, and the row's launch field still controls whether Workcell may start anything. |
| `validation-host-only` | The row is usable only for the named validation lane. It is not an operator launch host. |
| `unsupported` | The host and target combination is not a supported Workcell combination. Operators should follow the row reason instead of launching. |

## Target assurance class

| Value | Meaning |
|---|---|
| `strict` | The target is intended for the stricter Workcell runtime boundary, such as the dedicated local VM path. The assurance class alone does not confer support. |
| `compat` | The target is a compatibility or lower-assurance path. It must stay explicitly labeled and still depends on the matrix row for launch and evidence. |

## Launch

| Value | Meaning |
|---|---|
| `allowed` | Workcell may proceed with operator launch for the resolved row, subject to the row's status and required evidence. |
| `blocked` | Workcell must not proceed with operator launch for the resolved row. The row reason explains the boundary. |

## Evidence

| Value | Meaning |
|---|---|
| `certification-only` | The row depends on live certification evidence recorded for that matrix entry. This is not replaced by the status or assurance class. |
| `repo-required` | The row depends on repository-owned validation evidence rather than an operator launch claim. |
| `manual-only` | The row depends on manual verification recorded for that matrix entry rather than automated or repository-owned evidence. |
| `none` | The row has no supporting evidence claim. It must not be treated as supported. |

## Validation lane

| Value | Meaning |
|---|---|
| `none` | The row is not assigned to a named validation lane. |
| `trusted-linux-amd64-validator` | The row is assigned to the trusted Linux amd64 validation lane and is not an operator launch host unless another matrix row says so. |

## Target kind

| Value | Meaning |
|---|---|
| `local_vm` | A local VM target kind. The current examples include the Colima strict path. |
| `local_compat` | A local compatibility target kind. The current examples include the Docker Desktop compat path. |
| `remote_vm` | A remote VM target kind. The current macOS examples for AWS EC2 SSM and GCP VM are `preview-only`; the same providers resolve to other statuses on other hosts per the matrix. |

## Current matrix examples

| host_os | host_arch | target_kind | target_provider | target_assurance_class | status | launch | evidence | validation_lane |
|---|---|---|---|---|---|---|---|---|
| `macos` | `arm64` | `local_vm` | `colima` | `strict` | `supported` | `allowed` | `certification-only` | `none` |
| `macos` | `arm64` | `local_compat` | `docker-desktop` | `compat` | `supported` | `allowed` | `certification-only` | `none` |
| `macos` | `arm64` | `remote_vm` | `aws-ec2-ssm` | `compat` | `preview-only` | `blocked` | `certification-only` | `none` |
| `macos` | `arm64` | `remote_vm` | `gcp-vm` | `compat` | `preview-only` | `blocked` | `certification-only` | `none` |
| `linux` | `amd64` | `local_vm` | `colima` | `strict` | `validation-host-only` | `blocked` | `repo-required` | `trusted-linux-amd64-validator` |
| `linux` | `amd64` | `local_compat` | `docker-desktop` | `compat` | `unsupported` | `blocked` | `none` | `none` |

- On macOS arm64 with the Colima `local_vm` strict target, the operator sees an allowed, certification-only launch path for the reviewed local VM row.
- On macOS arm64 with the Docker Desktop `local_compat` compat target, the operator sees an allowed, certification-only launch path for the reviewed compatibility row.
- On macOS arm64 with the AWS EC2 SSM `remote_vm` compat target, the operator sees a preview-only row with launch blocked for certification-only preview work.
- On macOS arm64 with the GCP VM `remote_vm` compat target, the operator sees a preview-only row with launch blocked for certification-only preview work.
- On Linux amd64 with the Colima `local_vm` strict target, the operator sees a validation-host-only row for the trusted Linux amd64 validator with launch blocked.
- On Linux amd64 with the Docker Desktop `local_compat` compat target, the operator sees an unsupported row with launch blocked and no evidence claim.

## No implicit support claim

No tier, assurance class, or status confers a support claim on its own.
Supported launch paths still require the live certification evidence recorded in
[`policy/host-support-matrix.tsv`](../policy/host-support-matrix.tsv).

For the emitted `--doctor` and `--inspect` lines, see
[`docs/diagnostics-and-support-matrix.md`](diagnostics-and-support-matrix.md).
For a suspected runtime boundary breach rather than a support question, follow
the [operator boundary-incident response runbook](incident-response.md).
