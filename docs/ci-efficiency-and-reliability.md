# CI Efficiency and Reliability

This page records the shipped B8 changes. Use
[GitHub Workflows](github-workflows.md) for the current workflow inventory.

## Assurance Rule

A change to pull-request CI must preserve each deterministic release gate.
Scheduled fuzzing is a discovery activity. It is not a release gate. The seed
corpus is deterministic and stays in `go test ./...`.

## Pull Request Changes

### Active fuzzing

The pull-request validation path runs the deterministic fuzz seed corpus through
`go test ./...`. It does not run a time-bounded active fuzz campaign.

The scheduled `fuzz.yml` workflow runs the active Go and Rust fuzz targets. An
operator can also start this workflow with `workflow_dispatch`. This design
removes a nondeterministic time budget from the pull-request critical path and
keeps the seed regression gate.

Use [Fuzzing](fuzzing.md) for the current target list and artifact behavior.

### Reproducible builds

The native amd64 and arm64 reproducible-build matrix is a heavy lane. A pull
request needs the `approved-heavy-ci` label to start this matrix. The aggregate
required check accepts the documented skip result for an unlabeled pull
request.

The matrix runs for each push to `main`. The release workflow also runs native
reproducibility preflight jobs and compares the release image with the verified
digests. Thus, an unlabeled fork pull request does not remove the release gate.

## Network Retry Policy

[`scripts/retry.sh`](../scripts/retry.sh) supplies a bounded retry for
idempotent fetch operations. The default is three attempts. The default initial
delay is five seconds, and the delay doubles after each failure.

Workflows use retries for operations such as a toolchain install or a verified
tool download. Checksum verification still follows the downloaded tools that
use recorded checksums.

Do not add retries to deterministic tests, linters, or locked dependency
checks. A retry can hide a repeatable defect and add delay.

## Flaky Test Record

Use the `flaky-test` issue label for a confirmed nondeterministic failure. The
issue must name the test or lane. The issue must include the observed behavior.

The `ci-insights.yml` workflow creates a weekly flaky-test report. It combines:

1. Open issues with the `flaky-test` label.
2. Workflow runs in the selected period that failed or had more than one run
   attempt.

The second signal supplies candidates. It does not prove that a failure is
flaky. [`scripts/ci/flaky-report.sh`](../scripts/ci/flaky-report.sh) creates the
read-only job summary.

## CI Cost Report

The same workflow creates a weekly cost report. The report shows run count,
total wall-clock time, and average wall-clock time for each workflow. Queue time
is part of wall-clock time. [`scripts/ci/cost-report.sh`](../scripts/ci/cost-report.sh)
creates the read-only job summary.

## Recorded Timing Estimate

The 2026-07-05 B8 estimate followed the
[fuzz change](https://github.com/omkhar/workcell/commit/924c07a37f268afdbf55715ee04952ea6f0edc0b)
and the
[reproducible-build change](https://github.com/omkhar/workcell/commit/845b1b0d2c77574afbedb2202799971a9d912286).
It used configuration and timeout values. A local host cannot run all hosted CI
jobs. It did not claim an end-to-end measured result.

The expected result for an unlabeled pull request was:

- The change removes approximately 30 seconds of active fuzz time from the
  validation path.
- The change removes the approximately 45-minute reproducible-build matrix from
  the required pull-request critical path.
- The scheduled and on-demand workflow keeps active fuzz campaigns.
- `go test ./...` keeps the fuzz seed tests.
- Pushes to `main` and the release workflow keep reproducible builds.

Use the CI cost report for current measured history. Do not present the recorded
estimate as a current service-level objective.
