# Host Support Matrix

This file is generated from
[`policy/host-support-matrix.tsv`](../policy/host-support-matrix.tsv). Do not
edit it directly.

The canonical matrix is keyed by `host_os`, `host_arch`, `target_kind`,
`target_provider`, and `target_assurance_class`. `workcell --inspect`,
`workcell --doctor`, and launch gating all derive their support-boundary output
from that same artifact.

## Validation-host bridge

- `trusted-linux-amd64-validator`:
  `./scripts/build-and-test.sh --docker`
  runs repo validation inside the pinned Linux validator container from a
  disposable snapshot. This is a reviewed validation-host bridge, not a
  supported Workcell launch host.

## Matrix

| Host OS | Host arch | Target kind | Target provider | Assurance class | Support status | Launch | Evidence tier | Validation lane | Reason |
|---|---|---|---|---|---|---|---|---|---|
| `macos` | `arm64` | `local_vm` | `colima` | `strict` | `supported` | `allowed` | `certification-only` | `none` | `apple-silicon-macos-reviewed-launch-host` |
| `linux` | `amd64` | `local_vm` | `colima` | `strict` | `validation-host-only` | `blocked` | `repo-required` | `trusted-linux-amd64-validator` | `trusted-linux-amd64-validation-host-only` |
| `linux` | `arm64` | `local_vm` | `colima` | `strict` | `unsupported` | `blocked` | `none` | `none` | `linux-arm64-hosts-not-yet-reviewed` |
| `windows` | `amd64` | `local_vm` | `colima` | `strict` | `unsupported` | `blocked` | `none` | `none` | `windows-hosts-not-yet-reviewed` |
| `windows` | `arm64` | `local_vm` | `colima` | `strict` | `unsupported` | `blocked` | `none` | `none` | `windows-hosts-not-yet-reviewed` |

## Interpretation

- `supported` plus `launch=allowed` means the reviewed launch path is supported
  for that exact host and target combination.
- `validation-host-only` means the row is trusted only as a validation bridge.
  It bounds support claims and diagnostics, but Workcell launch remains blocked
  on that host.
- `unsupported` means the combination is outside the reviewed support boundary
  and launch must fail closed.
