# Diagnostics and the support matrix

`workcell --doctor` and `workcell --inspect` emit host and
`support_matrix_*` key=value lines derived from
[`policy/host-support-matrix.tsv`](../policy/host-support-matrix.tsv). This
page explains each field and how an operator should act on the resolved row.

## Emitted fields

<!-- support-matrix-fields:begin -->
| Field | Meaning |
|---|---|
| `host_os` | Detected host operating system (for example `macos`, `linux`). |
| `host_arch` | Detected host CPU architecture (for example `arm64`, `amd64`). |
| `host_distro` | Detected Linux distribution ID, or `none` off Linux. |
| `host_distro_version` | Detected Linux distribution version, or `none` off Linux. |
| `support_matrix_status` | Row status for the resolved host and target: `supported`, `preview-only`, `validation-host-only`, or `unsupported`. |
| `support_matrix_launch` | Whether operator launch is `allowed` or `blocked` for the resolved combination. |
| `support_matrix_evidence` | Evidence tier backing the row: `certification-only`, `repo-required`, `manual-only`, or `none`. |
| `support_matrix_validation_lane` | Named validation lane for the row, or `none`. |
| `support_matrix_reason` | Human-readable reason string for the resolved status. |
<!-- support-matrix-fields:end -->

The matrix `target_kind`, `target_provider`, and `target_assurance_class`
columns are not emitted as `--doctor`/`--inspect` fields; read them from the
matched row in
[`policy/host-support-matrix.tsv`](../policy/host-support-matrix.tsv).

## Triage decision tree

1. If `support_matrix_launch=allowed`:
   - With `support_matrix_status=supported`, proceed only for the resolved row and only with the evidence its `support_matrix_evidence` names (for current supported rows this is `certification-only`) as recorded in `policy/host-support-matrix.tsv`.
   - With any other status, treat the row as inconsistent for operator launch, stop, and follow `support_matrix_reason`.
2. If `support_matrix_launch=blocked`:
   - With `support_matrix_status=preview-only`, treat the row as preview or certification-only. It is not an operator launch host.
   - With `support_matrix_status=validation-host-only`, use the host only for the named validation lane, such as `trusted-linux-amd64-validator`.
   - With `support_matrix_status=unsupported`, do not launch. Read `support_matrix_reason` for the unsupported boundary.
   - With any other status, follow the blocked launch decision first and do not treat the row as supported.

## No implicit support claim

No tier, assurance class, or status confers a support claim on its own.
Supported launch paths still require the live certification evidence recorded in
[`policy/host-support-matrix.tsv`](../policy/host-support-matrix.tsv).

For the vocabulary behind each value, see
[`docs/support-tiers.md`](support-tiers.md). The canonical source for the
resolved matrix row is
[`policy/host-support-matrix.tsv`](../policy/host-support-matrix.tsv).
