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

# Time the WHOLE loop inside ONE long-lived process (like C5's in-process loop) so
# no per-sample interpreter launch sits in a measured interval -- only the target's
# fork/exec. Args (warmup iterations -- target...); target is exec'd (no shell) so
# quoting survives, output discarded, non-zero launch aborts.
# shellcheck disable=SC2016  # perl/python program text: $vars are NOT shell expansions
timing_perl='use strict; use warnings; use Time::HiRes qw(clock_gettime CLOCK_MONOTONIC);
my $warm = shift @ARGV; my $iter = shift @ARGV;
sub launch { my $p = fork; die "fork failed\n" unless defined $p;
  if (!$p) { open STDOUT, ">", "/dev/null"; open STDERR, ">", "/dev/null"; exec { $ARGV[0] } @ARGV; exit 127 }
  waitpid $p, 0; die "target launch failed\n" if $? }
launch() for (1 .. $warm);
for (1 .. $iter) { my $t = clock_gettime(CLOCK_MONOTONIC); launch(); printf "%.0f\n", (clock_gettime(CLOCK_MONOTONIC) - $t) * 1e9 }'
# shellcheck disable=SC2016  # python program text: $-free, quotes intentional
timing_python='import sys, time, subprocess
warm = int(sys.argv[1]); iters = int(sys.argv[2]); argv = sys.argv[3:]
def launch():
    if subprocess.call(argv, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL) != 0:
        sys.exit("target launch failed")
for _ in range(warm):
    launch()
for _ in range(iters):
    t = time.monotonic_ns(); launch(); print(time.monotonic_ns() - t)'

case "${CLOCK}" in
  perl) raw="$(perl -e "${timing_perl}" -- "${WARMUP}" "${ITERATIONS}" "$@")" || die "target launch failed during timing" ;;
  python3) raw="$(python3 -c "${timing_python}" "${WARMUP}" "${ITERATIONS}" "$@")" || die "target launch failed during timing" ;;
esac

[ -n "${raw}" ] || die "no samples produced"
printf 'mode=%s %s\n' "${MODE}" "$(printf '%s\n' "${raw}" | stats)"
