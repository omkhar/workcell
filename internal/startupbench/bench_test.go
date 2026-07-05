// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package startupbench holds the tests that pin the pure logic of the C2
// session-start latency benchmark scripts (scripts/bench/startup-bench.sh and
// scripts/bench/run-startup-bench.sh): the median/p90/stddev math, the
// cross-run stability gate, and the skip-when-no-runtime behavior. These run
// under `go test ./...` and need no container runtime.
package startupbench

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRoot(tb testing.TB) string {
	tb.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatal("unable to determine repo root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// runScript runs a bench script with the given args and extra environment,
// returning its exit code and combined stdout+stderr.
func runScript(tb testing.TB, relScript string, env map[string]string, args ...string) (int, string) {
	tb.Helper()
	root := repoRoot(tb)
	cmd := exec.Command(filepath.Join(root, filepath.FromSlash(relScript)), args...)
	cmd.Dir = root
	// Hermetic environment: build the child env explicitly instead of inheriting
	// os.Environ(), so a developer's exported WORKCELL_STARTUP_* (or any other
	// stray var) cannot leak in and change behavior. Carry only the few vars the
	// bench scripts need to locate tools and temp space, plus what the test sets
	// (a test-supplied key -- e.g. PATH -- overrides the base, with no duplicate).
	base := map[string]string{"PATH": os.Getenv("PATH")}
	for _, k := range []string{"HOME", "TMPDIR"} {
		if v, ok := os.LookupEnv(k); ok {
			base[k] = v
		}
	}
	for k, v := range env {
		base[k] = v
	}
	cmd.Env = make([]string, 0, len(base))
	for k, v := range base {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), string(out)
	}
	tb.Fatalf("run %s failed: %v\n%s", relScript, err, out)
	return -1, ""
}

const harness = "scripts/bench/startup-bench.sh"
const driver = "scripts/bench/run-startup-bench.sh"

// parseFields turns a "k=v k=v" harness line into a map.
func parseFields(line string) map[string]string {
	fields := map[string]string{}
	for _, tok := range strings.Fields(strings.TrimSpace(line)) {
		if k, v, ok := strings.Cut(tok, "="); ok {
			fields[k] = v
		}
	}
	return fields
}

func TestHarnessStatsOddSampleSet(t *testing.T) {
	// Deliberately unsorted; the harness sorts before computing. n=5 so median
	// is the 3rd value and p90 (index floor(5*9/10)=4) is the max.
	code, out := runScript(t, harness,
		map[string]string{"WORKCELL_STARTUP_SAMPLES_NS": "50 10 40 20 30"},
		"cold", "0", "0")
	if code != 0 {
		t.Fatalf("exit %d, out=%q", code, out)
	}
	f := parseFields(out)
	want := map[string]string{
		"mode": "cold", "n": "5", "mean_ns": "30", "median_ns": "30",
		"p90_ns": "50", "stddev_ns": "14", "min_ns": "10", "max_ns": "50",
	}
	for k, v := range want {
		if f[k] != v {
			t.Errorf("field %s = %q, want %q (line: %s)", k, f[k], v, strings.TrimSpace(out))
		}
	}
}

func TestHarnessMedianEvenSampleSet(t *testing.T) {
	// n=6: matches the C5 harness convention median=sorted[n/2] (upper-middle).
	code, out := runScript(t, harness,
		map[string]string{"WORKCELL_STARTUP_SAMPLES_NS": "10 20 30 40 50 60"},
		"warm", "0", "0")
	if code != 0 {
		t.Fatalf("exit %d, out=%q", code, out)
	}
	f := parseFields(out)
	if f["median_ns"] != "40" {
		t.Errorf("median_ns = %q, want 40", f["median_ns"])
	}
	if f["p90_ns"] != "60" {
		t.Errorf("p90_ns = %q, want 60", f["p90_ns"])
	}
	if f["n"] != "6" {
		t.Errorf("n = %q, want 6", f["n"])
	}
}

func TestHarnessRejectsUnknownMode(t *testing.T) {
	code, out := runScript(t, harness,
		map[string]string{"WORKCELL_STARTUP_SAMPLES_NS": "10 20 30"},
		"bogus", "0", "0")
	if code == 0 {
		t.Fatalf("expected non-zero exit for unknown mode, got 0: %s", out)
	}
	if !strings.Contains(out, "unknown mode") {
		t.Errorf("missing 'unknown mode' diagnostic: %s", out)
	}
}

func TestHarnessRejectsNonIntegerSample(t *testing.T) {
	code, out := runScript(t, harness,
		map[string]string{"WORKCELL_STARTUP_SAMPLES_NS": "10 x 30"},
		"cold", "0", "0")
	if code == 0 {
		t.Fatalf("expected non-zero exit for non-integer sample, got 0: %s", out)
	}
	if !strings.Contains(out, "non-integer sample") {
		t.Errorf("missing 'non-integer sample' diagnostic: %s", out)
	}
}

func TestHarnessLivePathTimesTarget(t *testing.T) {
	// No canned samples: the harness times a benign target. This exercises the
	// real clock + timing pipeline and confirms n == iterations with a
	// non-negative median. Values are host-dependent, so we assert structure.
	code, out := runScript(t, harness, nil, "warm", "3", "1", "--", "true")
	if code != 0 {
		t.Fatalf("exit %d, out=%q", code, out)
	}
	f := parseFields(out)
	if f["n"] != "3" {
		t.Errorf("n = %q, want 3 (line: %s)", f["n"], strings.TrimSpace(out))
	}
	for _, k := range []string{"median_ns", "p90_ns", "min_ns", "max_ns"} {
		if f[k] == "" {
			t.Errorf("missing field %s in live output: %s", k, strings.TrimSpace(out))
		}
	}
}

func TestDriverSkipsWithoutRuntime(t *testing.T) {
	code, out := runScript(t, driver, map[string]string{"WORKCELL_STARTUP_RUNTIME": "none"})
	if code != 0 {
		t.Fatalf("skip should exit 0, got %d: %s", code, out)
	}
	if !strings.Contains(out, "skipping") || !strings.Contains(out, "no container runtime") {
		t.Errorf("missing clean-skip message: %s", out)
	}
}

func TestDriverDryRunStablePasses(t *testing.T) {
	code, out := runScript(t, driver,
		map[string]string{"WORKCELL_STARTUP_SAMPLES_NS": "10 20 30 40 50"})
	if code != 0 {
		t.Fatalf("stable dry run should exit 0, got %d: %s", code, out)
	}
	for _, want := range []string{
		"# session-start latency benchmark results",
		"| cold |", "| warm |",
		"Cross-run stability (median)",
		"Stability gate: STABLE",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q\n---\n%s", want, out)
		}
	}
}

func TestDriverDryRunUnstableFailsGate(t *testing.T) {
	// Two ';'-separated per-run groups with very different medians (20 vs 200)
	// blow past the default 15%% stability threshold, so the gate must fail.
	code, out := runScript(t, driver,
		map[string]string{"WORKCELL_STARTUP_SAMPLES_NS": "10 20 30;100 200 300"})
	if code != 2 {
		t.Fatalf("unstable dry run should exit 2, got %d: %s", code, out)
	}
	if !strings.Contains(out, "Stability gate: UNSTABLE") {
		t.Errorf("missing UNSTABLE gate verdict: %s", out)
	}
	if !strings.Contains(out, "stability gate FAILED") {
		t.Errorf("missing gate-failure diagnostic: %s", out)
	}
}

func TestDriverColdSkipsWarmupAndDrivesCacheHit(t *testing.T) {
	// Regression for the C2 driver findings:
	//   P1 -- cold must not spend its freshly-evicted state on a discarded
	//         warmup launch, so the driver forces warmup=0 for cold while other
	//         modes keep the configured warmup.
	//   P2 -- the driver must drive the documented three-mode set, including
	//         cache-hit, not just cold+warm.
	// A stub harness (wired via the WORKCELL_STARTUP_HARNESS test seam) records
	// the mode + warmup it was invoked with so we can assert the argv the driver
	// actually passes on the live path.
	dir := t.TempDir()
	logPath := filepath.Join(dir, "harness.log")
	stub := filepath.Join(dir, "stub-harness.sh")
	script := "#!/usr/bin/env bash\n" +
		"set -euo pipefail\n" +
		"mode=\"$1\"; iters=\"$2\"; warmup=\"$3\"\n" +
		"printf '%s %s\\n' \"$mode\" \"$warmup\" >> \"${HARNESS_LOG}\"\n" +
		"printf 'mode=%s n=%s mean_ns=1 median_ns=1 p90_ns=1 stddev_ns=0 min_ns=1 max_ns=1\\n' \"$mode\" \"$iters\"\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub harness: %v", err)
	}
	env := map[string]string{
		"HARNESS_LOG":                 logPath,
		"WORKCELL_STARTUP_HARNESS":    stub,
		"WORKCELL_STARTUP_RUNTIME":    "colima", // bypass the no-runtime skip
		"WORKCELL_STARTUP_CMD":        "true",
		"WORKCELL_STARTUP_ITERATIONS": "2",
		"WORKCELL_STARTUP_WARMUP":     "1",
		"WORKCELL_STARTUP_RUNS":       "2", // live runs require >= 2; stub output is constant so the gate stays STABLE
		// Live runs require each driven mode's prep hook; no-ops suffice here.
		"WORKCELL_STARTUP_COLD_PREP":      ":",
		"WORKCELL_STARTUP_CACHE_HIT_PREP": ":",
		"WORKCELL_STARTUP_WARM_PREP":      ":",
	}
	code, out := runScript(t, driver, env)
	if code != 0 {
		t.Fatalf("driver exit %d: %s", code, out)
	}
	if !strings.Contains(out, "| cache-hit |") {
		t.Errorf("report missing cache-hit row (P2): %s", out)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read harness log: %v", err)
	}
	warmupByMode := map[string]string{}
	for _, ln := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		parts := strings.Fields(ln)
		if len(parts) == 2 {
			warmupByMode[parts[0]] = parts[1]
		}
	}
	for _, mode := range []string{"cold", "cache-hit", "warm"} {
		if _, ok := warmupByMode[mode]; !ok {
			t.Errorf("driver never invoked harness for mode %q; saw %v", mode, warmupByMode)
		}
	}
	if got := warmupByMode["cold"]; got != "0" {
		t.Errorf("cold warmup = %q, want 0 (P1: cold must not warm before measuring)", got)
	}
	if got := warmupByMode["warm"]; got != "1" {
		t.Errorf("warm warmup = %q, want 1 (configured warmup preserved)", got)
	}
	if got := warmupByMode["cache-hit"]; got != "1" {
		t.Errorf("cache-hit warmup = %q, want 1 (configured warmup preserved)", got)
	}
}

func TestRunScriptEnvIsHermetic(t *testing.T) {
	// A developer's exported WORKCELL_STARTUP_* must not leak into the script
	// driver tests. Export a stray SAMPLES_NS + prep hook in the parent process;
	// a no-runtime run must still cleanly SKIP (not turn into a canned dry run)
	// and must not execute the leaked prep hook.
	t.Setenv("WORKCELL_STARTUP_SAMPLES_NS", "999")
	t.Setenv("WORKCELL_STARTUP_COLD_PREP", "echo LEAKED_PREP_RAN")
	code, out := runScript(t, driver, map[string]string{"WORKCELL_STARTUP_RUNTIME": "none"})
	if code != 0 {
		t.Fatalf("hermetic skip run should exit 0, got %d: %s", code, out)
	}
	if !strings.Contains(out, "skipping") || !strings.Contains(out, "no container runtime") {
		t.Errorf("stray WORKCELL_STARTUP_SAMPLES_NS leaked (expected a clean skip): %s", out)
	}
	if strings.Contains(out, "LEAKED_PREP_RAN") {
		t.Errorf("stray prep hook leaked into the run: %s", out)
	}
}

func TestDriverColdRepsPerMeasuredSample(t *testing.T) {
	// Regression for the C2 cold-prep finding: a single session start warms the
	// cache the next sample would otherwise hit, so evicting once before the
	// whole pass leaves only the first sample genuinely cold. The driver must
	// re-run WORKCELL_STARTUP_COLD_PREP before EVERY measured cold sample, while
	// warm/cache-hit legitimately share one prep for their whole pass. Verified on
	// the live path (prep is dry-run-suppressed), timing a benign command; the
	// prep hooks append a byte per invocation so we can count them.
	dir := t.TempDir()
	coldF := filepath.Join(dir, "cold")
	warmF := filepath.Join(dir, "warm")
	chF := filepath.Join(dir, "cachehit")
	env := map[string]string{
		"WORKCELL_STARTUP_RUNTIME":    "colima", // bypass the no-runtime skip
		"WORKCELL_STARTUP_CMD":        "true",
		"WORKCELL_STARTUP_ITERATIONS": "3",
		"WORKCELL_STARTUP_WARMUP":     "0",
		"WORKCELL_STARTUP_RUNS":       "2", // live runs require >= 2 runs
		// The gate would be noisy timing `true`; this test is about prep counts,
		// so widen the threshold so the (real) stability gate never flakes.
		"WORKCELL_STARTUP_STABILITY_PCT":  "100000000",
		"WORKCELL_STARTUP_COLD_PREP":      "printf c >> " + coldF,
		"WORKCELL_STARTUP_WARM_PREP":      "printf w >> " + warmF,
		"WORKCELL_STARTUP_CACHE_HIT_PREP": "printf h >> " + chF,
	}
	code, out := runScript(t, driver, env)
	if code != 0 {
		t.Fatalf("driver exit %d: %s", code, out)
	}
	countPreps := func(path string) int {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read prep counter %s: %v", path, err)
		}
		return len(data)
	}
	// 3 measured cold samples x 2 runs -> 6 cold preps (one per sample), while
	// warm/cache-hit share one prep per pass -> 2 each across the two runs.
	if got := countPreps(coldF); got != 6 {
		t.Errorf("cold prep ran %d time(s), want 6 (once per measured sample x 2 runs)", got)
	}
	if got := countPreps(warmF); got != 2 {
		t.Errorf("warm prep ran %d time(s), want 2 (one prep per pass x 2 runs)", got)
	}
	if got := countPreps(chF); got != 2 {
		t.Errorf("cache-hit prep ran %d time(s), want 2 (one prep per pass x 2 runs)", got)
	}
	// The per-sample timings are aggregated through the harness stats core, so
	// the cold row must still read like a normal n=3 distribution.
	if !strings.Contains(out, "| cold |") || !strings.Contains(out, " 3 |") {
		t.Errorf("aggregated cold row missing/incorrect: %s", out)
	}
}

func TestDriverDryRunSkipsPrep(t *testing.T) {
	// A canned dry run must NEVER execute prep hooks, even if an operator has live
	// hooks exported from a previous run. The hooks would append to a marker file;
	// after a dry run that file must not exist.
	dir := t.TempDir()
	marker := filepath.Join(dir, "prep-ran")
	env := map[string]string{
		"WORKCELL_STARTUP_SAMPLES_NS":     "10 20 30 40 50",
		"WORKCELL_STARTUP_COLD_PREP":      "printf c >> " + marker,
		"WORKCELL_STARTUP_WARM_PREP":      "printf w >> " + marker,
		"WORKCELL_STARTUP_CACHE_HIT_PREP": "printf h >> " + marker,
	}
	code, out := runScript(t, driver, env)
	if code != 0 {
		t.Fatalf("dry run should exit 0, got %d: %s", code, out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		data, _ := os.ReadFile(marker)
		t.Errorf("dry run executed prep hook(s) (marker = %q); dry runs must skip prep", data)
	}
	if !strings.Contains(out, "Stability gate: STABLE") {
		t.Errorf("dry run should still produce a stable report: %s", out)
	}
}

func TestDriverRejectsInvalidNumericControls(t *testing.T) {
	// RUNS=0 / non-numeric controls could make the driver exit 0 with no
	// benchmarking or a misleading STABLE, so each numeric control is validated as
	// an integer at/above its floor. Uses the canned dry-run path so no runtime is
	// needed; validation runs regardless of live vs dry-run.
	cases := []struct{ name, key, val string }{
		{"RUNS_zero", "WORKCELL_STARTUP_RUNS", "0"},
		{"RUNS_nonnumeric", "WORKCELL_STARTUP_RUNS", "abc"},
		{"ITERATIONS_zero", "WORKCELL_STARTUP_ITERATIONS", "0"},
		{"WARMUP_negative", "WORKCELL_STARTUP_WARMUP", "-1"},
		{"STABILITY_PCT_nonnumeric", "WORKCELL_STARTUP_STABILITY_PCT", "5x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := map[string]string{
				"WORKCELL_STARTUP_SAMPLES_NS": "10 20 30",
				c.key:                         c.val,
			}
			code, out := runScript(t, driver, env)
			if code == 0 {
				t.Fatalf("expected non-zero exit for %s=%s, got 0: %s", c.key, c.val, out)
			}
			if !strings.Contains(out, c.key) {
				t.Errorf("error should name the offending control %s: %s", c.key, out)
			}
		})
	}
}

func TestDriverPreservesCommandArgv(t *testing.T) {
	// Regression for the WORKCELL_STARTUP_CMD word-splitting finding: an argument
	// with spaces (e.g. --workspace '/path/with space') must reach the target as
	// a single argv element, not be split/globbed. A recorder script writes the
	// argv it was launched with; the last launch wins (it truncates each time),
	// so the constant command leaves a deterministic argv regardless of mode.
	dir := t.TempDir()
	argvF := filepath.Join(dir, "argv")
	rec := filepath.Join(dir, "record.sh")
	script := "#!/usr/bin/env bash\n" +
		"set -euo pipefail\n" +
		": > \"${ARGV_FILE}\"\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\" >> \"${ARGV_FILE}\"; done\n"
	if err := os.WriteFile(rec, []byte(script), 0o755); err != nil {
		t.Fatalf("write recorder: %v", err)
	}
	env := map[string]string{
		"ARGV_FILE":                   argvF,
		"WORKCELL_STARTUP_RUNTIME":    "colima", // bypass the no-runtime skip
		"WORKCELL_STARTUP_CMD":        rec + " alpha 'beta gamma'",
		"WORKCELL_STARTUP_ITERATIONS": "1",
		"WORKCELL_STARTUP_WARMUP":     "0",
		"WORKCELL_STARTUP_RUNS":       "2", // live runs require >= 2 runs
		// This test is about argv boundaries, not stability; widen the threshold
		// so the (real) gate timing the recorder never flakes.
		"WORKCELL_STARTUP_STABILITY_PCT": "100000000",
		// Live runs require each driven mode's prep hook; no-ops suffice here.
		"WORKCELL_STARTUP_COLD_PREP":      ":",
		"WORKCELL_STARTUP_CACHE_HIT_PREP": ":",
		"WORKCELL_STARTUP_WARM_PREP":      ":",
	}
	code, out := runScript(t, driver, env)
	if code != 0 {
		t.Fatalf("driver exit %d: %s", code, out)
	}
	data, err := os.ReadFile(argvF)
	if err != nil {
		t.Fatalf("read argv file: %v", err)
	}
	got := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	want := []string{"alpha", "beta gamma"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("target argv = %q, want %q (word-splitting would break the spaced arg)\n%s", got, want, out)
	}
}

func TestDriverLiveRequiresPrepHooks(t *testing.T) {
	// Regression for the prep-hook finding: on a LIVE run, a missing mode prep
	// hook must fail fast (naming the mode + env var) instead of silently
	// measuring whatever runtime state is present and emitting publishable-looking
	// numbers. The dry-run path needs no prep hooks and must keep passing.
	live := map[string]string{
		"WORKCELL_STARTUP_RUNTIME": "colima", // bypass the no-runtime skip
		"WORKCELL_STARTUP_CMD":     "true",
		// WORKCELL_STARTUP_COLD_PREP deliberately omitted.
		"WORKCELL_STARTUP_CACHE_HIT_PREP": ":",
		"WORKCELL_STARTUP_WARM_PREP":      ":",
	}
	code, out := runScript(t, driver, live)
	if code == 0 {
		t.Fatalf("live run with a missing cold prep hook should fail, got exit 0: %s", out)
	}
	if !strings.Contains(out, "WORKCELL_STARTUP_COLD_PREP") || !strings.Contains(out, "cold") {
		t.Errorf("missing-prep error must name the mode and the env var: %s", out)
	}

	// Dry-run still works with no prep hooks set.
	code, out = runScript(t, driver,
		map[string]string{"WORKCELL_STARTUP_SAMPLES_NS": "10 20 30 40 50"})
	if code != 0 {
		t.Fatalf("dry-run with no prep hooks should pass, got %d: %s", code, out)
	}
	if !strings.Contains(out, "Stability gate: STABLE") {
		t.Errorf("dry-run should still produce a stable report: %s", out)
	}
}

func TestDriverSkipsWhenRuntimeClientButNoDaemon(t *testing.T) {
	// A host with the runtime CLIENT installed but no usable daemon must fall
	// through to the clean CI-safe skip (exit 0), not select live mode and then
	// error on the missing command/prep hooks. Fake colima/container/docker
	// clients that exist (so `command -v` finds them) but whose health probe
	// fails (exit 1); shadow any real ones by putting the fakes first on PATH.
	dir := t.TempDir()
	for _, name := range []string{"colima", "container", "docker"} {
		// Every invocation (incl. the health probe) fails; existence still lets
		// `command -v` succeed.
		if err := os.WriteFile(filepath.Join(dir, name),
			[]byte("#!/usr/bin/env bash\nexit 1\n"), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	env := map[string]string{
		// Fakes first, then the real system bins the driver needs (bash/date/...).
		"PATH": dir + string(os.PathListSeparator) + os.Getenv("PATH"),
		// No WORKCELL_STARTUP_RUNTIME (force auto-detect), no SAMPLES_NS.
	}
	code, out := runScript(t, driver, env)
	if code != 0 {
		t.Fatalf("client-only host should cleanly skip (exit 0), got %d: %s", code, out)
	}
	if !strings.Contains(out, "skipping") || !strings.Contains(out, "no container runtime") {
		t.Errorf("expected clean skip when only the client (no daemon) is present: %s", out)
	}
}

func TestDriverLiveRequiresTwoRuns(t *testing.T) {
	// A single-run live capture has no repeatability evidence; the docs say exit 0
	// means the stability gate passed, so a live run must require RUNS >= 2 and
	// fail fast. The canned dry-run path may use whatever RUNS the data implies.
	live := map[string]string{
		"WORKCELL_STARTUP_RUNTIME":        "colima", // bypass the no-runtime skip
		"WORKCELL_STARTUP_CMD":            "true",
		"WORKCELL_STARTUP_RUNS":           "1",
		"WORKCELL_STARTUP_COLD_PREP":      ":",
		"WORKCELL_STARTUP_CACHE_HIT_PREP": ":",
		"WORKCELL_STARTUP_WARM_PREP":      ":",
	}
	code, out := runScript(t, driver, live)
	if code == 0 {
		t.Fatalf("live run with RUNS=1 should fail fast, got exit 0: %s", out)
	}
	if !strings.Contains(out, "WORKCELL_STARTUP_RUNS") || !strings.Contains(out, ">= 2") {
		t.Errorf("error should require RUNS >= 2 for a live run: %s", out)
	}

	// Dry-run with a single canned group and RUNS=1 is a rehearsal, not gated or
	// publishable, and must keep working.
	code, out = runScript(t, driver, map[string]string{
		"WORKCELL_STARTUP_SAMPLES_NS": "10 20 30 40 50",
		"WORKCELL_STARTUP_RUNS":       "1",
	})
	if code != 0 {
		t.Fatalf("dry-run with RUNS=1 should still pass, got %d: %s", code, out)
	}
}

func TestDriverZeroMedianIsUnstable(t *testing.T) {
	// A median of 0 in one run and nonzero in another is a degenerate/broken
	// measurement (a 0 ns session start is impossible), not a 0%% spread that
	// reads as STABLE. The gate must fail (exit 2).
	code, out := runScript(t, driver,
		map[string]string{"WORKCELL_STARTUP_SAMPLES_NS": "0 0 0;10 20 30"})
	if code != 2 {
		t.Fatalf("zero-vs-nonzero medians should fail the gate (exit 2), got %d: %s", code, out)
	}
	if !strings.Contains(out, "Stability gate: UNSTABLE") {
		t.Errorf("expected UNSTABLE verdict for a zero median: %s", out)
	}
	if strings.Contains(out, "Stability gate: STABLE") {
		t.Errorf("a zero median must not read as STABLE: %s", out)
	}
}

func TestDriverStabilityThresholdIsConfigurable(t *testing.T) {
	// The same 20->21 spread (5%%) passes at the default threshold but a 1%%
	// threshold rejects it, proving the gate reads the configured bound.
	env := map[string]string{
		"WORKCELL_STARTUP_SAMPLES_NS":    "10 20 30;11 21 31",
		"WORKCELL_STARTUP_STABILITY_PCT": "1",
	}
	code, out := runScript(t, driver, env)
	if code != 2 {
		t.Fatalf("5%% spread under a 1%% threshold should fail (exit 2), got %d: %s", code, out)
	}
	if !strings.Contains(out, "UNSTABLE") {
		t.Errorf("missing UNSTABLE verdict at tight threshold: %s", out)
	}
}
