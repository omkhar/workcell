# Use Case Coverage Matrix

This matrix shows the current validation level for important Workcell
workflows.

## Status terms

- `tested`: Repository validation, invariants, smoke tests, or scenarios cover
  the use case.
- `partial`: Some deterministic evidence exists, but live or provider-specific
  coverage is incomplete.
- `gap`: The implementation exists, but an important coverage gap remains.
- `planned gap`: The roadmap names the work, but the implementation does not
  exist.

## Matrix

| Use case | Status | Main evidence |
|---|---|---|
| Secretless provider launch on the managed path | `tested` | `scripts/container-smoke.sh`, `scripts/verify-invariants.sh` |
| Credential injection through the reviewed policy | `partial` | `tests/scenarios/shared/test-auth-status.sh`, `internal/authpolicy/manage_test.go`; the Claude macOS resolver is a fail-closed scaffold |
| Host policy inspection and credential explanations | `tested` | `tests/scenarios/shared/test-policy-commands.sh`, `internal/authpolicy/manage_test.go` |
| Signed host-side `publish-pr` handoff | `tested` | `tests/scenarios/shared/test-publish-pr-dry-run.sh`, `scripts/verify-invariants.sh` |
| Repository control-plane masks and provider-home seeding | `tested` | `tests/scenarios/shared/test-home-control-plane-manifest.sh`, `scripts/container-smoke.sh`, `scripts/verify-invariants.sh` |
| Nonroot repository validation and release helpers | `tested` | `scripts/ci/job-validate.sh`, `scripts/pre-merge.sh`, `scripts/verify-invariants.sh` |
| Prompt-autonomy downgrade labels | `partial` | `scripts/verify-invariants.sh`; other providers have less coverage |
| Session inventory, control, delete, logs, timeline, diff, and export | `tested` | `tests/scenarios/shared/test-session-commands.sh`, `internal/host/sessions/sessions_test.go` |
| Isolated-session workspace preflight and direct-workspace remediation | `tested` | `tests/scenarios/shared/test-session-commands.sh` |
| Persistent non-secret cache with `--cache-profile standard` | `tested` | `tests/scenarios/shared/test-assurance-dry-run.sh`, `scripts/verify-invariants.sh` |
| Bundle install and uninstall on the hosted macOS matrix | `tested` | `tests/scenarios/shared/test-install-lifecycle.sh`, `internal/testkit/install_release_e2e_test.go`, `.github/workflows/ci.yml` `Install verification` jobs |
| Homebrew install and uninstall on the hosted macOS matrix | `tested` | `.github/workflows/ci.yml` and `.github/workflows/release.yml` install-verification jobs |
| Release-bundle reproducibility | `tested` | `scripts/verify-release-bundle.sh`, `scripts/ci/job-validate.sh --profile release-preflight` |
| Runtime-image reproducibility | `tested` | `scripts/verify-reproducible-build.sh`, `.github/workflows/ci.yml` `Reproducible build` jobs |
| Sigstore signatures, SBOMs, and GitHub attestations | `tested` | Successful v1.0.2 `Release` workflow |
| Full local macOS and Colima boundary proof | `gap` | A local certification lane exists. It is not repo-required proof. |
| Live authentication for every provider path | `gap` | Operators run `scripts/provider-e2e.sh` manually, and coverage is incomplete. |
| Copilot deterministic provider path | `tested` | `tests/scenarios/shared/test-copilot-session-dry-run.sh`, `scripts/container-smoke.sh`, `scripts/verify-upstream-copilot-release.sh` |
| Copilot live staged-token path | `partial` | Work that promotes or materially alters the Copilot support claim requires live `copilot -p` certification. |
| Antigravity provider path | `planned gap` | No supported adapter, authentication input, quickstart, scenario evidence, or certification exists. |

The core secretless boundary, signed publication, reproducibility, release
provenance, and host operator commands have the most test evidence. See
[scenario-gaps.md](scenario-gaps.md) for the remaining work.
