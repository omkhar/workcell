#!/usr/bin/env -S BASH_ENV= ENV= bash
#
# run-exec-guard-bench.sh -- drive the exec-guard microbenchmark.
#
# Compiles scripts/bench/exec-guard-bench.c and, for each interposed exec/spawn
# family call, times the guard's ALLOW path (LD_PRELOAD of the built cdylib)
# against the unhooked libc baseline. The delta is the guarded-launch overhead
# (classification plus the child's load of the guard library) the workcell_exec_guard shim adds per launch. The full measurement is repeated
# for WORKCELL_BENCH_RUNS passes so a reviewer can confirm the numbers are
# stable across runs (the C5 validation gate).
#
# The LD_PRELOAD interposition is Linux-only; on any other OS the driver runs
# the unhooked baseline only and says so. See docs/syscall-shim-benchmarks.md.
#
# Configuration (all optional, via environment):
#   WORKCELL_BENCH_ITERATIONS  measured samples per call (default 3000)
#   WORKCELL_BENCH_WARMUP      discarded warmup samples (default 300)
#   WORKCELL_BENCH_RUNS        full measurement passes (default 2)
#   WORKCELL_BENCH_TARGET      benign launch target (default /bin/true)
#   WORKCELL_EXEC_GUARD_SO     path to the built cdylib
#   WORKCELL_BENCH_OUTPUT      also write the Markdown report to this file
#   CC                         C compiler (default cc)
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/bench/run-exec-guard-bench.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

# A preload already in force skews the unhooked baseline, and inside a strict
# Workcell session the loaded guard rejects the baseline's guard-free launch.
# Run the driver outside any preloaded runtime.
if [ -n "${LD_PRELOAD:-}" ] || [ -s /etc/ld.so.preload ]; then
  echo "run-exec-guard-bench: a preload is active (LD_PRELOAD or /etc/ld.so.preload); run outside a Workcell session" >&2
  exit 2
fi

ROOT_DIR="${workcell_entry_root}"
ROOT_DIR="$(cd "${ROOT_DIR}" && pwd)"

MODES="execve execv execvp posix_spawn"
ITERATIONS="${WORKCELL_BENCH_ITERATIONS:-3000}"
WARMUP="${WORKCELL_BENCH_WARMUP:-300}"
RUNS="${WORKCELL_BENCH_RUNS:-2}"
# The guard accepts only this preload path in a child environment.
APPROVED_SO_PATH=/usr/local/lib/libworkcell_exec_guard.so
SO_PATH="${WORKCELL_EXEC_GUARD_SO:-${APPROVED_SO_PATH}}"
OUTPUT_PATH="${WORKCELL_BENCH_OUTPUT:-}"
CC_BIN="${CC:-cc}"

TARGET="${WORKCELL_BENCH_TARGET:-}"
if [ -z "${TARGET}" ]; then
  if [ -x /bin/true ]; then
    TARGET=/bin/true
  elif [ -x /usr/bin/true ]; then
    TARGET=/usr/bin/true
  else
    echo "run-exec-guard-bench: no /bin/true or /usr/bin/true target found" >&2
    exit 1
  fi
fi

OS="$(uname -s)"
HOOKED=1
if [ "${OS}" != "Linux" ]; then
  HOOKED=0
  echo "run-exec-guard-bench: ${OS} is not Linux; the exec-guard LD_PRELOAD shim" \
    "is Linux-only, running the unhooked baseline only" >&2
fi

if [ "${HOOKED}" -eq 1 ] && { [ "${SO_PATH}" != "${APPROVED_SO_PATH}" ] || [ ! -f "${SO_PATH}" ]; }; then
  echo "run-exec-guard-bench: the guard needs its cdylib at ${APPROVED_SO_PATH} (got ${SO_PATH})" >&2
  echo "  build and install it: (cd runtime/container/rust && cargo build --release --locked --offline --lib) &&" >&2
  echo "  sudo install -m 0644 runtime/container/rust/target/release/libworkcell_exec_guard.so ${APPROVED_SO_PATH}" >&2
  exit 1
fi

WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/exec-guard-bench.XXXXXX")"
trap 'rm -rf "${WORKDIR}"' EXIT
BIN="${WORKDIR}/exec-guard-bench"
REPORT="${WORKDIR}/report.md"

"${CC_BIN}" -O2 -std=c11 -Wall -Wextra \
  -o "${BIN}" "${ROOT_DIR}/scripts/bench/exec-guard-bench.c" -lm

# Extract an integer key=value field from a harness output line.
field() {
  printf '%s\n' "$1" | sed -n "s/.*[[:space:]]$2=\([0-9]*\).*/\1/p"
}

# A controlled PATH whose first entry holds the target, so the execvp mode's
# bare-name launch resolves deterministically (and exercises PATH search) rather
# than depending on the ambient PATH.
TARGET_DIR="$(cd "$(dirname "${TARGET}")" && pwd)"
BENCH_PATH="${TARGET_DIR}:/usr/bin:/bin"

run_bench() {
  # $1 mode, $2 preload flag (0/1)
  if [ "$2" -eq 1 ]; then
    PATH="${BENCH_PATH}" LD_PRELOAD="${SO_PATH}" "${BIN}" "$1" "${ITERATIONS}" "${WARMUP}" "${TARGET}"
  else
    PATH="${BENCH_PATH}" "${BIN}" "$1" "${ITERATIONS}" "${WARMUP}" "${TARGET}"
  fi
}

{
  echo "# exec-guard microbenchmark results"
  echo
  echo "- date (UTC): $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  echo "- host: $(uname -srm)"
  echo "- online CPUs: $(getconf _NPROCESSORS_ONLN 2>/dev/null || echo unknown)"
  echo "- target: ${TARGET}"
  echo "- iterations: ${ITERATIONS} (warmup ${WARMUP}) x ${RUNS} run(s)"
  if [ "${HOOKED}" -eq 1 ]; then
    echo "- cdylib: ${SO_PATH}"
  else
    echo "- cdylib: (skipped; non-Linux baseline only)"
  fi
  echo
} >"${REPORT}"

run_index=1
while [ "${run_index}" -le "${RUNS}" ]; do
  run_file="${WORKDIR}/run-${run_index}"
  : >"${run_file}"

  {
    echo "## Run ${run_index}"
    echo
    echo "| Mode | Unhooked median (ns) | Hooked median (ns) | Delta (ns) | Delta (%) | Unhooked stddev (ns) | Hooked stddev (ns) |"
    echo "|---|---|---|---|---|---|---|"
  } >>"${REPORT}"

  for mode in ${MODES}; do
    base_line="$(run_bench "${mode}" 0)"
    base_med="$(field "${base_line}" median_ns)"
    base_std="$(field "${base_line}" stddev_ns)"

    if [ "${HOOKED}" -eq 1 ]; then
      hook_line="$(run_bench "${mode}" 1)"
      hook_med="$(field "${hook_line}" median_ns)"
      hook_std="$(field "${hook_line}" stddev_ns)"
      delta="$(awk -v h="${hook_med}" -v b="${base_med}" 'BEGIN{printf "%d", h-b}')"
      pct="$(awk -v h="${hook_med}" -v b="${base_med}" \
        'BEGIN{ if (b>0) printf "%.1f", (h-b)*100.0/b; else printf "n/a" }')"
    else
      hook_med="n/a"
      hook_std="n/a"
      delta="n/a"
      pct="n/a"
    fi

    printf '| %s | %s | %s | %s | %s | %s | %s |\n' \
      "${mode}" "${base_med}" "${hook_med}" "${delta}" "${pct}" \
      "${base_std}" "${hook_std}" >>"${REPORT}"
    printf '%s %s %s\n' "${mode}" "${base_med}" "${hook_med}" >>"${run_file}"
  done

  echo >>"${REPORT}"
  run_index=$((run_index + 1))
done

# Cross-run stability summary: for each mode, the spread of the hooked median
# across all runs, as a percentage of the smallest run's median.
if [ "${HOOKED}" -eq 1 ] && [ "${RUNS}" -ge 2 ]; then
  {
    echo "## Cross-run stability (hooked median)"
    echo
    echo "| Mode | Min (ns) | Max (ns) | Spread (ns) | Spread (%) |"
    echo "|---|---|---|---|---|"
  } >>"${REPORT}"

  all_runs="${WORKDIR}/all-runs"
  cat "${WORKDIR}"/run-* >"${all_runs}"
  awk '
    { m=$1; h=$3
      if (!(m in seen)) { order[++n]=m; seen[m]=1; min[m]=h; max[m]=h }
      if (h<min[m]) min[m]=h
      if (h>max[m]) max[m]=h }
    END { for (i=1; i<=n; i++) { m=order[i]; s=max[m]-min[m];
            p=(min[m]>0)?s*100.0/min[m]:0;
            printf "| %s | %d | %d | %d | %.1f |\n", m, min[m], max[m], s, p } }
  ' "${all_runs}" >>"${REPORT}"
  echo >>"${REPORT}"
fi

cat "${REPORT}"
if [ -n "${OUTPUT_PATH}" ]; then
  cp "${REPORT}" "${OUTPUT_PATH}"
  echo "run-exec-guard-bench: report written to ${OUTPUT_PATH}" >&2
fi
