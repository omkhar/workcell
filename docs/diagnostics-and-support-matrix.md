# Diagnostics and the Support Matrix

`workcell --doctor` and `workcell --inspect` print host and
`support_matrix_*` fields. Workcell gets these fields from
[`policy/host-support-matrix.tsv`](../policy/host-support-matrix.tsv).

## Fields

<!-- support-matrix-fields:begin -->
| Field | Meaning |
|---|---|
| `host_os` | Detected host operating system, such as `macos` or `linux`. |
| `host_arch` | Detected host CPU architecture, such as `arm64` or `amd64`. |
| `host_distro` | Detected Linux distribution ID. The value is `none` on other systems. |
| `host_distro_version` | Detected Linux distribution version. The value is `none` on other systems. |
| `support_matrix_status` | Status of the matching row: `supported`, `preview-only`, `validation-host-only`, or `unsupported`. |
| `support_matrix_launch` | Launch decision for the matching row: `allowed` or `blocked`. |
| `support_matrix_evidence` | Evidence class for the row: `certification-only`, `repo-required`, `manual-only`, or `none`. |
| `support_matrix_validation_lane` | Name of the validation lane, or `none`. |
| `support_matrix_reason` | Reason for the status and launch decision. |
<!-- support-matrix-fields:end -->

The commands also print launch-state fields. These fields include
`target_kind`, `target_provider`, and `target_assurance_class`. Use them to
confirm the selected target.

## Operator decision

First, read `support_matrix_launch`.

- If the value is `allowed`, continue only when
  `support_matrix_status=supported`. Use only the evidence that
  `support_matrix_evidence` specifies.
- If the value is `blocked`, do not start an operator session.

If `support_matrix_launch=blocked`, read `support_matrix_status`:

- `preview-only`: Use the row only for its documented preview or certification
  work.
- `validation-host-only`: Use the host only for
  `support_matrix_validation_lane`.
- `unsupported`: Do not launch. Read `support_matrix_reason`.
- `supported` or any unexpected value: Stop. The blocked decision has
  priority.

No field gives support by itself. A supported launch requires one matching row
with `status=supported`, `launch=allowed`, and the required evidence.

See [support-tiers.md](support-tiers.md) for all values. For a possible runtime
boundary breach, use [incident-response.md](incident-response.md).
