#!/usr/bin/env -S BASH_ENV= ENV= bash
#
# startup-bench.sh -- session-start latency microbenchmark harness (C2).
#
# Times one "session start" latency sample -- the wall-clock cost of launching
# a workcell session -- for a single mode (cold / warm / cache-hit), repeated
# for a reproducible N-sample median + p90. It is the C2 sibling of the C5
# exec-guard harness (scripts/bench/exec-guard-bench.c): a small, dependency-
# free measurement core whose numbers a reviewer can reproduce. The driver
# (scripts/bench/run-startup-bench.sh) orchestrates the cold vs warm passes and
# the cross-run stability gate. See docs/session-startup-benchmarks.md.
#
# A "session start" is expensive (image resolve + VM/runtime boot + supervisor
# handshake), so the sample unit is one full launch of the target command, not
# a tight micro-loop. The stats conventions match the C5 harness exactly:
#   median = sorted[floor(n/2)]        (0-based)
#   p90    = sorted[floor(n*9/10)]     (0-based, clamped to n-1)
#   mean   = sum/n
#   stddev = sqrt(sumsq/n - mean^2)    (population)
# so cold/warm numbers are directly comparable to the exec-guard baselines.
#
# Usage:
#   startup-bench.sh <mode> <iterations> <warmup> [--] [target-cmd ...]
#     mode        one of: cold warm cache-hit
#     iterations  measured samples (each = one full target launch)
#     warmup      discarded warmup launches run before measurement
#     target-cmd  the session-start command to time (e.g. ./scripts/workcell ...)
#
# Deterministic / dry-run path: if WORKCELL_STARTUP_SAMPLES_NS is set to a
# whitespace-separated list of positive integers, those are used as the measured
# samples verbatim and NO command is launched. This makes the stats core
# reproducible in CI and unit tests without a live runtime, and is how the
# driver feeds canned data for a dry run.
#
# Output (one line, key=value pairs, all times in nanoseconds):
#   mode=<mode> n=<count> mean_ns=<m> median_ns=<md> p90_ns=<p> \
#     stddev_ns=<s> min_ns=<lo> max_ns=<hi>
#
# A failed target launch (non-zero exit) aborts with a non-zero status and no
# numbers, so a broken session start can never masquerade as a measurement.
set -euo pipefail

die() {
  echo "startup-bench: $*" >&2
  exit 1
}

[ "$#" -ge 3 ] || die "usage: startup-bench.sh <mode> <iterations> <warmup> [--] [target-cmd ...]"

MODE="$1"
ITERATIONS="$2"
WARMUP="$3"
shift 3
if [ "${1:-}" = "--" ]; then
  shift
fi

case "${MODE}" in
  cold | warm | cache-hit) ;;
  *) die "unknown mode '${MODE}' (want: cold warm cache-hit)" ;;
esac

case "${ITERATIONS}" in
  '' | *[!0-9]*) die "iterations must be a non-negative integer, got '${ITERATIONS}'" ;;
esac
case "${WARMUP}" in
  '' | *[!0-9]*) die "warmup must be a non-negative integer, got '${WARMUP}'" ;;
esac

# Reduce sorted newline-separated integer samples on stdin to the key=value
# stats line (n .. max), using the exact C5 harness conventions. Kept pure so a
# unit test can pin the median/p90/stddev math against a known sample set.
stats() {
  sort -n | awk '
    { s[NR] = $1; sum += $1; sumsq += $1 * $1 }
    END {
      n = NR
      if (n == 0) { print "startup-bench: no samples" > "/dev/stderr"; exit 1 }
      mean = sum / n
      var = sumsq / n - mean * mean
      if (var < 0) { var = 0 }
      stddev = sqrt(var)
      median = s[int(n / 2) + 1]
      p90i = int(n * 9 / 10)
      if (p90i >= n) { p90i = n - 1 }
      p90 = s[p90i + 1]
      printf "n=%d mean_ns=%.0f median_ns=%d p90_ns=%d stddev_ns=%.0f min_ns=%d max_ns=%d\n",
        n, mean, median, p90, stddev, s[1], s[n]
    }'
}

emit() {
  # $@ = integer samples (one per argument)
  local line
  line="$(printf '%s\n' "$@" | stats)"
  printf 'mode=%s %s\n' "${MODE}" "${line}"
}

# --- Deterministic path: canned samples, no launch -------------------------
if [ -n "${WORKCELL_STARTUP_SAMPLES_NS:-}" ]; then
  read -ra samples <<<"${WORKCELL_STARTUP_SAMPLES_NS}"
  [ "${#samples[@]}" -ge 1 ] || die "WORKCELL_STARTUP_SAMPLES_NS is empty"
  for sample in "${samples[@]}"; do
    case "${sample}" in
      '' | *[!0-9]*) die "WORKCELL_STARTUP_SAMPLES_NS holds a non-integer sample '${sample}'" ;;
    esac
  done
  emit "${samples[@]}"
  exit 0
fi

# --- Live path: launch the target and time each start ----------------------
[ "$#" -ge 1 ] || die "no target command given (and WORKCELL_STARTUP_SAMPLES_NS unset)"
[ "${ITERATIONS}" -ge 1 ] || die "iterations must be >= 1 for a live measurement"

# Pick a monotonic-enough nanosecond clock. GNU date has %N; macOS date does
# not, so fall back to perl/python for sub-second resolution.
CLOCK=""
probe="$(date +%s%N 2>/dev/null || true)"
case "${probe}" in
  '' | *[!0-9]*) ;;
  *) CLOCK="date" ;;
esac
if [ -z "${CLOCK}" ]; then
  if command -v perl >/dev/null 2>&1; then
    CLOCK="perl"
  elif command -v python3 >/dev/null 2>&1; then
    CLOCK="python3"
  else
    die "no nanosecond clock available (need GNU date, perl, or python3)"
  fi
fi

now_ns() {
  case "${CLOCK}" in
    date) date +%s%N ;;
    perl) perl -MTime::HiRes=time -e 'printf "%.0f", Time::HiRes::time() * 1e9' ;;
    python3) python3 -c 'import time; print(int(time.time() * 1e9))' ;;
  esac
}

launch() {
  "$@" >/dev/null 2>&1 || die "target launch failed (exit $?): $*"
}

warm_index=0
while [ "${warm_index}" -lt "${WARMUP}" ]; do
  launch "$@"
  warm_index=$((warm_index + 1))
done

samples=()
sample_index=0
while [ "${sample_index}" -lt "${ITERATIONS}" ]; do
  start="$(now_ns)"
  launch "$@"
  end="$(now_ns)"
  samples+=("$((end - start))")
  sample_index=$((sample_index + 1))
done

emit "${samples[@]}"
