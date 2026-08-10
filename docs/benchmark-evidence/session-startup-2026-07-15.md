# C2 Session-Start Raw Capture: 2026-07-15

This file preserves the preliminary Batch 3 capture. The interpreted result is
in [Session-Start Benchmarks](../session-startup-benchmarks.md#results).

Do not use this capture as C2 certification. The capture has four known
confounds.

## Invocation

The operator used `./scripts/bench/run-startup-bench.sh` in an interactive Zsh
session.
The paths below use `$HOME` in place of the operator home.

```sh
export REPO="$HOME/src/workcell"
export WCL_PROFILE="wcl-workcell-006e49ec"

cleanup_sessions() {
  ./scripts/workcell session list 2>/dev/null | awk 'NR>1{print $1}' | while read -r id; do
    ./scripts/workcell session stop --id "$id" >/dev/null 2>&1 || true
    ./scripts/workcell session delete --id "$id" >/dev/null 2>&1 || true
  done
}
export -f cleanup_sessions

export WORKCELL_STARTUP_CMD='./scripts/workcell session start --agent codex --workspace $REPO --session-workspace direct'
export WORKCELL_STARTUP_COLD_PREP='cleanup_sessions; DOCKER_HOST="unix://$HOME/.colima/$WCL_PROFILE/docker.sock" docker image rm -f workcell:local'
export WORKCELL_STARTUP_CACHE_HIT_PREP='cleanup_sessions; ./scripts/workcell --prepare-only --agent codex --workspace $REPO'
export WORKCELL_STARTUP_WARM_PREP='./scripts/workcell --prepare-only --agent codex --workspace $REPO; ./scripts/workcell session start --agent codex --workspace $REPO --session-workspace direct'
export WORKCELL_STARTUP_ITERATIONS=5
export WORKCELL_STARTUP_RUNS=2
export WORKCELL_STARTUP_STABILITY_PCT=15
```

The operator then ran the benchmark driver.

## Known confounds

| Confound | Effect |
|---|---|
| Zsh did not export the Bash function. | Per-sample session cleanup did not run. The taskless sessions stopped before the approximately 15-second gaps ended. |
| The taskless warm session exited. | The warm samples measured an image-resident start, not a kept-warm session. |
| The cold hook kept the local image archive. | Cold samples measured archive restore and boot, not a first build. |
| Cache-hit samples were slower than cold samples. | The cause remains unknown, so no performance conclusion uses that tier. |

The benchmark measures the return from detached `session start`. The command
returns when the session monitor is ready.

For a new capture, set the preparation variable for each selected mode to an
executable path. Set `WORKCELL_STARTUP_TEARDOWN` and
`WORKCELL_STARTUP_TEARDOWN_VERIFY` to executable paths. If you select `warm`,
also set `WORKCELL_STARTUP_WARM_PREP` and
`WORKCELL_STARTUP_WARM_VERIFY` to executable paths. Put the measured executable
and its arguments after `--`.

## Generated report

The data below preserves the generated report labels and values.

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

This stability result does not remove the confounds above.
