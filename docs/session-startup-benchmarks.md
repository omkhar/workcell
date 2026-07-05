# Session-start latency baselines

Workcell starts an isolated session by resolving the runtime image, booting the
container runtime (Colima today, Apple `container` under evaluation in C1), and
completing the supervisor handshake before an agent can run. **C2** is the
program that measures that start latency, drives it down with cached images and
an optional kept-warm lane, and publishes reproducible numbers. This page
records the methodology and the rerun steps; it is the C2 sibling of
[syscall-shim-benchmarks.md](syscall-shim-benchmarks.md) (C5).

The numbers are produced on a host with a live container runtime, not in the
PR-blocking CI lanes (a real session start needs a booted VM). The results tables
below are **placeholders pending a live capture** — see
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

The `cold` vs `warm` delta is the headline C2 number; `cache-hit` separates the
image-cache win from the kept-warm-lane win so each is credited independently.

## Methodology

The harness (`scripts/bench/startup-bench.sh`) times one mode: it runs the
session-start command `WORKCELL_STARTUP_WARMUP` times (discarded) to settle
first-touch page-cache and loader costs, then `WORKCELL_STARTUP_ITERATIONS`
measured times, and reports the sample distribution. The stats conventions match
the C5 exec-guard harness exactly, so the two pages' numbers are directly
comparable:

- **median** (`sorted[floor(n/2)]`, the outlier-robust headline), **p90**
  (`sorted[floor(n*9/10)]` clamped, the tail a slow start shows), **mean/stddev**
  (population, to expose a skewed distribution), and **min/max** (observed range).

The driver (`scripts/bench/run-startup-bench.sh`) establishes each mode's runtime
state through a per-mode prep hook (`WORKCELL_STARTUP_COLD_PREP` /
`WORKCELL_STARTUP_CACHE_HIT_PREP` / `WORKCELL_STARTUP_WARM_PREP` — e.g. evicting
the cached image and stopping the kept-warm session for `cold`, pre-pulling the
image but leaving the warm lane down for `cache-hit`, or pre-pulling and priming
the warm lane for `warm`), then times the configured `WORKCELL_STARTUP_CMD` for
that mode. The whole measurement is repeated for `WORKCELL_STARTUP_RUNS` passes.

Live runs are guarded so a misconfigured capture cannot look publishable:

- **Every driven mode's prep hook is required** (`*_COLD_PREP` /
  `*_CACHE_HIT_PREP` / `*_WARM_PREP`); an unset hook fails fast rather than
  measuring whatever state happened to be present.
- **The runtime must be usable, not just installed** — a cheap read-only probe
  (`docker info` / `colima status` / `container system status`) sends a
  client-only host to the clean CI-safe skip. `WORKCELL_STARTUP_RUNTIME` overrides
  and skips the probe.
- **`WORKCELL_STARTUP_RUNS >= 2`** — the stability gate needs cross-run evidence,
  so a single-run capture is rejected.

For `cold` the driver re-runs `WORKCELL_STARTUP_COLD_PREP` before **every**
measured sample (warmup `0`) and aggregates the per-sample timings — a start warms
the cache the next start would hit, so evicting once per pass would leave only the
first sample truly cold, and the cold-prep hook must be **repeatable**. `warm` and
`cache-hit` share one prep per pass and keep `WORKCELL_STARTUP_WARMUP`.

`WORKCELL_STARTUP_CMD` is parsed with shell quoting, so a spaced argument keeps its
boundary (`--workspace '/path/with space'` stays one argv element, not word-split
tokens). The canned dry run needs no prep hooks or runtime and never executes
hooks — these guards are live-only.

### The cross-run stability gate

Reproducibility is the C2 acceptance bar, so the driver enforces it: after all
runs it computes, per mode, the run-to-run **median** spread as a percentage of
the smallest run's median, and **fails** (non-zero exit) if any mode exceeds
`WORKCELL_STARTUP_STABILITY_PCT` (default 15%) — the evidence a published number
repeats. A zero median fails the gate outright: a 0 ns start is impossible, so it
signals a broken clock rather than a 0% spread that would read as `STABLE`.

### Runner caveats

- Numbers are **relative** to the measuring host's hardware and runtime backend;
  treat the cold-vs-warm delta, not the absolute medians, as the portable signal.
- Session start includes VM boot, which is noisier than a userspace microbenchmark;
  expect a wider stddev than the C5 exec-guard numbers.

## Results

**Status: numbers pending live capture.** The tables below are templates — a
sustained upstream mirror outage blocking the image build left no live runtime to
capture real figures. Replace the `TODO` cells once a live run is captured (see
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
   command, then run the driver (see [Rerunning](#rerunning)) with
   `WORKCELL_STARTUP_OUTPUT` set to capture the report.
2. Confirm the run exits `0` (stability gate passed, numbers reproducible). A
   non-zero exit means the spread was too wide to publish — investigate first.
3. Transcribe the report's per-mode medians, p90s and stability table into the
   tables above, fill `vs cold` with the deltas, and record the host, runtime
   backend, and `N`/`R`.

## Rerunning

From the repository root on a host with a container runtime:

```sh
# Wire the prep hooks + session-start command to your runtime. WORKCELL_STARTUP_CMD
# is shell-quoted (quote args with spaces); COLD_PREP is re-run per sample (make
# it idempotent); a live run needs all three prep hooks and RUNS >= 2.
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
`WORKCELL_STARTUP_STABILITY_PCT`, `WORKCELL_STARTUP_CMD`, the three `*_PREP` hooks,
and `WORKCELL_STARTUP_OUTPUT`. The numeric controls are validated up front
(`ITERATIONS`/`RUNS` integers `>= 1`, `WARMUP`/`STABILITY_PCT` integers `>= 0`);
anything else fails fast rather than silently misreporting.

### Dry run without a runtime

The driver is CI-safe: with no runtime available it prints a clear message and
exits `0` (skip, not fail). To rehearse the full report and stability gate on any
host — no runtime needed — feed canned samples. The canned dry run needs no prep
hooks and never executes them, so any `WORKCELL_STARTUP_*_PREP` still exported
from a previous live run is ignored:

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
benchmark ([syscall-shim-benchmarks.md](syscall-shim-benchmarks.md)). A scheduled,
non-PR-blocking lane that captures the live numbers on a runtime-capable runner is
**deferred** until the image build is unblocked.
