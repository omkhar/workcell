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

## Shared Validator Image

The `Validate repository` job builds the validator image once.
It saves the image as a zstd archive and uploads it as the `workcell-validator-image` artifact.
It also records the image ID as a job output.

Each `Hostile environment` job downloads the archive and runs `docker load`.
The job compares the loaded image ID with the recorded ID.
A missing archive or a different ID fails the job.
The job never rebuilds the image.

An earlier version built the image in all four hostile jobs at the same time.
Two of the four jobs failed with `curl` exit 22 when they fetched the pinned Debian snapshot.
The workflow then used `max-parallel: 1`, and the four jobs ran one after the other.
Now the four jobs run in parallel, because none of them builds the image.
The job names that branch protection requires did not change.

## Parallel Host Invariants

The `Host launcher invariants` job runs `scripts/verify-invariants.sh`.
It runs in parallel with `Validate repository` and needs only `Pull request shape`.
The hosted workflow sets `WORKCELL_CI_VALIDATE_SKIP_HOST_INVARIANTS=1` for `Validate repository`.
Local parity does not set it, so `scripts/ci/job-validate.sh` still runs the script locally.
The job name is a required status check in `policy/github-hosted-controls.toml`.
An administrator must add `Host launcher invariants` to the repository ruleset before this change merges.

## Validator Caches

`Validate repository` and the `tmpdir` and `workspace` hostile jobs restore one cache directory with `actions/cache/restore`.
`scripts/ci/run-validate-in-validator.sh` mounts it over the validator cache path.
The directory holds the Go build cache, the Go module cache and the cargo target directory.
The `root` and `uidmap` jobs do not mount it, because their uid cannot use the host directory.

The key holds the hash of `go.mod`, `go.sum`, both `Cargo.lock` files and `tools/validator/Dockerfile`.
The Dockerfile pins the Go and Rust versions.
Only a push to `main` saves the cache, with `actions/cache/save`.
A pull request restores the cache and never saves, so it cannot write into the scope that `main` uses.

Use the CI cost report for current measured history. Do not present the recorded
estimate as a current service-level objective.
