# Support Tiers and Status Terms

This page defines the terms that Workcell uses for host and target support. The
canonical source is
[`policy/host-support-matrix.tsv`](../policy/host-support-matrix.tsv).

Read a complete matrix row before you make a support decision. No one field
gives support by itself.

## Status

| Value | Meaning |
|---|---|
| `supported` | The row can be an operator launch path when `launch=allowed` and the required evidence exists. |
| `preview-only` | The row is for documented preview or certification work. It is not a supported operator launch path. |
| `validation-host-only` | The row is only for its named validation lane. It is not an operator launch path. |
| `unsupported` | Workcell does not support this host and target combination. |

## Target assurance class

| Value | Meaning |
|---|---|
| `strict` | The target uses the stricter Workcell boundary, such as the dedicated local VM path. This value does not give support by itself. |
| `compat` | The target is a compatibility path with lower assurance. The matrix row still controls launch and evidence. |
| `per-session-vm` | The target design gives each session a VM. The shipped Apple container row is still preview-only and blocked. |

## Launch

| Value | Meaning |
|---|---|
| `allowed` | Workcell can start an operator session when the row is also `supported` and has the required evidence. |
| `blocked` | Workcell must not start an operator session. Read the row reason. |

## Evidence

| Value | Meaning |
|---|---|
| `certification-only` | The row depends on its recorded live certification evidence. |
| `repo-required` | The row depends on repository-owned validation. This does not make it an operator launch path. |
| `manual-only` | The row depends on recorded manual verification. |
| `none` | The row has no evidence claim. Do not treat it as supported. |

## Validation lane

| Value | Meaning |
|---|---|
| `none` | The row has no named validation lane. |
| `trusted-linux-amd64-validator` | The row is for the trusted Linux amd64 validation lane. It is not an operator launch host. |

## Target kind

| Value | Meaning |
|---|---|
| `local_vm` | A local VM target, such as Colima or the Apple container preview. |
| `local_compat` | A local compatibility target, such as Docker Desktop. |
| `remote_vm` | Workcell reaches a remote VM through reviewed broker access. The AWS and GCP paths are preview or validation paths. |

## Current representative rows

The canonical matrix contains more unsupported host and architecture rows. The
table below shows every shipped macOS arm64 row and every Linux amd64 row.

| host_os | host_arch | target_kind | target_provider | target_assurance_class | status | launch | evidence | validation_lane |
|---|---|---|---|---|---|---|---|---|
| `macos` | `arm64` | `local_vm` | `colima` | `strict` | `supported` | `allowed` | `certification-only` | `none` |
| `macos` | `arm64` | `local_compat` | `docker-desktop` | `compat` | `supported` | `allowed` | `certification-only` | `none` |
| `macos` | `arm64` | `remote_vm` | `aws-ec2-ssm` | `compat` | `preview-only` | `blocked` | `certification-only` | `none` |
| `macos` | `arm64` | `remote_vm` | `gcp-vm` | `compat` | `preview-only` | `blocked` | `certification-only` | `none` |
| `macos` | `arm64` | `local_vm` | `apple-container` | `per-session-vm` | `preview-only` | `blocked` | `certification-only` | `none` |
| `linux` | `amd64` | `local_vm` | `colima` | `strict` | `validation-host-only` | `blocked` | `repo-required` | `trusted-linux-amd64-validator` |
| `linux` | `amd64` | `local_compat` | `docker-desktop` | `compat` | `unsupported` | `blocked` | `none` | `none` |
| `linux` | `amd64` | `remote_vm` | `aws-ec2-ssm` | `compat` | `validation-host-only` | `blocked` | `repo-required` | `trusted-linux-amd64-validator` |
| `linux` | `amd64` | `remote_vm` | `gcp-vm` | `compat` | `validation-host-only` | `blocked` | `repo-required` | `trusted-linux-amd64-validator` |

Linux arm64 and Windows rows are `unsupported`, `blocked`, and have no evidence
claim. The `apple-container` target also requires macOS 26, but its matrix row
is still preview-only and blocked. See the canonical matrix for the exact
reason in each row.

See [diagnostics-and-support-matrix.md](diagnostics-and-support-matrix.md) for
the fields from `--doctor` and `--inspect`.
