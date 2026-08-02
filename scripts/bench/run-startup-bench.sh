#!/usr/bin/env -S BASH_ENV= ENV= bash
#
# run-startup-bench.sh -- drive the session-start latency benchmark (C2).
#
# `cold` and `cache-hit` re-prep before every measured sample (warmup 0) so a
# discarded warmup can't spend that state; only `warm` shares one prep per pass.
# Repeated for WORKCELL_STARTUP_RUNS passes; the driver FAILS if any mode's
# run-to-run median spread exceeds the stability threshold (C5's sibling).
#
# With no live runtime the driver exits 0 with a clear skip message;
# WORKCELL_STARTUP_SAMPLES_NS switches to a canned dry-run (no runtime) used by the
# unit tests. All configuration env vars and the full methodology are documented
# in docs/session-startup-benchmarks.md.
# shellcheck shell=bash
set -euo pipefail

ROOT_DIR="$(cd -P "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/go-run-env.sh"

ensure_go_run_env
mkdir -p "${WORKCELL_GO_CACHE_ROOT}/bin"
STARTUP_BENCH_BIN="$(mktemp "${WORKCELL_GO_CACHE_ROOT}/bin/workcell-citools-startup-bench.XXXXXX")"
trap 'rm -f "${STARTUP_BENCH_BIN}"' EXIT
build_go_tool_in_repo "${ROOT_DIR}" "${STARTUP_BENCH_BIN}" ./cmd/workcell-citools
(
  trap '' HUP INT TERM
  while kill -0 "$$" 2>/dev/null; do sleep 1; done
  rm -f -- "${STARTUP_BENCH_BIN}"
) </dev/null >/dev/null 2>&1 &
exec "${STARTUP_BENCH_BIN}" startup-bench "$@"
