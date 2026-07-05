# Syscall-shim performance baselines

The `workcell_exec_guard` LD_PRELOAD shim (`runtime/container/rust/src/lib.rs`)
interposes the libc exec/spawn family and classifies every launch before
forwarding it to the real entry point. This page records the added latency that
classification costs on the **allow path** -- a launch the guard permits and
forwards -- and the methodology and rerun steps behind those numbers.

The numbers are produced by the optional `bench.yml` GitHub lane on Linux, not
on the macOS development host: the shim reads `/proc` and interposes the
glibc/musl symbols, so its overhead only exists on Linux. The results table
below holds the measured values from that lane; rerun it to refresh them (see
[Filling in the numbers](#filling-in-the-numbers)).

## What the guard hooks, and what the harness measures

The shim exports and interposes these libc symbols (`#[no_mangle]` entries in
`runtime/container/rust/src/lib.rs`):

- `execve`, `execv`, `execvp`, `execvpe`
- `execveat`, `fexecve`
- `posix_spawn`, `posix_spawnp`
- `syscall` (via an assembly trampoline that re-dispatches `SYS_execve` /
  `SYS_execveat` back through the hooked `execve` / `execveat`)

The microbenchmark exercises four representative entry points that span the two
distinct overhead shapes:

| Harness mode | Interposed symbol | Overhead shape |
|---|---|---|
| `execve` | `execve` | classification runs in the forked child (cold per launch) |
| `execv` | `execv` | as above; reads `environ` rather than an explicit `envp` |
| `execvp` | `execvp` | as above, plus `PATH` search resolution |
| `posix_spawn` | `posix_spawn` | classification runs in the calling process (warm, cached signatures) |

`execveat` / `fexecve` / `posix_spawnp` share the same classifier code paths as
the sampled entry points, so they are covered transitively rather than sampled
separately.

## Methodology

The harness (`scripts/bench/exec-guard-bench.c`) launches a benign target
(`/bin/true`) that the guard classifies as "not protected" and forwards, so
every sample runs the classifier to completion on the allow path -- it measures
classification cost, not a blocked-exec early return. Each sample is one
`fork` + exec + `wait` (for the `exec*` modes) or one `posix_spawn` + `wait`.

The driver (`scripts/bench/run-exec-guard-bench.sh`) runs the harness twice per
mode -- once unhooked (plain libc) and once with `LD_PRELOAD` set to the built
`libworkcell_exec_guard.so` -- and reports the delta. The same fork/exec/wait
structure is on both sides, so the delta isolates the guard's added latency.

Variance is controlled by:

- **Warmup.** `WORKCELL_BENCH_WARMUP` samples run and are discarded before
  measurement, so first-touch loader and page-cache costs are excluded.
- **Volume.** `WORKCELL_BENCH_ITERATIONS` measured samples per mode (5000 in the
  lane), reported as **median** (robust to scheduler outliers) alongside mean,
  p90, and standard deviation.
- **Repetition.** `WORKCELL_BENCH_RUNS` full passes (2 in the lane). The driver
  prints a cross-run stability table so the run-to-run spread of the hooked
  median is visible -- this is the C5 validation gate.

### Runner caveats

- Numbers are **relative overheads on a shared CI runner**, not absolute
  hardware figures; treat the delta (and its percentage), not the absolute
  medians, as the portable signal.
- For the `exec*` modes the classifier runs in the freshly forked child, so its
  `OnceLock` signature caches start cold each launch; for `posix_spawn` it runs
  in the long-lived driver process, so the caches are warm after the first call.
  The two overhead columns are expected to differ for this reason.
- On a stock runner `/proc/1/environ` is not readable, so the guard fails closed
  to the strict profile (`current_mode_blocks_mutable_native_exec` returns
  `true`). This matches the container's strict-profile default.

## Results

**Status: measured on the `bench.yml` lane** (`ubuntu-latest` GitHub-hosted
runner, release cdylib, glibc). These are shared-runner relative overheads, not
absolute guarantees; re-measure on the target host for absolute figures. The
guard adds ~440us (~76%) per `exec*` launch and ~205us (~42%) per `posix_spawn`
on the allow path, dominated by the classification of the resolved target.

### Allow-path overhead (median of 5000 samples, 2 runs)

| Mode | Unhooked median (ns) | Hooked median (ns) | Delta (ns) | Delta (%) |
|---|---|---|---|---|
| `execve` | 575336 | 1015989 | 440653 | 76.6 |
| `execv` | 570947 | 1009646 | 438699 | 76.8 |
| `execvp` | 573745 | 1013812 | 440067 | 76.7 |
| `posix_spawn` | 486236 | 691238 | 205002 | 42.2 |

### Cross-run stability (hooked median)

| Mode | Min (ns) | Max (ns) | Spread (ns) | Spread (%) |
|---|---|---|---|---|
| `execve` | 1015989 | 1019361 | 3372 | 0.3 |
| `execv` | 1009497 | 1009646 | 149 | 0.0 |
| `execvp` | 1013812 | 1016247 | 2435 | 0.2 |
| `posix_spawn` | 691238 | 693251 | 2013 | 0.3 |

## Filling in the numbers

1. Trigger the lane: run the **Bench** workflow (`bench.yml`) via
   `workflow_dispatch`, or wait for its weekly schedule.
2. Download the `exec-guard-bench-results` artifact (or read the job log): it is
   the Markdown report the driver emits, including both per-run tables and the
   cross-run stability table.
3. Transcribe the two runs' medians into the tables above, replacing every
   `TODO`, and confirm the stability spread is small (single-digit percent) --
   that is the evidence the lane produces stable numbers across two runs.
4. Remove the placeholder status note once the tables hold measured values.

## Rerunning locally (Linux only)

From the repository root on a Linux host:

```sh
# Build the exec-guard cdylib (offline, from vendored sources).
(cd runtime/container/rust && cargo build --release --locked --offline --lib)

# Run the benchmark (defaults: 3000 iterations, 300 warmup, 2 runs, /bin/true).
./scripts/bench/run-exec-guard-bench.sh
```

Tunable via environment: `WORKCELL_BENCH_ITERATIONS`, `WORKCELL_BENCH_WARMUP`,
`WORKCELL_BENCH_RUNS`, `WORKCELL_BENCH_TARGET`, `WORKCELL_EXEC_GUARD_SO`, and
`WORKCELL_BENCH_OUTPUT` (write the Markdown report to a file).

On macOS the driver compiles and runs the **unhooked baseline only** and says
so: `DYLD_INSERT_LIBRARIES` semantics differ and the Linux-only guard logic does
not apply, so hooked numbers must come from Linux.

## Where this fits

The lane is registered in `policy/workflow-lane-policy.json` and reflected in
`policy/workflow-lanes.json`; the artifact retention is in
`policy/retention-policy.json` and mirrored in
[retention-policy.md](retention-policy.md). See
[github-workflows.md](github-workflows.md) for the full workflow inventory and
[fuzzing.md](fuzzing.md) for the sibling scheduled exec-guard lane.
