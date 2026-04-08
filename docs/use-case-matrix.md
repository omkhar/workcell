# Use Case Coverage Matrix

This matrix summarizes how well the current repo validates the most important
Workcell workflows.

## Coverage scale

- `tested`: covered by repo validation, invariants, smoke, or scenario tests
- `doc-only`: documented and intentionally supported, but not deeply automated
- `gap`: recognized coverage gap tracked in `docs/scenario-gaps.md`

## Matrix

| Use case | Status | Primary evidence |
|---|---|---|
| secretless provider launch on the managed path | tested | `scripts/container-smoke.sh`, `scripts/verify-invariants.sh` |
| provider auth injected through the reviewed policy model | tested | container smoke, auth-status tests, provider-specific helpers |
| host-side signed `publish-pr` handoff | tested | shared scenario tests and invariant checks |
| repo control-plane masking and provider-home re-seeding | tested | invariants and smoke |
| prompt-autonomy downgrade labeling | tested for Codex, partial elsewhere | invariants plus provider-specific coverage |
| host-side session inventory and audit export | tested | `tests/scenarios/shared/test-session-commands.sh`, `internal/hostutil/sessions_test.go` |
| release-bundle reproducibility | tested | `scripts/verify-release-bundle.sh`, tagged release preflight |
| runtime-image reproducibility | tested | `scripts/verify-reproducible-build.sh`, CI, tagged release preflight |
| Sigstore signing and SBOM publication | tested | successful tagged release workflow |
| GitHub attestations on tagged releases | tested at release time | `release.yml` publish job |
| full macOS Colima boundary proof | gap | local or self-hosted exercise only |
| exhaustive live-provider auth UX across all providers | gap | manual provider-e2e path exists, but automation is partial |

## Notes

The most heavily tested paths are the secretless runtime boundary, release
preflight, reproducibility, and signed publication. The biggest remaining gaps
are local macOS boundary proof and deeper end-to-end live-provider coverage.
