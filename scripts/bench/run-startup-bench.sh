#!/usr/bin/env -S BASH_ENV= ENV= bash
#
# run-startup-bench.sh -- drive the session-start latency benchmark (C2).
#
# For each mode (cold, cache-hit, warm) it runs a per-mode prep hook, then times
# WORKCELL_STARTUP_ITERATIONS session starts via scripts/bench/startup-bench.sh.
# `cold` and `cache-hit` re-prep before every measured sample (warmup 0) so a
# discarded warmup can't spend that state; only `warm` shares one prep per pass.
# Repeated for WORKCELL_STARTUP_RUNS passes; the driver FAILS if any mode's
# run-to-run median spread exceeds the stability threshold (C5's sibling).
#
# With no live runtime the driver exits 0 with a clear skip message;
# WORKCELL_STARTUP_SAMPLES_NS switches to a canned dry-run (no runtime) used by the
# unit tests. All configuration env vars and the full methodology are documented
# in docs/session-startup-benchmarks.md.
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

# Validate a numeric driver control is an integer at/above a floor; fail fast.
validate_int() {
  # $1 env var name, $2 value, $3 floor
  case "$2" in
    '' | *[!0-9]*)
      echo "run-startup-bench: $1 must be an integer, got '$2'." >&2
      exit 1
      ;;
  esac
  if [ "$2" -lt "$3" ]; then
    echo "run-startup-bench: $1 must be >= $3, got '$2'." >&2
    exit 1
  fi
}

# Probe that an auto-detected runtime's daemon is usable (cheap read-only status),
# not just that the client binary exists, so detection can fall through to skip.
runtime_usable() {
  case "$1" in
    docker) docker info ;;
    colima) colima status ;;
    container) container system status ;;
    *) return 1 ;;
  esac >/dev/null 2>&1
}

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
    # Only select a runtime whose daemon is usable (else an installed-but-dead
    # client picks live mode then hard-fails). Explicit override skips the probe.
    for candidate in colima container docker; do
      command -v "${candidate}" >/dev/null 2>&1 || continue
      if runtime_usable "${candidate}"; then
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
  # Live runs must establish each mode's runtime state; a missing prep hook would
  # leave prep_mode a no-op and measure arbitrary state as publishable. Fail fast.
  for mode in ${MODES}; do
    case "${mode}" in
      cold) prep_var="WORKCELL_STARTUP_COLD_PREP" ;;
      cache-hit) prep_var="WORKCELL_STARTUP_CACHE_HIT_PREP" ;;
      warm) prep_var="WORKCELL_STARTUP_WARM_PREP" ;;
    esac
    if [ -z "${!prep_var:-}" ]; then
      echo "run-startup-bench: live run requires a prep hook for mode '${mode}':" \
        "set ${prep_var} to establish the ${mode} runtime state. Without it the" \
        "harness measures arbitrary state and the numbers are not publishable." >&2
      exit 1
    fi
  done
  # Parse CMD into argv honoring quoting (--workspace '/a b' stays one word).
  eval "CMD_ARGV=( ${WORKCELL_STARTUP_CMD} )"
fi

# Validate numeric controls (RUNS=0/non-integer would else misreport); RUNS last.
validate_int "WORKCELL_STARTUP_ITERATIONS" "${ITERATIONS}" 1
validate_int "WORKCELL_STARTUP_WARMUP" "${WARMUP}" 0
validate_int "WORKCELL_STARTUP_RUNS" "${RUNS}" 1
validate_int "WORKCELL_STARTUP_STABILITY_PCT" "${STABILITY_PCT}" 0

# A publishable live capture needs >=2 runs: one run skips the gate yet exits 0.
if [ "${DRY_RUN}" -eq 0 ] && [ "${RUNS}" -lt 2 ]; then
  echo "run-startup-bench: a live run requires WORKCELL_STARTUP_RUNS >= 2 for" \
    "cross-run stability evidence (got ${RUNS}); a single run is not publishable." >&2
  exit 1
fi

WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/startup-bench.XXXXXX")"
trap 'rm -rf "${WORKDIR}"' EXIT
REPORT="${WORKDIR}/report.md"

# Extract an integer key=value field from a harness output line.
field() {
  printf '%s\n' "$1" | sed -n "s/.*[[:space:]]$2=\([0-9]*\).*/\1/p"
}

# Run the per-mode prep hook; its stdout goes to stderr so it can't pollute the
# report (exit status preserved).
prep_mode() {
  case "$1" in
    cold) eval "${WORKCELL_STARTUP_COLD_PREP:-:}" ;;
    cache-hit) eval "${WORKCELL_STARTUP_CACHE_HIT_PREP:-:}" ;;
    warm) eval "${WORKCELL_STARTUP_WARM_PREP:-:}" ;;
  esac >&2
}

# Invoke the harness for warm (one prep/pass): dry-run passes canned samples,
# live times the parsed session command.
run_harness() {
  # $1 mode, $2 run index (1-based)
  if [ "${DRY_RUN}" -eq 1 ]; then
    local gi=$(($2 - 1))
    [ "${gi}" -lt "${#SAMPLE_GROUPS[@]}" ] || gi=$((${#SAMPLE_GROUPS[@]} - 1))
    WORKCELL_STARTUP_SAMPLES_NS="${SAMPLE_GROUPS[gi]}" "${HARNESS}" "$1" "${ITERATIONS}" "${WARMUP}"
  else
    "${HARNESS}" "$1" "${ITERATIONS}" "${WARMUP}" -- "${CMD_ARGV[@]}"
  fi
}

# Measure cold/cache-hit with a genuine first start per sample: re-run the mode's
# prep hook before EACH sample (warmup 0), then aggregate via the harness stats.
measure_reprep() {
  # $1 mode, $2 run index (1-based)
  local mode="$1"
  if [ "${DRY_RUN}" -eq 1 ]; then
    # Dry-run: canned samples, no prep, no launch -- emit the group's stats.
    local gi=$(($2 - 1))
    [ "${gi}" -lt "${#SAMPLE_GROUPS[@]}" ] || gi=$((${#SAMPLE_GROUPS[@]} - 1))
    WORKCELL_STARTUP_SAMPLES_NS="${SAMPLE_GROUPS[gi]}" "${HARNESS}" "${mode}" "${ITERATIONS}" 0
    return
  fi
  local samples=() one i=0
  while [ "${i}" -lt "${ITERATIONS}" ]; do
    prep_mode "${mode}"
    one="$("${HARNESS}" "${mode}" 1 0 -- "${CMD_ARGV[@]}")"
    samples+=("$(field "${one}" min_ns)")
    i=$((i + 1))
  done
  WORKCELL_STARTUP_SAMPLES_NS="${samples[*]}" "${HARNESS}" "${mode}" "${#samples[@]}" 0
}

{
  echo "# session-start latency benchmark results"
  echo
  echo "- date (UTC): $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  echo "- host: $(uname -srm)"
  echo "- online CPUs: $(getconf _NPROCESSORS_ONLN 2>/dev/null || echo unknown)"
  echo "- runtime: ${RUNTIME}"
  echo "- iterations: ${ITERATIONS} (warmup ${WARMUP}; cold/cache-hit re-prep + warmup 0 per sample) x ${RUNS} run(s)"
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
    # cold and cache-hit re-prep per measured sample; only warm shares one prep
    # for the whole pass (and dry-run runs no prep -- canned samples never launch).
    if [ "${mode}" = "warm" ]; then
      [ "${DRY_RUN}" -eq 1 ] || prep_mode "${mode}"
      line="$(run_harness "${mode}" "${run_index}")"
    else
      line="$(measure_reprep "${mode}" "${run_index}")"
    fi
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

# Cross-run stability gate: per mode, the median spread across runs as a percent
# of the smallest run's median; if any mode exceeds the threshold the driver fails.
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
  # Emit "<worst-spread%> <degenerate-flag>" on stdout; the per-mode rows go to
  # the report via stderr. A zero median in any run is a degenerate/broken
  # measurement (a 0 ns session start is impossible), not a 0% spread -- flag it
  # so the gate fails instead of reading STABLE.
  gate_line="$(awk -v thr="${STABILITY_PCT}" '
    { m = $1; v = $2
      if (!(m in seen)) { order[++k] = m; seen[m] = 1; min[m] = v; max[m] = v }
      if (v < min[m]) min[m] = v
      if (v > max[m]) max[m] = v }
    END {
      worst = 0; degenerate = 0
      for (i = 1; i <= k; i++) {
        m = order[i]; s = max[m] - min[m]
        if (min[m] <= 0) {
          degenerate = 1
          printf "| %s | %d | %d | %d | n/a | UNSTABLE |\n", m, min[m], max[m], s > "/dev/stderr"
        } else {
          p = s * 100.0 / min[m]
          verdict = (p > thr) ? "UNSTABLE" : "STABLE"
          if (p > worst) worst = p
          printf "| %s | %d | %d | %d | %.1f | %s |\n", m, min[m], max[m], s, p, verdict > "/dev/stderr"
        }
      }
      printf "%.1f %d", worst, degenerate
    }
  ' "${all_runs}" 2>>"${REPORT}")"
  echo >>"${REPORT}"
  worst="${gate_line%% *}"
  degenerate="${gate_line##* }"

  if [ "${degenerate}" -eq 1 ]; then
    GATE_STATUS="UNSTABLE"
  elif awk -v w="${worst}" -v thr="${STABILITY_PCT}" 'BEGIN { exit (w > thr) ? 0 : 1 }'; then
    GATE_STATUS="UNSTABLE"
  fi
  {
    if [ "${GATE_STATUS}" = "STABLE" ]; then
      echo "Stability gate: STABLE (max cross-run median spread ${worst}% <= ${STABILITY_PCT}%)."
    elif [ "${degenerate}" -eq 1 ]; then
      echo "Stability gate: UNSTABLE (a mode reported a zero median across runs" \
        "-- degenerate measurement, not a fast start)."
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
  if [ "${degenerate:-0}" -eq 1 ]; then
    echo "run-startup-bench: cross-run stability gate FAILED (degenerate zero median)" >&2
  else
    echo "run-startup-bench: cross-run stability gate FAILED (spread ${worst}% > ${STABILITY_PCT}%)" >&2
  fi
  exit 2
fi
