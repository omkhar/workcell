# Session-start latency baselines

Workcell starts an isolated session by resolving the runtime image, booting the
container runtime (Colima today, Apple `container` under evaluation in C1), and
completing the supervisor handshake before an agent can run. **C2** is the
program that measures that start latency, drives it down with cached images and
an optional kept-warm lane, and publishes reproducible numbers. This page
records the methodology and the rerun steps; it is the C2 sibling of
[syscall-shim-benchmarks.md](syscall-shim-benchmarks.md) (C5).

The numbers are produced on a host with a live container runtime, not in the
PR-blocking CI lanes: a real session start needs a booted VM, so the cost only
exists where that runtime is available. The results tables below are
**placeholders pending a live capture** — see
[Filling in the numbers](#filling-in-the-numbers). Do not treat the template
values as measured.

## What is measured

A **sample** is one full session start: the wall-clock time from invoking the
session-start command to the point the session is ready. The harness times three
modes that span the latency shapes C2 targets:

| Mode | Runtime state before the sample | What it isolates |
|---|---|---|
| `cold` | image not cached, no kept-warm session | worst-case first start (image resolve + full boot) |
| `warm` | image cached and a kept-warm session available | best-case start off the kept-warm lane |
| `cache-hit` | image cached, no kept-warm session | the image-cache win alone, without the warm lane |

The `cold` vs `warm` delta is the headline C2 number: how much the cache plus the
kept-warm lane save off a first start. The `cache-hit` mode separates the
image-cache contribution from the kept-warm-lane contribution so each optimization
can be credited independently.

## Methodology

The harness (`scripts/bench/startup-bench.sh`) times one mode: it runs the
session-start command `WORKCELL_STARTUP_WARMUP` times (discarded) to settle
first-touch page-cache and loader costs, then `WORKCELL_STARTUP_ITERATIONS`
measured times, and reports the sample distribution. The stats conventions match
the C5 exec-guard harness exactly, so the two pages' numbers are directly
comparable:

- **median** — `sorted[floor(n/2)]`, robust to scheduler and I/O outliers; the
  headline figure.
- **p90** — `sorted[floor(n*9/10)]` (clamped to the last sample), the tail a user
  actually feels on a slow start.
- **mean / stddev** — population mean and standard deviation, reported alongside
  the median so a skewed distribution is visible.
- **min / max** — the observed range.

The driver (`scripts/bench/run-startup-bench.sh`) establishes each mode's runtime
state through a per-mode prep hook (`WORKCELL_STARTUP_COLD_PREP` /
`WORKCELL_STARTUP_CACHE_HIT_PREP` / `WORKCELL_STARTUP_WARM_PREP` — e.g. evicting
the cached image and stopping the kept-warm session for `cold`, pre-pulling the
image but leaving the warm lane down for `cache-hit`, or pre-pulling and priming
the warm lane for `warm`), then times the configured `WORKCELL_STARTUP_CMD` for
that mode. The whole measurement is repeated for `WORKCELL_STARTUP_RUNS` passes.

For `cold` the driver re-runs `WORKCELL_STARTUP_COLD_PREP` before **every**
measured sample and times each start on its own with warmup `0`, then aggregates
the per-sample timings through the same stats core. A single session start warms
the cache the next start would otherwise hit, so evicting only once before the
pass would leave just the first sample genuinely cold; the per-sample re-prep
keeps every `cold` sample a true first start. The cold-prep hook must therefore
be **repeatable** — it runs once per measured sample, not once per pass. The
`warm` and `cache-hit` modes legitimately share one prep for the whole pass and
keep the configured `WORKCELL_STARTUP_WARMUP` to settle first-touch page-cache
and loader costs before measuring.

`WORKCELL_STARTUP_CMD` is parsed with shell quoting into the target argv, so an
argument containing spaces keeps its boundary. Quote such arguments exactly as
you would on a shell command line — e.g.
`WORKCELL_STARTUP_CMD="./scripts/workcell --workspace '/path/with space'"` reaches
the target as three argv elements, not four word-split tokens.

### The cross-run stability gate

Reproducibility is the C2 acceptance bar, so the driver does not just print the
per-run numbers — it enforces them. After all runs it computes, for each mode,
the spread of the run-to-run **median** as a percentage of the smallest run's
median, and it **fails** (non-zero exit) if any mode exceeds
`WORKCELL_STARTUP_STABILITY_PCT` (default 15%). A published number is only
trustworthy if it repeats; this gate is the evidence that it does. It mirrors the
C5 cross-run stability check, made enforcing.

### Runner caveats

- Numbers are **relative** to the measuring host's hardware and runtime backend;
  treat the cold-vs-warm delta, not the absolute medians, as the portable signal,
  and re-measure on the target host for absolute figures.
- The `cold` mode depends on the prep hook genuinely evicting cached state. If the
  hook is a no-op the `cold` and `warm` numbers converge — that is a
  misconfiguration, not a fast cold start.
- The cold-prep hook runs once per measured `cold` sample (not once per pass), so
  it must be **repeatable and idempotent** — every invocation has to leave the
  same fully-evicted, no-warm-lane state. A hook that only evicts on its first
  call will silently measure cache-hits for the remaining samples.
- Session start includes VM boot, which is noisier than a userspace microbenchmark;
  expect a wider stddev than the C5 exec-guard numbers and keep the stability
  threshold accordingly.

## Results

**Status: numbers pending live capture.** The tables below are templates. There
is a sustained upstream mirror outage blocking the container image build at the
time of writing, so no live runtime was available to capture real figures.
Replace the `TODO` cells once a live run is captured (see
[Filling in the numbers](#filling-in-the-numbers)). Do not fabricate values.

### Start latency by mode (median of N samples, R runs)

| Mode | Median (ns) | p90 (ns) | Mean (ns) | Stddev (ns) | vs cold |
|---|---|---|---|---|---|
| `cold` | TODO | TODO | TODO | TODO | — |
| `cache-hit` | TODO | TODO | TODO | TODO | TODO |
| `warm` | TODO | TODO | TODO | TODO | TODO |

### Cross-run stability (median)

| Mode | Min median (ns) | Max median (ns) | Spread (ns) | Spread (%) | Verdict |
|---|---|---|---|---|---|
| `cold` | TODO | TODO | TODO | TODO | TODO |
| `cache-hit` | TODO | TODO | TODO | TODO | TODO |
| `warm` | TODO | TODO | TODO | TODO | TODO |

## Filling in the numbers

1. On a host with a live container runtime, wire the prep hooks and session
   command, then run the driver (see [Rerunning](#rerunning) below) with
   `WORKCELL_STARTUP_OUTPUT` set to capture the Markdown report.
2. Confirm the run exits `0` — that means the cross-run stability gate passed and
   the numbers are reproducible. A non-zero exit means the spread was too wide to
   publish; investigate before transcribing.
3. Transcribe the report's per-mode medians, p90s and the stability table into the
   tables above, fill the `vs cold` column with the measured deltas, and record
   the host, runtime backend, and `N`/`R` used.

## Rerunning

From the repository root on a host with a container runtime:

```sh
# Wire the prep hooks and the session-start command to your runtime, then:
# WORKCELL_STARTUP_CMD is parsed with shell quoting; quote args with spaces,
# e.g. ...--workspace '/path/with space'. WORKCELL_STARTUP_COLD_PREP is re-run
# before every cold sample, so make it repeatable (idempotent eviction).
export WORKCELL_STARTUP_CMD='./scripts/workcell <your session-start args>'
export WORKCELL_STARTUP_COLD_PREP='<evict cached image + stop kept-warm session>'
export WORKCELL_STARTUP_CACHE_HIT_PREP='<pre-pull image, no kept-warm session>'
export WORKCELL_STARTUP_WARM_PREP='<pre-pull image + prime kept-warm session>'
export WORKCELL_STARTUP_OUTPUT=session-startup-results.md

# Defaults: 5 iterations, 1 warmup, 2 runs, 15% stability threshold.
./scripts/bench/run-startup-bench.sh
```

Tunable via environment: `WORKCELL_STARTUP_ITERATIONS`, `WORKCELL_STARTUP_WARMUP`
(forced to `0` for `cold`), `WORKCELL_STARTUP_RUNS`,
`WORKCELL_STARTUP_STABILITY_PCT`, `WORKCELL_STARTUP_CMD`,
`WORKCELL_STARTUP_COLD_PREP`, `WORKCELL_STARTUP_CACHE_HIT_PREP`,
`WORKCELL_STARTUP_WARM_PREP`, and `WORKCELL_STARTUP_OUTPUT`.

### Dry run without a runtime

The driver is CI-safe: with no runtime available it prints a clear message and
exits `0` (skip, not fail). To rehearse the full report and stability gate on any
host — no runtime needed — feed canned samples:

```sh
# One stable run set (gate passes, exit 0):
WORKCELL_STARTUP_SAMPLES_NS='10 20 30 40 50' ./scripts/bench/run-startup-bench.sh

# Two ';'-separated per-run groups with divergent medians (gate fails, exit 2):
WORKCELL_STARTUP_SAMPLES_NS='10 20 30;100 200 300' ./scripts/bench/run-startup-bench.sh
```

This canned path is what the unit tests in `internal/startupbench` use to pin the
median/p90/stddev math, the stability gate, and the skip behavior without a
container build or a live VM.

## Where this fits

The harness and driver live in `scripts/bench/` alongside the C5 exec-guard
benchmark. A scheduled, non-PR-blocking workflow lane that captures the live
numbers on a runtime-capable runner is **deferred** until the image build is
unblocked; when added it will follow the `bench.yml` pattern and upload the
Markdown report as an artifact. See
[syscall-shim-benchmarks.md](syscall-shim-benchmarks.md) for the sibling C5
baselines and [github-workflows.md](github-workflows.md) for the workflow
inventory.
