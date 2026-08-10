# Syscall-shim performance baselines

The Linux `workcell_exec_guard` library checks each intercepted process launch.
This page records the added classification latency on the allow path.

The result is a relative Linux CI measurement. It is not an absolute performance
guarantee for an operator host.

## What the guard hooks, and what the harness measures

The library interposes these functions:

- `execve`
- `execv`
- `execvp`
- `execvpe`
- `execveat`
- `fexecve`
- `posix_spawn`
- `posix_spawnp`
- `syscall` for `SYS_execve` and `SYS_execveat`

The benchmark samples four representative functions:

| Mode | Additional behavior |
|---|---|
| `execve` | Reads explicit argument and environment arrays. |
| `execv` | Reads the process environment. |
| `execvp` | Resolves a bare command through a controlled `PATH`. |
| `posix_spawn` | Uses classifier caches in the long-lived driver. |

The other interposed functions reuse these classifier paths, except for
`execveat` and `fexecve`. The benchmark does not measure their file-descriptor
paths. It does not sample the other hooks separately.

## Methodology

`scripts/bench/exec-guard-bench.c` launches `/bin/true`. The guard permits this
target and completes all allow-path classification.

`scripts/bench/run-exec-guard-bench.sh` measures each mode twice:

1. Plain libc without the guard.
2. The same operation with the guard preloaded.

The reported delta is the added classification time.

### Runner caveats

The harness removes `LD_PRELOAD` before it starts each measured child. The guard
stays loaded in the harness, so its hooks still run.

This method excludes the child cost to load the shared library. Each guarded
child process also pays that cost.

The scheduled workflow uses 5,000 measured samples, 500 warm-up samples, and two
complete runs for each mode.

## Results

### Allow-path overhead (median of 5000 samples, 2 runs)

The [raw two-run report](benchmark-evidence/exec-guard-bench-2026-07-05.md)
comes from the [2026-07-05 Bench workflow run](https://github.com/omkhar/workcell/actions/runs/28729189802).
The run used a GitHub-hosted Linux runner and the release library with glibc.

| Mode | Plain median (ns) | Guard median (ns) | Delta (ns) | Delta |
|---|---|---|---|---|
| `execve` | 553855 | 818320 | 264465 | 47.7% |
| `execv` | 551629 | 818568 | 266939 | 48.4% |
| `execvp` | 565185 | 827073 | 261888 | 46.3% |
| `posix_spawn` | 467863 | 524429 | 56566 | 12.1% |

### Cross-run stability (hooked median)

| Mode | Minimum guard median (ns) | Maximum guard median (ns) | Spread |
|---|---|---|---|
| `execve` | 818320 | 826893 | 1.0% |
| `execv` | 815793 | 818568 | 0.3% |
| `execvp` | 824098 | 827073 | 0.4% |
| `posix_spawn` | 524429 | 524850 | 0.1% |

The `exec*` modes add approximately 265 microseconds in this capture.
`posix_spawn` adds approximately 57 microseconds.

Each `exec*` sample uses a new child, so its classifier caches start cold. The
`posix_spawn` samples use warm caches in the driver process.

## Where this fits

### Scheduled workflow

`.github/workflows/bench.yml` runs each week and on demand. It uploads
`exec-guard-bench-results`.

Repository policy records the workflow lane and artifact retention.
See [GitHub Workflows](github-workflows.md) and
[Retention Policy](retention-policy.md).

## Rerunning locally (Linux only)

Run this benchmark only on Linux:

```sh
(cd runtime/container/rust && \
  cargo build --release --locked --offline --lib)
./scripts/bench/run-exec-guard-bench.sh
```

The driver uses these optional variables:

- `WORKCELL_BENCH_ITERATIONS`
- `WORKCELL_BENCH_WARMUP`
- `WORKCELL_BENCH_RUNS`
- `WORKCELL_BENCH_TARGET`
- `WORKCELL_EXEC_GUARD_SO`
- `WORKCELL_BENCH_OUTPUT`

On macOS, the driver runs only the plain baseline. The Linux preload guard does
not apply to macOS.

## Filling in the numbers

To refresh this page, run the scheduled workflow. Copy the two-run medians and
spreads from its artifact. Do not use one run as a published result.
Preserve the complete report in `docs/benchmark-evidence/` before you publish
new values.
