#!/usr/bin/env -S BASH_ENV= ENV= bash
#
# startup-bench.sh -- session-start latency microbenchmark harness (C2).
#
# Times session-start latency for one mode (cold / warm / cache-hit) over an
# N-sample median + p90. A small, dependency-free core; run-startup-bench.sh
# orchestrates the passes and stability gate. See docs/session-startup-benchmarks.md.
# Stats conventions match the C5 exec-guard harness exactly:
#   median = sorted[floor(n/2)]        (0-based)
#   p90    = sorted[floor(n*9/10)]     (0-based, clamped to n-1)
#   mean   = sum/n ; stddev = sqrt(sumsq/n - mean^2)  (population)
#
# Usage: startup-bench.sh <mode> <iterations> <warmup> [--] [target-cmd ...]
#   mode cold|warm|cache-hit; iterations/warmup = measured/discarded launches.
# Dry-run: if WORKCELL_STARTUP_SAMPLES_NS is a whitespace-separated list of
# non-negative integers, those are the samples verbatim and NO command launches
# (how the driver/unit tests exercise the stats core with no runtime).
#
# Output (one line, ns): mode=<> n=<> mean_ns=<> median_ns=<> p90_ns=<> \
#   stddev_ns=<> min_ns=<> max_ns=<>. A failed launch aborts non-zero with no
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

# Pick a MONOTONIC nanosecond clock (mirrors the C5 exec-guard CLOCK_MONOTONIC).
# Wall-clock (`date +%s%N`) is unusable: an NTP step or sleep/wake mid-launch
# could make end-start negative or absorb the step. perl/python3 expose
# CLOCK_MONOTONIC; plain `date` does not, so it is not a fallback.
CLOCK=""
if command -v perl >/dev/null 2>&1 &&
  perl -MTime::HiRes=clock_gettime,CLOCK_MONOTONIC -e1 >/dev/null 2>&1; then
  CLOCK="perl"
elif command -v python3 >/dev/null 2>&1; then
  CLOCK="python3"
else
  die "no monotonic nanosecond clock (need perl Time::HiRes or python3)"
fi

now_ns() {
  case "${CLOCK}" in
    perl) perl -MTime::HiRes=clock_gettime,CLOCK_MONOTONIC -e 'printf "%.0f", clock_gettime(CLOCK_MONOTONIC) * 1e9' ;;
    python3) python3 -c 'import time; print(time.monotonic_ns())' ;;
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
