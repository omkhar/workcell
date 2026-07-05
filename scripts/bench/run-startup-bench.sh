#!/usr/bin/env -S BASH_ENV= ENV= bash
#
# run-startup-bench.sh -- drive the session-start latency benchmark (C2).
#
# For each mode (cold, cache-hit, warm) it runs a per-mode prep hook to
# establish the runtime state, then times WORKCELL_STARTUP_ITERATIONS full
# session starts of the target command via scripts/bench/startup-bench.sh,
# reporting the median, p90 and spread. The `cold` mode forces warmup to 0 so a
# discarded warmup launch cannot spend the freshly-evicted state -- every cold
# sample is measured against cold-prepped state, not a warmed start.
# The whole measurement is repeated for WORKCELL_STARTUP_RUNS
# passes so a reviewer can confirm the numbers are stable across runs, and the
# driver FAILS if the run-to-run spread of any mode's median exceeds the
# stability threshold -- the C2 sibling of the C5 cross-run validation gate.
#
# CI/offline safety: session starts need a live container runtime (Colima or an
# Apple `container` VM). When none is available the driver prints a clear
# message and exits 0 (skip, not fail), so it is safe to invoke as a dry run on
# any host. Supplying WORKCELL_STARTUP_SAMPLES_NS switches the driver into a
# canned dry-run that exercises the full report + stability gate without a
# runtime (used by the unit tests and for local rehearsal).
# See docs/session-startup-benchmarks.md.
#
# Configuration (all optional, via environment):
#   WORKCELL_STARTUP_ITERATIONS  measured samples per mode (default 5)
#   WORKCELL_STARTUP_WARMUP      discarded warmup samples (default 1; forced to 0 for cold)
#   WORKCELL_STARTUP_RUNS        full measurement passes (default 2)
#   WORKCELL_STARTUP_STABILITY_PCT  max allowed cross-run median spread (default 15)
#   WORKCELL_STARTUP_CMD         session-start command to time (required live)
#   WORKCELL_STARTUP_COLD_PREP   shell run before the cold pass (evict cache + stop warm lane)
#   WORKCELL_STARTUP_CACHE_HIT_PREP  shell run before the cache-hit pass (prime cache, no warm lane)
#   WORKCELL_STARTUP_WARM_PREP   shell run before the warm pass (prime cache + warm lane)
#   WORKCELL_STARTUP_RUNTIME     override runtime detection (a name, or "none")
#   WORKCELL_STARTUP_SAMPLES_NS  canned samples -> dry-run, no runtime needed.
#                                A ';' splits per-run groups (each ';'-segment is
#                                one run's samples), which drives RUNS and lets a
#                                dry run rehearse an unstable cross-run spread.
#   WORKCELL_STARTUP_OUTPUT      also write the Markdown report to this file
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/.."
ROOT_DIR="$(cd "${ROOT_DIR}" && pwd)"
# WORKCELL_STARTUP_HARNESS overrides the harness path (test seam only).
HARNESS="${WORKCELL_STARTUP_HARNESS:-${ROOT_DIR}/scripts/bench/startup-bench.sh}"

MODES="cold cache-hit warm"
ITERATIONS="${WORKCELL_STARTUP_ITERATIONS:-5}"
WARMUP="${WORKCELL_STARTUP_WARMUP:-1}"
RUNS="${WORKCELL_STARTUP_RUNS:-2}"
STABILITY_PCT="${WORKCELL_STARTUP_STABILITY_PCT:-15}"
OUTPUT_PATH="${WORKCELL_STARTUP_OUTPUT:-}"
SAMPLES="${WORKCELL_STARTUP_SAMPLES_NS:-}"

[ -x "${HARNESS}" ] || {
  echo "run-startup-bench: harness not found or not executable: ${HARNESS}" >&2
  exit 1
}

# Detect an available container runtime. A canned-sample dry run needs none.
DRY_RUN=0
RUNTIME=""
SAMPLE_GROUPS=()
if [ -n "${SAMPLES}" ]; then
  DRY_RUN=1
  RUNTIME="dry-run (canned samples)"
  # A ';' separates per-run sample groups; multiple groups define RUNS.
  local_ifs="${IFS}"
  IFS=';' read -ra SAMPLE_GROUPS <<<"${SAMPLES}"
  IFS="${local_ifs}"
  if [ "${#SAMPLE_GROUPS[@]}" -gt 1 ]; then
    RUNS="${#SAMPLE_GROUPS[@]}"
  fi
else
  detected="${WORKCELL_STARTUP_RUNTIME:-}"
  if [ -z "${detected}" ]; then
    for candidate in colima container docker; do
      if command -v "${candidate}" >/dev/null 2>&1; then
        detected="${candidate}"
        break
      fi
    done
  fi
  if [ -z "${detected}" ] || [ "${detected}" = "none" ]; then
    echo "run-startup-bench: no container runtime (Colima / Apple container) is" \
      "available on this host; session-start latency needs a live runtime." >&2
    echo "run-startup-bench: skipping (clean exit). Set WORKCELL_STARTUP_SAMPLES_NS" \
      "for a canned dry run, or run on a host with a runtime. See" \
      "docs/session-startup-benchmarks.md." >&2
    exit 0
  fi
  RUNTIME="${detected}"
  if [ -z "${WORKCELL_STARTUP_CMD:-}" ]; then
    echo "run-startup-bench: WORKCELL_STARTUP_CMD (the session-start command to" \
      "time) is required for a live run." >&2
    exit 1
  fi
fi

WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/startup-bench.XXXXXX")"
trap 'rm -rf "${WORKDIR}"' EXIT
REPORT="${WORKDIR}/report.md"

# Extract an integer key=value field from a harness output line.
field() {
  printf '%s\n' "$1" | sed -n "s/.*[[:space:]]$2=\([0-9]*\).*/\1/p"
}

# Run the per-mode prep hook that establishes cold vs warm runtime state.
prep_mode() {
  case "$1" in
    cold) eval "${WORKCELL_STARTUP_COLD_PREP:-:}" ;;
    cache-hit) eval "${WORKCELL_STARTUP_CACHE_HIT_PREP:-:}" ;;
    warm) eval "${WORKCELL_STARTUP_WARM_PREP:-:}" ;;
  esac
}

# Invoke the harness for one mode. In dry-run the canned samples for this run
# are passed straight through; live, the configured session command is timed.
run_harness() {
  # $1 mode, $2 run index (1-based), $3 warmup for this mode
  if [ "${DRY_RUN}" -eq 1 ]; then
    local gi=$(($2 - 1))
    [ "${gi}" -lt "${#SAMPLE_GROUPS[@]}" ] || gi=$((${#SAMPLE_GROUPS[@]} - 1))
    WORKCELL_STARTUP_SAMPLES_NS="${SAMPLE_GROUPS[gi]}" "${HARNESS}" "$1" "${ITERATIONS}" "$3"
  else
    # shellcheck disable=SC2086  # WORKCELL_STARTUP_CMD is an intentional word-split command
    "${HARNESS}" "$1" "${ITERATIONS}" "$3" -- ${WORKCELL_STARTUP_CMD}
  fi
}

# Effective warmup for a mode. Cold must reflect a true first start, so its
# freshly-evicted state cannot be spent on a discarded warmup launch; warm and
# cache-hit keep the configured warmup to settle first-touch page-cache costs.
mode_warmup() {
  case "$1" in
    cold) echo 0 ;;
    *) echo "${WARMUP}" ;;
  esac
}

{
  echo "# session-start latency benchmark results"
  echo
  echo "- date (UTC): $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  echo "- host: $(uname -srm)"
  echo "- online CPUs: $(getconf _NPROCESSORS_ONLN 2>/dev/null || echo unknown)"
  echo "- runtime: ${RUNTIME}"
  echo "- iterations: ${ITERATIONS} (warmup ${WARMUP}; cold forces warmup 0) x ${RUNS} run(s)"
  echo "- stability threshold: ${STABILITY_PCT}% cross-run median spread"
  echo
} >"${REPORT}"

run_index=1
while [ "${run_index}" -le "${RUNS}" ]; do
  run_file="${WORKDIR}/run-${run_index}"
  : >"${run_file}"

  {
    echo "## Run ${run_index}"
    echo
    echo "| Mode | Median (ns) | p90 (ns) | Mean (ns) | Stddev (ns) | Min (ns) | Max (ns) | n |"
    echo "|---|---|---|---|---|---|---|---|"
  } >>"${REPORT}"

  for mode in ${MODES}; do
    prep_mode "${mode}"
    line="$(run_harness "${mode}" "${run_index}" "$(mode_warmup "${mode}")")"
    med="$(field "${line}" median_ns)"
    p90="$(field "${line}" p90_ns)"
    mean="$(field "${line}" mean_ns)"
    std="$(field "${line}" stddev_ns)"
    lo="$(field "${line}" min_ns)"
    hi="$(field "${line}" max_ns)"
    n="$(field "${line}" n)"

    printf '| %s | %s | %s | %s | %s | %s | %s | %s |\n' \
      "${mode}" "${med}" "${p90}" "${mean}" "${std}" "${lo}" "${hi}" "${n}" >>"${REPORT}"
    printf '%s %s\n' "${mode}" "${med}" >>"${run_file}"
  done

  echo >>"${REPORT}"
  run_index=$((run_index + 1))
done

# Cross-run stability gate: for each mode, the spread of the median across all
# runs as a percentage of the smallest run's median. If any mode exceeds the
# threshold the run is not reproducible and the driver fails.
GATE_STATUS="STABLE"
if [ "${RUNS}" -ge 2 ]; then
  {
    echo "## Cross-run stability (median)"
    echo
    echo "| Mode | Min median (ns) | Max median (ns) | Spread (ns) | Spread (%) | Verdict |"
    echo "|---|---|---|---|---|---|"
  } >>"${REPORT}"

  all_runs="${WORKDIR}/all-runs"
  cat "${WORKDIR}"/run-* >"${all_runs}"
  worst="$(awk -v thr="${STABILITY_PCT}" '
    { m = $1; v = $2
      if (!(m in seen)) { order[++k] = m; seen[m] = 1; min[m] = v; max[m] = v }
      if (v < min[m]) min[m] = v
      if (v > max[m]) max[m] = v }
    END {
      worst = 0
      for (i = 1; i <= k; i++) {
        m = order[i]; s = max[m] - min[m]
        p = (min[m] > 0) ? s * 100.0 / min[m] : 0
        verdict = (p > thr) ? "UNSTABLE" : "STABLE"
        if (p > worst) worst = p
        printf "| %s | %d | %d | %d | %.1f | %s |\n", m, min[m], max[m], s, p, verdict > "/dev/stderr"
      }
      printf "%.1f", worst
    }
  ' "${all_runs}" 2>>"${REPORT}")"
  echo >>"${REPORT}"

  if awk -v w="${worst}" -v thr="${STABILITY_PCT}" 'BEGIN { exit (w > thr) ? 0 : 1 }'; then
    GATE_STATUS="UNSTABLE"
  fi
  {
    if [ "${GATE_STATUS}" = "STABLE" ]; then
      echo "Stability gate: STABLE (max cross-run median spread ${worst}% <= ${STABILITY_PCT}%)."
    else
      echo "Stability gate: UNSTABLE (max cross-run median spread ${worst}% > ${STABILITY_PCT}%)."
    fi
    echo
  } >>"${REPORT}"
fi

cat "${REPORT}"
if [ -n "${OUTPUT_PATH}" ]; then
  cp "${REPORT}" "${OUTPUT_PATH}"
  echo "run-startup-bench: report written to ${OUTPUT_PATH}" >&2
fi

if [ "${GATE_STATUS}" = "UNSTABLE" ]; then
  echo "run-startup-bench: cross-run stability gate FAILED (spread ${worst}% > ${STABILITY_PCT}%)" >&2
  exit 2
fi
