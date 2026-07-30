#!/usr/bin/env -S BASH_ENV= ENV= bash
# shellcheck shell=bash
set -euo pipefail

ROOT_DIR="$(cd -P "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/go-run-env.sh"

ensure_go_run_env
STARTUP_BENCH_BIN_DIR="${WORKCELL_GO_CACHE_ROOT}/bin"
mkdir -p "${STARTUP_BENCH_BIN_DIR}"
STARTUP_BENCH_BIN="$(mktemp "${STARTUP_BENCH_BIN_DIR}/workcell-citools-startup-bench.XXXXXX")"
trap 'rm -f "${STARTUP_BENCH_BIN}"' EXIT
build_go_tool_in_repo "${ROOT_DIR}" "${STARTUP_BENCH_BIN}" ./cmd/workcell-citools
STARTUP_BENCH_RUNNER_PID="$$"
(
  trap '' HUP INT TERM
  while kill -0 "${STARTUP_BENCH_RUNNER_PID}" 2>/dev/null; do sleep 1; done
  rm -f -- "${STARTUP_BENCH_BIN}"
) </dev/null >/dev/null 2>&1 &
exec "${STARTUP_BENCH_BIN}" startup-bench "$@"
