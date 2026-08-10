# Exec-Guard Benchmark Raw Capture: 2026-07-05

This file preserves the report from the
[2026-07-05 Bench workflow run](https://github.com/omkhar/workcell/actions/runs/28729189802).
The interpreted result is in
[Syscall-Shim Benchmarks](../syscall-shim-benchmarks.md#results).

- Pull-request head: `18bbcf26390a92c863476495e447bc981fd6b873`
- Checked-out merge commit: `c1112cc1f50ccb5f9c4dbc1114e6f891b4b1f5c4`
- Artifact digest: `sha256:59968e78b3c3dbf58536de1df9f861dd3dd4ff188c5e38c5ea85a3195982beaf`

## Generated report

The data below preserves the generated report labels and values. It replaces
the runner workspace prefix with `$GITHUB_WORKSPACE`.

- date (UTC): 2026-07-05T04:14:51Z
- host: Linux 6.17.0-1018-azure x86_64
- online CPUs: 4
- target: /bin/true
- iterations: 5000 (warmup 500) x 2 run(s)
- cdylib: $GITHUB_WORKSPACE/runtime/container/rust/target/release/libworkcell_exec_guard.so

### Run 1

| Mode | Unhooked median (ns) | Hooked median (ns) | Delta (ns) | Delta (%) | Unhooked stddev (ns) | Hooked stddev (ns) |
|---|---|---|---|---|---|---|
| execve | 553855 | 818320 | 264465 | 47.7 | 66171 | 45594 |
| execv | 551629 | 818568 | 266939 | 48.4 | 37468 | 46129 |
| execvp | 565185 | 827073 | 261888 | 46.3 | 53482 | 42094 |
| posix_spawn | 467863 | 524429 | 56566 | 12.1 | 34718 | 30218 |

### Run 2

| Mode | Unhooked median (ns) | Hooked median (ns) | Delta (ns) | Delta (%) | Unhooked stddev (ns) | Hooked stddev (ns) |
|---|---|---|---|---|---|---|
| execve | 549696 | 826893 | 277197 | 50.4 | 40175 | 38619 |
| execv | 550807 | 815793 | 264986 | 48.1 | 38915 | 37926 |
| execvp | 567039 | 824098 | 257059 | 45.3 | 35583 | 37278 |
| posix_spawn | 469546 | 524850 | 55304 | 11.8 | 35119 | 32251 |

### Cross-run stability (hooked median)

| Mode | Min (ns) | Max (ns) | Spread (ns) | Spread (%) |
|---|---|---|---|---|
| execve | 818320 | 826893 | 8573 | 1.0 |
| execv | 815793 | 818568 | 2775 | 0.3 |
| execvp | 824098 | 827073 | 2975 | 0.4 |
| posix_spawn | 524429 | 524850 | 421 | 0.1 |
