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
| provider auth injected through the reviewed policy model | tested for direct staged inputs plus Codex host-auth reuse; the Claude macOS resolver remains fail-closed scaffold only | container smoke, auth-status tests, policy tests, provider-specific helpers |
| host-side policy inspection and credential explainability | tested | `tests/scenarios/shared/test-policy-commands.sh`, `internal/authpolicy/manage_test.go` |
| host-side signed `publish-pr` handoff | tested | shared scenario tests and invariant checks |
| repo control-plane masking and provider-home re-seeding | tested | invariants and smoke |
| repo-mounted validator and release-helper runs stay nonroot with explicit caller identity and isolated writable state | tested | CI, release preflight, `scripts/pre-merge.sh`, and invariant checks |
| prompt-autonomy downgrade labeling | tested for Codex, partial elsewhere | invariants plus provider-specific coverage |
| host-side session inventory, detached session control, delete, log and timeline views, clean-base diff, and audit export | tested | `tests/scenarios/shared/test-session-commands.sh`, `internal/hostutil/sessions_test.go`, `cmd/workcell-hostutil/main_test.go` |
| detached-session isolated workspace preflight and direct-workspace remediation | tested | `tests/scenarios/shared/test-session-commands.sh` |
| opt-in persistent cache plane (`--cache-profile standard`) | tested | `tests/scenarios/shared/test-assurance-dry-run.sh`, `scripts/verify-invariants.sh` |
| bundle installer plus uninstall helper on the supported GitHub-hosted macOS matrix | tested | `scripts/verify-invariants.sh`, CI, tagged release install verification |
| Homebrew formula install plus uninstall on the supported GitHub-hosted macOS matrix | tested | CI and tagged release install verification |
| release-bundle reproducibility | tested | `scripts/verify-release-bundle.sh`, tagged release preflight |
| runtime-image reproducibility | tested | `scripts/verify-reproducible-build.sh`, CI, tagged release preflight |
| Sigstore signing and SBOM publication | tested | successful tagged release workflow |
| GitHub attestations on tagged releases | tested at release time | `release.yml` publish job |
| full macOS Colima boundary proof | gap | local certification lane exists, but it is still not repo-required proof |
| exhaustive live-provider auth UX across all providers | gap | manual provider-e2e path exists, but automation is partial |

## Notes

The most heavily tested paths are the repo-required secretless runtime
boundary, explicit nonroot repo validation and release preflight,
reproducibility, signed publication, and the host-side operator plane. The
most mature host-side operator surfaces are auth/policy inspection plus
session inventory, detached control, delete, timeline, export, and
isolated-workspace remediation. The biggest remaining gaps are local macOS
boundary proof and deeper end-to-end live-provider coverage.
