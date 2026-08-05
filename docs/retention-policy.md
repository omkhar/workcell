# CI and release artifact retention policy

GitHub Actions keeps each workflow artifact for a fixed time.
This page lists those times and their purpose.

[`policy/retention-policy.json`](../policy/retention-policy.json) is the source of truth.
`workcell-citools check-retention-policy` checks the policy against all workflow files.

The check requires an explicit `retention-days` value for each upload.
It also requires the exact policy value for each artifact name.
The local `pr-parity` gate and the `Security` workflow run this check.

## Retention by artifact

| Workflow | Artifact | Days |
| --- | --- | ---: |
| `bench.yml` | `exec-guard-bench-results` | 14 |
| `ci.yml` | `workcell-ci-install-candidate` | 7 |
| `fuzz.yml` | `fuzz-reproducers` | 14 |
| `fuzz.yml` | `rust-fuzz-reproducers` | 14 |
| `fuzz.yml` | `rust-fuzz-lockfile` | 14 |
| `release.yml` | `workcell-release-preflight` | 90 |
| `release.yml` | `workcell-release-install-candidate` | 90 |
| `release.yml` | `workcell-release-artifacts` | 7 |
| `security.yml` | `zizmor-sarif` | 5 |
| `scorecard.yml` | `scorecard-sarif` | 5 |
| `upstream-refresh.yml` | `upstream-refresh-candidate` | 7 |

## Rationale

### Release artifacts

`workcell-release-preflight` contains four preflight manifests.
The manifests bind build inputs, the control plane, the source bundle, and runtime images.

`workcell-release-install-candidate` contains the source bundle and Homebrew formula used by the macOS install jobs.
Workcell keeps both artifacts for 90 days to support release review and incident checks.

`workcell-release-artifacts` contains the sealed 18-asset workflow publication set.
The final job publishes the same 18 files as immutable GitHub release assets.
GitHub adds its own source-code and release-attestation entries to the release page.
Workcell keeps the workflow copy for seven days.

### CI install artifact

`workcell-ci-install-candidate` contains a temporary bundle, formula, and checksum data.
The Apple Silicon install jobs use it during one CI run.
Workcell keeps it for seven days.

### Benchmark artifact

`exec-guard-bench-results` contains the benchmark report.
Workcell keeps it for 14 days, which covers the weekly schedule.

The reviewed benchmark page is the durable baseline.
See [syscall shim benchmarks](syscall-shim-benchmarks.md).

### Fuzz artifacts

The Go and Rust reproducer artifacts exist only after a crash.
They contain inputs that reproduce that crash.

`rust-fuzz-lockfile` contains the resolved fuzz lock file.
Workcell keeps all three artifacts for 14 days.

First, examine each crash in a private workspace.
Remove credentials, personal data, and unrelated private content.
Follow [`SECURITY.md`](../SECURITY.md#reporting) if the crash can affect security.
Do not commit a sensitive reproducer before the fix or approved disclosure.

See [fuzzing](fuzzing.md) for the triage process.

### Security artifacts

`zizmor-sarif` is a short-term copy of the workflow audit result.
The `zizmor` job fails on each finding.
GitHub code scanning does not receive this SARIF file.

`scorecard-sarif` is a short-term copy of the Scorecard result.
The workflow also sends that result to GitHub code scanning.

Workcell keeps both convenience copies for five days.

### Upstream refresh artifact

`upstream-refresh-candidate` contains a patch, a diffstat, and metadata.
It is advisory input for a host-side refresh PR.
It is not release provenance.

Workcell keeps the candidate for seven days.
The scheduled workflow can publish a new candidate.
The earlier artifact remains until GitHub deletes it or its retention time ends.

## Verify a release after workflow artifacts expire

Workflow artifacts are not the durable release record.
Use these sources after a workflow artifact expires:

- Download the required release-data assets and their matching Sigstore bundles.
- Use Cosign and the matching bundle to verify each release-data asset against
  the pinned release workflow identity.
- Use the signed `SHA256SUMS` file to verify only the eight other release-data
  assets that it names.
- Do not check `SHA256SUMS` or a `.sigstore.json` file against that list.
- Use `gh attestation verify` for a subject that has a GitHub attestation.
- Use `cosign verify` for the runtime image in GHCR.

GitHub stores artifact attestations separately from workflow artifacts.
GHCR stores the image signature and image attestations with the image.
The public Rekor log records the Cosign signature entries.

See [provenance](provenance.md) for exact verification commands.
See [GitHub workflow design](github-workflows.md) for the workflow controls.
