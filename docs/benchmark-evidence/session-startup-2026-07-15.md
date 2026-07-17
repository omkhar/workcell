# C2 session-start latency — raw capture (2026-07-15)

Raw generated report from the Batch-3 local-operator capture, preserved verbatim
for reproducibility and audit. The published, interpreted result lives in
[`../session-startup-benchmarks.md`](../session-startup-benchmarks.md#results);
this file is the underlying evidence, including the `cache-hit` samples that are
**not** promoted to a published tier (see that doc for why).

## Exact invocation

Driver: `scripts/bench/run-startup-bench.sh` (see
[`../session-startup-benchmarks.md`](../session-startup-benchmarks.md#rerunning)).
Environment for this capture (paths generalized; `$REPO` = the workspace,
`$WCL_PROFILE` = `wcl-workcell-006e49ec`):

```sh
WORKCELL_STARTUP_CMD='./scripts/workcell session start --agent codex --workspace $REPO --session-workspace direct'
WORKCELL_STARTUP_COLD_PREP='<stop+delete all detached sessions>; DOCKER_HOST="unix://$HOME/.colima/$WCL_PROFILE/docker.sock" docker image rm -f workcell:local'
WORKCELL_STARTUP_CACHE_HIT_PREP='<stop+delete all detached sessions>; ./scripts/workcell --prepare-only --agent codex --workspace $REPO'
WORKCELL_STARTUP_WARM_PREP='./scripts/workcell --prepare-only --agent codex --workspace $REPO; ./scripts/workcell session start --agent codex --workspace $REPO --session-workspace direct'
WORKCELL_STARTUP_ITERATIONS=5
WORKCELL_STARTUP_RUNS=2
WORKCELL_STARTUP_STABILITY_PCT=15
```

## Provenance caveats — this is a PRELIMINARY, confounded capture

- The measured command is a detached `session start`, which returns once the
  session monitor is ready (a no-task `codex` then exits shortly after, in the
  background — it does not affect the start-to-ready latency the harness times).
- **No persistent kept-warm session.** `WARM_PREP` starts a detached session, but a
  no-task `codex` exits within seconds, so no kept-warm session existed during the
  `warm` samples. The `warm` tier therefore measures an **image-resident start**, not
  the documented kept-warm lane — the `cold`→`warm` delta is an image-restore cost,
  not a warm-lane win.
- **`cold` is a tarball restore, not a first build.** `COLD_PREP` evicts only the
  Docker image; Workcell's local image tarball remains, so `cold` reloads from the
  tarball + boots. A no-tarball first-ever start additionally runs the one-time
  `buildx` build (excluded here).
- **`cache-hit` is a real, unresolved anomaly — not noise.** The `cache-hit` medians
  below (23.75 s / 24.73 s) are ~50–55% **slower** than `cold` (15.86 s / 15.96 s)
  with no overlapping range. Running `--prepare-only` before each sample makes the
  next start slower than evicting the image — counter-intuitive and **unexplained**.
  Preserved here for investigation; not used to draw a published conclusion.

## Generated report (verbatim)

- date (UTC): 2026-07-15T12:09:05Z
- host: Darwin 25.5.0 arm64
- online CPUs: 12
- runtime: colima
- iterations: 5 (warmup 1; cold/cache-hit re-prep + warmup 0 per sample) x 2 run(s)
- stability threshold: 15% cross-run median spread

### Run 1

| Mode | Median (ns) | p90 (ns) | Mean (ns) | Stddev (ns) | Min (ns) | Max (ns) | n |
|---|---|---|---|---|---|---|---|
| cold | 15863271000 | 21907998000 | 17088363200 | 2410769883 | 15823521000 | 21907998000 | 5 |
| cache-hit | 23750892000 | 24634484000 | 23605663600 | 731944064 | 22359406000 | 24634484000 | 5 |
| warm | 13457187000 | 13685462000 | 13497043600 | 99040400 | 13413047000 | 13685462000 | 5 |

### Run 2

| Mode | Median (ns) | p90 (ns) | Mean (ns) | Stddev (ns) | Min (ns) | Max (ns) | n |
|---|---|---|---|---|---|---|---|
| cold | 15958072000 | 15980669000 | 15888017600 | 149981479 | 15589029000 | 15980669000 | 5 |
| cache-hit | 24728861000 | 25590544000 | 24599065800 | 886827402 | 23530987000 | 25590544000 | 5 |
| warm | 13543427000 | 13659044000 | 13489852400 | 139001557 | 13313804000 | 13659044000 | 5 |

### Cross-run stability (median)

| Mode | Min median (ns) | Max median (ns) | Spread (ns) | Spread (%) | Verdict |
|---|---|---|---|---|---|
| cold | 15863271000 | 15958072000 | 94801000 | 0.6 | STABLE |
| cache-hit | 23750892000 | 24728861000 | 977969000 | 4.1 | STABLE |
| warm | 13457187000 | 13543427000 | 86240000 | 0.6 | STABLE |

Stability gate: STABLE (max cross-run median spread 4.1% <= 15%).
