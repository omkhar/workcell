# Host Expansion Readiness

This page records the Phase 12 host-readiness gate. It also defines the Phase
13 host promotion gate. It does not change operator support.

Use
[`policy/host-support-matrix.tsv`](../policy/host-support-matrix.tsv) for each
support decision. Use [Support Tiers and Status Terms](support-tiers.md) for
the permitted values.

## Support Fields

The host-support matrix keeps these decisions separate:

- `target_assurance_class` defines the assurance class.
- `status` defines the support status.
- `launch` controls operator launch.
- `evidence` defines the required evidence.
- `validation_lane` names a validation path when one exists.

The word `candidate` is a phase label. It is not a matrix value. Read the
complete matrix row before you make a support decision.

## Phase 12 Terms

Phase 12 uses these planning terms. The terms do not replace matrix values.

| Term | Meaning |
|---|---|
| `strict` | The target must keep the dedicated VM and container boundary. |
| `compat` | The target has lower assurance and needs explicit diagnostics and rollback. |
| `preview` | The target has limited evaluation and no operator support claim. |
| `certification candidate` | The implementation needs live certification. |
| `experimental` | The work is an investigation or prototype. |
| `unsupported` | Workcell blocks operator launch by default. |

Linux amd64, Linux arm64, Raspberry Pi, Windows WSL2, and native Windows are
separate host tracks. Each track needs its own package, runtime, boundary, and
live-host evidence.

## Phase 13 Scope

Phase 13 will evaluate one Linux amd64 `local_compat` combination. The phase
must select one exact distribution, version, runtime, and `target_provider`.
The repository has not made this selection.

The Linux amd64 Docker Desktop row stays `unsupported`, `blocked`, and
`evidence=none`. A different runtime requires a new target-provider
implementation and an exact matrix row.

The Phase 12 gate also applies to a future Windows promotion. All Windows rows
stay `unsupported`, `blocked`, and `evidence=none`.

Phase 13 does not change Linux `strict` support. Linux `strict` support needs
an equivalent boundary with a dedicated VM and container.

## Promotion Gate

One promotion change must include all these items:

- Replace the applicable wildcard row with the exact host and target row.
- Add fail-closed launch behavior and clear diagnostics.
- Add install, update, uninstall, rollback, and support-bundle procedures.
- Add deterministic repository tests for the selected row.
- Add negative tests for all unsupported combinations.
- Complete live certification on a real operator host.
- Update operator, support, and validation documents with the same claim.

The live certification must test the same distribution, version, runtime, and
provider that the matrix row names. The rollback must disable or remove the new
path. Regression tests must protect the current supported paths.

## Fail-Closed Behavior

Workcell must find the row that matches the detected host and selected target.
It must block launch unless that row has `status=supported` and
`launch=allowed`. Workcell must not select a different target automatically.

`workcell --doctor` and `workcell --inspect` must show the detected host, the
selected target, and the complete support decision. If `launch=blocked`, both
commands must give the reason. `workcell --doctor` must give the recommended
action.

Use [Diagnostics and the Support Matrix](diagnostics-and-support-matrix.md) for
the exact diagnostic fields.

## Review Gate

Reviewers must reject a support claim that exceeds its evidence. Repository
tests do not replace live certification. A validation host is not an operator
host.

Use [Runtime Target Expansion](runtime-target-expansion-plan.md) for the
program rules. Use [Runtime Target Phase Record](runtime-target-phase-plan.md)
for the phase sequence.
