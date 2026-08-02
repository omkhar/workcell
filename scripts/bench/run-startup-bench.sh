#!/usr/bin/env -S BASH_ENV= ENV= bash
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
