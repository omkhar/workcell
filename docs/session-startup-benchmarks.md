# Session-start latency baselines

## Where this fits

Workcell does not publish a certified session-start target. The C2 performance
claim remains deferred.

This page describes the benchmark driver and a preliminary 2026-07-15 capture.
The capture has known confounds and is not certification evidence.

## What is measured

One sample measures wall-clock time from `session start` invocation until the
session monitor reports ready.

| Mode | Intended state | Purpose |
|---|---|---|
| `cold` | Image absent and profile stopped | Image restore and runtime boot |
| `cache-hit` | Image present and no warm session | Image-cache effect |
| `warm` | Image present and verified warm session | Warm-session effect |

The Go driver uses a monotonic clock. It includes target launch and output
capture. It excludes driver setup and cleanup.

The report contains median, p90, mean, population standard deviation, minimum,
and maximum values.

## Methodology

`scripts/bench/run-startup-bench.sh` applies these rules:

- Each selected mode requires its preparation command.
- Cold and cache-hit preparation runs before each measured sample.
- Each launch requires teardown and an absence check.
- Warm mode also requires `WORKCELL_STARTUP_WARM_VERIFY`.
- A live run requires a usable runtime.
- A live run requires at least two passes.
- Each target must return the expected session ID and sample token.

### The cross-run stability gate

The driver checks the spread between run medians. The default maximum spread is
15% of the smallest run median. A zero median or larger spread fails the run.

### Runner caveats

These guards check command results. They do not prove resource ownership or a
correct runtime state. A dedicated certifier is necessary for a C2 claim.

## Results

### Measured start latency (5 samples per run, both runs shown)

Status: preliminary capture from 2026-07-15.

| Capture item | Value |
|---|---|
| Host | `Darwin 25.5.0 arm64` |
| Online CPUs | `12` |
| Runtime | `colima` |
| Provider | `codex` |
| Samples per run | `5` |
| Runs | `2` |
| Maximum median spread | `4.1%` |

The capture measured the image archive-restore state and the image-resident
state. It did not measure a warm-session lane.

| Actual tier | Run | Median (ns) | p90 (ns) | Mean (ns) | Standard deviation (ns) |
|---|---|---|---|---|---|
| Image archive restore | 1 | 15863271000 | 21907998000 | 17088363200 | 2410769883 |
| Image archive restore | 2 | 15958072000 | 15980669000 | 15888017600 | 149981479 |
| Image resident | 1 | 13457187000 | 13685462000 | 13497043600 | 99040400 |
| Image resident | 2 | 13543427000 | 13659044000 | 13489852400 | 139001557 |

### Cross-run stability (median)

| Tier | Minimum median (ns) | Maximum median (ns) | Spread |
|---|---|---|---|
| Image archive restore | 15863271000 | 15958072000 | 0.6% |
| Image resident | 13457187000 | 13543427000 | 0.6% |

The median difference is approximately 2.4 seconds. It represents image archive
restore cost. It is not a warm-session improvement.

### Methodology confounds (why this is preliminary, not certified)

1. The taskless warm session exited before the samples.
2. The cold mode kept the local image archive.
3. A Zsh function export did not run per-sample cleanup.
4. Cache-hit medians were 50% to 55% slower than cold medians.

The cause of the cache-hit result remains unknown. Do not use it for a
performance claim.

## Filling in the numbers

The [raw capture](benchmark-evidence/session-startup-2026-07-15.md) contains the
exact command, all samples, and the complete confound record.

## Rerunning

On a host with a live runtime, set repeatable state commands:

```sh
export WORKCELL_STARTUP_COLD_PREP=/absolute/path/to/cold-preparation
export WORKCELL_STARTUP_CACHE_HIT_PREP=/absolute/path/to/cache-preparation
export WORKCELL_STARTUP_TEARDOWN=/absolute/path/to/session-teardown
export WORKCELL_STARTUP_TEARDOWN_VERIFY=/absolute/path/to/absence-check
export WORKCELL_STARTUP_OUTPUT=session-startup-results.md

./scripts/bench/run-startup-bench.sh -- \
  /absolute/path/to/measured-wrapper ARGUMENT
```

The driver passes sample mode, run, index, and token through `WORKCELL_STARTUP_*`
variables. It passes an empty session ID to teardown. It passes the parsed
session ID only to the absence check.

The default modes are `cold cache-hit`. If a persistent warm session exists,
set `WORKCELL_STARTUP_MODES='cold cache-hit warm'`. Then set the warm
preparation and verification commands.

### Dry run without a runtime

The driver exits with a skip result when no runtime is usable. Synthetic test
values can exercise report generation without a runtime:

```sh
WORKCELL_STARTUP_SAMPLES_NS='10 20 30 40 50' \
  ./scripts/bench/run-startup-bench.sh
```

Use two semicolon-separated run groups to test the stability failure:

```sh
WORKCELL_STARTUP_SAMPLES_NS='10 20 30;100 200 300' \
  ./scripts/bench/run-startup-bench.sh
```

The synthetic test path runs no state command and measures no launch.
