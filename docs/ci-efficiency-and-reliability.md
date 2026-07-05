# CI Efficiency and Reliability

This documents the B8 program: reducing pull-request CI wall-clock without
weakening release-time assurance, plus the reliability and visibility tooling
that keeps CI health observable. It complements
[github-workflows.md](github-workflows.md), which is the authoritative workflow
inventory.

The prime directive throughout: **no release-gating assurance is lost.** Every
*deterministic* check moved off the PR-blocking path still runs before release
(on the post-merge `main` push and/or re-verified in `release.yml` preflight);
each change below names that mechanism. The one exception is **active fuzzing**,
which is a nondeterministic discovery hunt, not a release gate — its release-time
assurance is the deterministic seed-corpus regression (still run on every PR and
at preflight via `go test`), while the extended active budget runs on the
scheduled `fuzz.yml` lane and is intentionally *not* a `release.yml` dependency.

## PR wall-clock reductions

### Active fuzzing moved off the PR-blocking validate lane

The `Validate repository` lane in [`ci.yml`](../.github/workflows/ci.yml) used
to spend a nondeterministic active-fuzzing time budget on every PR (and every
release preflight) via `scripts/validate-repo.sh`. Active fuzzing is a
time-bounded *discovery hunt*, not a deterministic gate: it hit a spurious
per-input timeout (0 execs/sec at ~16s, no reproducer) that failed a PR while
`main` stayed green, forcing a full validate-lane re-run.

What changed:

- The deterministic fuzz **seed corpora still run on every PR** via
  `go test ./...`. That is the release-relevant regression assurance and it is
  unchanged.
- The extended **active-fuzzing budget runs only on the scheduled fuzz lane**
  ([`fuzz.yml`](../.github/workflows/fuzz.yml) →
  `scripts/ci/job-fuzz.sh` → `scripts/ci/run-fuzz-in-validator.sh`), which
  mutates the same seeds for a longer budget on a weekly cadence and on
  `workflow_dispatch`.

Coverage preservation: the scheduled Go fuzz lane exercises `FuzzParse`
(`internal/tomlsubset`), `FuzzParseSSHDirective` and
`FuzzIsAllowedSystemSymlink` (`internal/injection`), and the
`internal/metadatautil` parser targets. The PR path previously fuzzed
`FuzzParse` and `FuzzParseSSHDirective` (both covered by the scheduled lane) plus
a stale reference to `FuzzIsSafeRelativeSymlinkTarget`, a target that no longer
exists and silently no-op'd. So **no active-fuzz coverage is lost** at
nightly/release by this move; the seed-corpus regression that does gate release
still runs everywhere `go test` runs.

### Heavy reproducible-build rebuilds gated behind `approved-heavy-ci`

`ci.yml`'s `reproducible-build-platform` matrix runs two ~45-minute no-cache
double-builds (amd64 + native arm64). It was the only heavy `ci.yml` lane not
behind the `approved-heavy-ci` label gate that install verification, CodeQL, and
mutation already use, so an unreviewed fork PR could trigger that compute on
demand.

What changed: the fork-PR trigger is now gated behind `approved-heavy-ci`
(identical `if:` to install verification). On an unlabeled PR the per-platform
matrix is skipped and the aggregate `Reproducible build` required context passes
on the `skipped` result; on a push to `main`, a labeled PR, or dispatch it fails
unless the matrix succeeds.

Release-time assurance preservation — reproducibility is re-verified **twice**
independently of the unlabeled-PR path:

| Mechanism | Where | Enforces |
|---|---|---|
| Post-merge `main` push | `ci.yml` `reproducible-build-platform` (runs on every push to `main`) | Every merged commit's runtime image is reproducibility-verified |
| Release preflight | `release.yml` `preflight-amd64-repro` / `preflight-arm64-repro` (native, no QEMU) | Publish byte-compares the released image against these verified per-platform digests; a mismatch fails the release |

Additionally, `release.yml`'s preflight gates on the tagged commit's `main`
check-runs being green (the "Require main checks green for tagged commit" step),
so the post-merge reproducibility run is enforced at release time as well.

## Retry policy for transient network steps

Transient registry/network failures were failing otherwise-green lanes.
[`scripts/retry.sh`](../scripts/retry.sh) is a bounded-retry wrapper
(`WORKCELL_RETRY_ATTEMPTS`, default 3; doubling `WORKCELL_RETRY_DELAY` backoff,
default 5s) applied to idempotent fetches:

- `fuzz.yml`: rustup nightly toolchain install and `cargo install cargo-fuzz`.
- `security.yml` / `release.yml`: `curl --retry`/`--retry-connrefused` on the
  actionlint and zizmor downloads. Checksum verification still runs after the
  download, so retry only adds robustness and cannot weaken integrity.

Deterministic steps (linters, tests, `--locked` lock checks) are deliberately
**not** wrapped: retrying them wastes CI time and hides nothing.

## Flaky-test tracking

Convention: when a test or lane fails nondeterministically, open a GitHub issue
labeled **`flaky-test`** describing the target and the observed nondeterminism.
That label is the human-curated tracked list.

The [`ci-insights.yml`](../.github/workflows/ci-insights.yml) `Flaky-test report`
job runs weekly (and on demand) and writes a Markdown report to its run's **job
summary** combining two signals:

1. Open `flaky-test`-labeled issues (the tracked list).
2. Workflow runs in the lookback window (default 7 days,
   `WORKCELL_CI_INSIGHTS_DAYS`) that needed a re-run (`run_attempt > 1`) or
   concluded in failure — an empirical flake signal from CI history.

The report is generated by [`scripts/ci/flaky-report.sh`](../scripts/ci/flaky-report.sh)
and is read-only (`actions:read` + issues read). Re-runs and failures are
*candidates*, not confirmed flakes; triage them and, if nondeterministic, file a
`flaky-test` issue so they persist in signal 1 until fixed.

## CI cost visibility

The `ci-insights.yml` `CI cost report` job runs on the same weekly cadence and
writes a Markdown table to its job summary aggregating per-workflow wall-clock
over the lookback window (runs, total wall-clock, average per run), sorted by
total. It is generated by
[`scripts/ci/cost-report.sh`](../scripts/ci/cost-report.sh) and is read-only
(`actions:read`). Wall-clock includes queue time because that is the latency a
change actually waits on.

## Before/after PR timing estimate and methodology

Full CI cannot be run locally (hosted runners, GHCR, macOS runners), so these
are estimates derived from the configured job timeouts and the fuzz-time
constants in the scripts, not measured end-to-end runs. The `CI cost report`
above provides the empirical before/after once it accumulates history across the
change.

Methodology: read the serial critical path for an **unlabeled** PR (the common
case — most PRs are not labeled `approved-heavy-ci`) as the max over required
contexts, and account for the removed work.

- **Active fuzzing:** the validate lane previously ran two effective active-fuzz
  targets at 15s each (`FuzzParse`, `FuzzParseSSHDirective`) plus one no-op,
  i.e. ~30s of serial fuzz time on the validate critical path per PR. Removing
  it drops that ~30s and — more importantly — removes a nondeterministic timeout
  failure mode whose cost was a full validate-lane re-run (up to the 60-minute
  lane budget) on a spurious failure.
- **Reproducible build:** previously the `Reproducible build` required context
  waited on a ~45-minute per-platform matrix on **every** PR (the two platforms
  run in parallel, so ~45 minutes, not 90). On an unlabeled PR that context now
  resolves in under a minute (skipped matrix → aggregator passes), removing a
  ~45-minute-budget required gate from the unlabeled-PR critical path.

Net expected effect for an unlabeled PR: the all-green critical path is no longer
gated by the ~45-minute reproducible-build matrix, and the validate lane sheds
~30s of flaky active-fuzz plus its re-run risk. PRs that genuinely need the heavy
lanes still get them by applying `approved-heavy-ci`. Exact minutes depend on
real per-run durations, which the cost report will quantify.
