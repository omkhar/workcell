# Session-start latency baselines

Workcell starts an isolated session by resolving the runtime image, booting the
container runtime (Colima today, Apple `container` under evaluation in C1), and
completing the supervisor handshake. **C2** measures that start latency and drives
it down with cached images and an optional kept-warm lane — the sibling of
[syscall-shim-benchmarks.md](syscall-shim-benchmarks.md) (C5). Numbers are captured
on a host with a live runtime, not in PR CI (a real start needs a booted VM); the
[results tables below](#results) hold a **preliminary live capture from 2026-07-15**
— recorded with its raw evidence, but confounded by three methodology issues
(documented there) and therefore not yet a certified C2 result.

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

The harness (`scripts/bench/startup-bench.sh`) times one mode: `WORKCELL_STARTUP_
WARMUP` discarded launches settle first-touch page-cache/loader costs, then
`WORKCELL_STARTUP_ITERATIONS` measured launches run inside one long-lived timer
process (like C5's in-process loop) on a **monotonic** clock (`CLOCK_MONOTONIC`),
so neither an NTP step/sleep-wake nor a per-sample interpreter launch corrupts or
inflates a sample. Stats conventions match the C5 exec-guard harness, so the pages
compare directly:

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

For `cold` **and `cache-hit`** the driver re-runs the mode's prep hook before
**every** measured sample (warmup `0`) and aggregates the per-sample timings — a
start warms the cache/lane the next start would spend, so prepping once per pass
would leave only the first sample genuine; those hooks must be **repeatable**.
Only `warm` legitimately shares one prep per pass and keeps `WORKCELL_STARTUP_WARMUP`.

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

Numbers are **relative** to the host's hardware and runtime backend, so treat the
cold-vs-warm delta (not absolute medians) as the portable signal; session start
includes VM boot, so expect a wider stddev than the C5 exec-guard numbers.

## Results

**Status: PRELIMINARY capture 2026-07-15 — recorded, but not yet a clean C2
certification.** Captured on the maintainer host (Darwin 25.5.0 arm64, 12 online
CPUs, `colima` runtime, profile `wcl-workcell-006e49ec`), 5 iterations × 2 runs,
`codex` provider; the cross-run stability gate passed (exit 0). The **exact
invocation (`WORKCELL_STARTUP_CMD` + all three prep hooks) and the complete raw
report** are preserved verbatim in
[`benchmark-evidence/session-startup-2026-07-15.md`](benchmark-evidence/session-startup-2026-07-15.md).
Three methodology confounds (below) mean these numbers are a useful preliminary
signal, **not** a certified C2 result; a clean capture remains for Batch-3.

### Measured start latency (median of 5 samples, 2 runs)

The two reported tiers are named for the **image state they actually exercise**, not
for a kept-warm session lane (see caveat 1):

| Tier (harness mode) | Median (ns) | p90 (ns) | Mean (ns) | Stddev (ns) | ≈ |
|---|---|---|---|---|---|
| image-evicted → tarball restore (`cold`) | 15863271000 | 21907998000 | 17088363200 | 2410769883 | **15.9 s** |
| image-resident (`warm`) | 13457187000 | 13685462000 | 13497043600 | 99040400 | **13.5 s** |

Both are reproducible across runs at ≤0.6% median spread. The ~2.4 s difference is
the **Docker-image restore-from-tarball cost**, not a kept-warm-session win.

### Cross-run stability (median)

| Tier | Min median (ns) | Max median (ns) | Spread (ns) | Spread (%) | Verdict |
|---|---|---|---|---|---|
| `cold` | 15863271000 | 15958072000 | 94801000 | 0.6 | STABLE |
| `warm` | 13457187000 | 13543427000 | 86240000 | 0.6 | STABLE |

### Methodology confounds (why this is preliminary, not certified)

1. **No kept-warm session was exercised.** The `warm` prep only prepares the image;
   a detached `codex` session started with no task exits within seconds, so no
   persistent kept-warm session existed during the `warm` samples. The `warm` tier
   therefore measures an **image-resident start**, not the documented kept-warm lane,
   and the delta above must **not** be read as a warm-lane win.
2. **`cache-hit` shows a real, unexplained anomaly — not noise.** In the raw report
   `cache-hit` medians are 23.75 s / 24.73 s versus 15.86 s / 15.96 s for `cold` —
   roughly **50–55% slower with no overlapping sample range**. Running `--prepare-only`
   before each sample (the `cache-hit` prep) demonstrably makes the subsequent start
   *slower* than evicting the image, which is counter-intuitive and **unresolved**. It
   is preserved in the raw report and flagged here for investigation; it is not dropped
   as noise.
3. **`cold` is a tarball restore, not a first-ever build.** The `cold` prep evicts only
   the Docker image while Workcell's local image tarball remains, so `cold` reloads from
   that tarball. A genuinely fresh host (no tarball) additionally runs the one-time
   `buildx` build of `workcell:local` (minutes) — a provisioning cost excluded here.

A clean C2 certification should establish an actual persistent kept-warm session,
resolve or explain the `cache-hit` anomaly, and decide whether to also capture a
no-tarball first-start tier.

## Filling in the numbers

On a host with a live runtime, run the driver (see [Rerunning](#rerunning)) with
`WORKCELL_STARTUP_OUTPUT` set. A `0` exit means the stability gate passed and the
numbers are reproducible (non-zero means the spread was too wide — fix first).
Transcribe the report's medians, p90s and stability table into the tables above,
fill `vs cold`, and record the host, runtime backend, and `N`/`R`.

## Rerunning

From the repository root on a host with a container runtime:

```sh
# A live run needs all three prep hooks and RUNS >= 2. WORKCELL_STARTUP_CMD is
# shell-quoted; COLD_PREP/CACHE_HIT_PREP re-run per sample, so make them idempotent.
export WORKCELL_STARTUP_CMD='./scripts/workcell <your session-start args>'
export WORKCELL_STARTUP_COLD_PREP='<evict cached image + stop kept-warm session>'
export WORKCELL_STARTUP_CACHE_HIT_PREP='<pre-pull image, no kept-warm session>'
export WORKCELL_STARTUP_WARM_PREP='<pre-pull image + prime kept-warm session>'
export WORKCELL_STARTUP_OUTPUT=session-startup-results.md

# Defaults: 5 iterations, 1 warmup, 2 runs, 15% stability threshold.
./scripts/bench/run-startup-bench.sh
```

Tunable via environment: `WORKCELL_STARTUP_ITERATIONS`, `WORKCELL_STARTUP_WARMUP`
(forced to `0` for `cold`/`cache-hit`), `WORKCELL_STARTUP_RUNS`,
`WORKCELL_STARTUP_STABILITY_PCT`, `WORKCELL_STARTUP_CMD`, the three `*_PREP` hooks,
and `WORKCELL_STARTUP_OUTPUT`. Numeric controls are validated up front
(`ITERATIONS`/`RUNS` `>= 1`, `WARMUP`/`STABILITY_PCT` `>= 0`); anything else fails
fast rather than silently misreporting.

### Dry run without a runtime

The driver is CI-safe: with no runtime it exits `0` with a clear skip message. To
rehearse the full report and stability gate on any host, feed canned samples — the
dry run runs no prep hooks (any exported `*_PREP` is ignored) and times nothing:

```sh
# One stable run set (gate passes, exit 0):
WORKCELL_STARTUP_SAMPLES_NS='10 20 30 40 50' ./scripts/bench/run-startup-bench.sh

# Two ';'-separated per-run groups with divergent medians (gate fails, exit 2):
WORKCELL_STARTUP_SAMPLES_NS='10 20 30;100 200 300' ./scripts/bench/run-startup-bench.sh
```

The unit tests in `internal/startupbench` use this canned path (plus benign live
targets) to pin the stats math, the gate, and the guards without a container.

## Where this fits

The harness and driver live in `scripts/bench/` alongside the C5 exec-guard
benchmark ([syscall-shim-benchmarks.md](syscall-shim-benchmarks.md)). A scheduled,
non-PR-blocking lane that captures the live numbers on a runtime-capable runner is
**deferred** until the image build is unblocked.
