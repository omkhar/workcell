// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package startupbench pins the C2 session-start latency benchmark's statistics,
// stability gate, and driver validation so they run under `go test ./...`.
package startupbench

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func repoRoot(tb testing.TB) string {
	tb.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatal("unable to determine repo root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// hermeticEnv builds an explicit child env (not inherited) so a stray exported
// WORKCELL_STARTUP_* can't leak in: only PATH/HOME/TMPDIR + extra (which overrides).
func hermeticEnv(extra map[string]string) []string {
	base := map[string]string{"PATH": os.Getenv("PATH")}
	for _, k := range []string{"HOME", "TMPDIR"} {
		if v, ok := os.LookupEnv(k); ok {
			base[k] = v
		}
	}
	for k, v := range extra {
		base[k] = v
	}
	env := make([]string, 0, len(base))
	for k, v := range base {
		env = append(env, k+"="+v)
	}
	return env
}

// runScriptSplit runs a bench script with args + extra env, returning exit code,
// stdout and stderr separately, with a hermetic child environment.
func runScriptSplit(tb testing.TB, relScript string, env map[string]string, args ...string) (int, string, string) {
	tb.Helper()
	root := repoRoot(tb)
	cmd := exec.Command(filepath.Join(root, filepath.FromSlash(relScript)), args...)
	cmd.Dir = root
	cmd.Env = hermeticEnv(env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return 0, stdout.String(), stderr.String()
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), stdout.String(), stderr.String()
	}
	tb.Fatalf("run %s failed: %v\n%s%s", relScript, err, stdout.String(), stderr.String())
	return -1, "", ""
}

// runScript is runScriptSplit with stdout+stderr combined (order not preserved).
func runScript(tb testing.TB, relScript string, env map[string]string, args ...string) (int, string) {
	tb.Helper()
	code, stdout, stderr := runScriptSplit(tb, relScript, env, args...)
	return code, stdout + stderr
}

// writeExec writes an executable helper script for a test.
func writeExec(tb testing.TB, path, script string) {
	tb.Helper()
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		tb.Fatalf("write %s: %v", path, err)
	}
}

// liveEnv returns a live-run env (auto-detect bypassed, all state hooks no-op,
// all modes selected, RUNS>=2, gate widened so real timing can't flake) merged
// with extra, which overrides. Tests set only what they exercise.
func liveEnv(extra map[string]string) map[string]string {
	truePath, err := exec.LookPath("true")
	if err != nil {
		panic("true executable is required by startup benchmark tests")
	}
	env := map[string]string{
		"WORKCELL_STARTUP_RUNTIME":        "colima",
		"WORKCELL_STARTUP_MODES":          "cold cache-hit warm",
		"WORKCELL_STARTUP_RUNS":           "2",
		"WORKCELL_STARTUP_STABILITY_PCT":  "100000000",
		"WORKCELL_STARTUP_COLD_PREP":      truePath,
		"WORKCELL_STARTUP_CACHE_HIT_PREP": truePath,
		"WORKCELL_STARTUP_WARM_PREP":      truePath,
		"WORKCELL_STARTUP_WARM_VERIFY":    truePath,
		"WORKCELL_STARTUP_TEARDOWN":       truePath,
	}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

func runLiveDriver(tb testing.TB, env map[string]string, target ...string) (int, string) {
	tb.Helper()
	env, target = completeLiveFixture(tb, env, target)
	return runScript(tb, driver, env, append([]string{"--"}, target...)...)
}
func completeLiveFixture(tb testing.TB, env map[string]string, target []string) (map[string]string, []string) {
	tb.Helper()
	env = maps.Clone(env)
	dir := tb.TempDir()
	if env["WORKCELL_STARTUP_TEARDOWN_VERIFY"] == "" {
		verify := filepath.Join(dir, "verify")
		writeExec(tb, verify, "#!/usr/bin/env bash\nset -euo pipefail\nprintf 'absent session_id=%s sample_token=%s\\n' \"${WORKCELL_STARTUP_SESSION_ID}\" \"${WORKCELL_STARTUP_SAMPLE_TOKEN}\"\n")
		env["WORKCELL_STARTUP_TEARDOWN_VERIFY"] = verify
	}
	if len(target) == 0 {
		sampleTarget := filepath.Join(dir, "target")
		writeExec(tb, sampleTarget, "#!/usr/bin/env bash\nset -euo pipefail\nprintf 'session_id=sample-%s-%s-%s\\n' \"${WORKCELL_STARTUP_SAMPLE_MODE}\" \"${WORKCELL_STARTUP_SAMPLE_RUN}\" \"${WORKCELL_STARTUP_SAMPLE_INDEX}\"\nprintf 'sample_token=%s\\n' \"${WORKCELL_STARTUP_SAMPLE_TOKEN}\"\n")
		target = []string{sampleTarget}
	}
	return env, target
}
func runLiveDriverSplit(tb testing.TB, env map[string]string, target ...string) (int, string, string) {
	tb.Helper()
	env, target = completeLiveFixture(tb, env, target)
	return runScriptSplit(tb, driver, env, append([]string{"--"}, target...)...)
}

const driver = "scripts/bench/run-startup-bench.sh"

func TestCalculateStatsConventions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []int64
		want   statistics
	}{
		{"odd", []int64{50, 10, 40, 20, 30}, statistics{5, 30, 30, 50, 14, 10, 50}},
		{"even upper median", []int64{10, 20, 30, 40, 50, 60}, statistics{6, 35, 40, 60, 17, 10, 60}},
		{"ties round to even", []int64{0, 1}, statistics{2, 0, 1, 1, 0, 0, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := calculate(tc.values); got != tc.want {
				t.Fatalf("calculate(%v) = %+v, want %+v", tc.values, got, tc.want)
			}
		})
	}
}

func TestPureInputValidation(t *testing.T) {
	if _, err := parseModes("cold bogus"); err == nil {
		t.Fatal("accepted an unsupported mode")
	}
	if _, err := parseSampleGroups("10 x 30"); err == nil {
		t.Fatal("accepted a non-integer sample")
	}
	if _, err := parseCapture([]byte("session_id=--all\nsample_token=token\n"), "token"); err == nil {
		t.Fatal("accepted an option-shaped session id")
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
		"classification: benchmark-only; caller-provided hooks are not C2 certification evidence",
		"caller-supplied teardown and absence-verification hooks for live launches",
		"| cold |", "| warm |",
		"## Raw samples", "mode=cold run=1 index=1 duration_ns=10",
		"Cross-run stability (median)",
		"Stability gate: STABLE",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q\n---\n%s", want, out)
		}
	}
}

func TestDriverArgumentValidationPrecedence(t *testing.T) {
	canned := map[string]string{"WORKCELL_STARTUP_SAMPLES_NS": "10 20 30"}
	noRuntime := map[string]string{"WORKCELL_STARTUP_RUNTIME": "none"}
	const certification = "run-startup-bench: certification is not a startup-bench command\n"
	const cannedUsage = "run-startup-bench: canned-sample mode does not accept arguments\n"
	const liveUsage = "run-startup-bench: measured argv must follow -- and include a target\n"
	assertUsage := func(name string, env map[string]string, want string, args ...string) {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := runScriptSplit(t, driver, env, args...)
			if code != 2 || stdout != "" || stderr != want {
				t.Fatalf("exit=%d stdout=%q stderr=%q, want exit=2 stdout empty stderr=%q", code, stdout, stderr, want)
			}
		})
	}
	assertUsage("certification", canned, certification, "certify")
	assertUsage("canned-live-shape", canned, cannedUsage, "--", "target")
	assertUsage("live-certify-flag", noRuntime, liveUsage, "--certify")
	assertUsage("live-lone-separator", noRuntime, liveUsage, "--")
}

func TestDriverDryRunUnstableFailsGate(t *testing.T) {
	// Two groups with very different medians (20 vs 200) exceed the 15% threshold.
	code, out := runScript(t, driver,
		map[string]string{"WORKCELL_STARTUP_SAMPLES_NS": "10 20 30;100 200 300"})
	if code != 1 {
		t.Fatalf("unstable dry run should exit 1, got %d: %s", code, out)
	}
	if !strings.Contains(out, "Stability gate: UNSTABLE") {
		t.Errorf("missing UNSTABLE gate verdict: %s", out)
	}
	if !strings.Contains(out, "stability gate FAILED") {
		t.Errorf("missing gate-failure diagnostic: %s", out)
	}
	if !strings.Contains(out, "iterations: 3") {
		t.Errorf("dry-run metadata must use the canned group size: %s", out)
	}
}

func TestRunScriptEnvIsHermetic(t *testing.T) {
	// A stray exported WORKCELL_STARTUP_* must not leak in: a no-runtime run must still SKIP (not a dry run) nor run the hook.
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

func TestCommandEnvReplacesInheritedValues(t *testing.T) {
	const key = "WORKCELL_STARTUP_SAMPLE_TOKEN"
	t.Setenv(key, "stale")
	var got []string
	for _, entry := range commandEnv([]string{key + "=fresh"}) {
		if strings.HasPrefix(entry, key+"=") {
			got = append(got, entry)
		}
	}
	if strings.Join(got, "\n") != key+"=fresh" {
		t.Fatalf("sample token entries = %q, want one fresh value", got)
	}
}

func TestDriverStateHooksRunAtTheRequiredCadence(t *testing.T) {
	// cold/cache-hit re-run prep per sample; warm prep runs once per pass, warm
	// verification runs before warmup and measured samples, and teardown runs
	// after every launch.
	dir := t.TempDir()
	logPath := filepath.Join(dir, "operations")
	counter := func(name string) string {
		helper := filepath.Join(dir, name)
		writeExec(t, helper, "#!/usr/bin/env bash\n"+
			"set -euo pipefail\n"+
			"printf '"+name+"\\n' >> \""+logPath+"\"\n")
		return helper
	}
	env := liveEnv(map[string]string{
		"WORKCELL_STARTUP_ITERATIONS":     "2",
		"WORKCELL_STARTUP_WARMUP":         "1",
		"WORKCELL_STARTUP_COLD_PREP":      counter("cold-prep"),
		"WORKCELL_STARTUP_WARM_PREP":      counter("warm-prep"),
		"WORKCELL_STARTUP_CACHE_HIT_PREP": counter("cache-hit-prep"),
		"WORKCELL_STARTUP_WARM_VERIFY":    counter("warm-verify"),
		"WORKCELL_STARTUP_TEARDOWN":       counter("teardown"),
	})
	code, out := runLiveDriver(t, env)
	if code != 0 {
		t.Fatalf("driver exit %d: %s", code, out)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int{
		"cold-prep": 4, "cache-hit-prep": 4, "warm-prep": 2,
		"warm-verify": 6, "teardown": 14,
	} {
		if got := strings.Count(string(data), name+"\n"); got != want {
			t.Errorf("%s ran %d time(s), want %d", name, got, want)
		}
	}
}

func TestDriverDryRunSkipsPrep(t *testing.T) {
	// A canned dry run must NEVER execute prep hooks; the marker must not exist.
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

func TestDriverPrepOutputStaysOffReport(t *testing.T) {
	// A prep hook's stdout (e.g. `docker pull`) must go to stderr, not the stdout report (else `run.sh > report.md` breaks).
	dir := t.TempDir()
	prep := filepath.Join(dir, "prep")
	writeExec(t, prep, "#!/usr/bin/env bash\nprintf 'PREP_STDOUT_MARKER\\n'\n")
	env := liveEnv(map[string]string{
		"WORKCELL_STARTUP_MODES":      "cold",
		"WORKCELL_STARTUP_ITERATIONS": "1",
		"WORKCELL_STARTUP_COLD_PREP":  prep,
	})
	code, stdout, stderr := runLiveDriverSplit(t, env)
	if code != 0 {
		t.Fatalf("driver exit %d:\n%s%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "PREP_STDOUT_MARKER") {
		t.Errorf("prep-hook stdout leaked into the report stream:\n%s", stdout)
	}
	if !strings.Contains(stdout, "# session-start latency benchmark results") {
		t.Errorf("report missing from stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "PREP_STDOUT_MARKER") {
		t.Errorf("prep-hook output should appear on stderr:\n%s", stderr)
	}
}

func TestEntrypointSignalKillsProcessGroupAndStillCleansUp(t *testing.T) {
	dir := t.TempDir()
	started, childPID, cleaned := filepath.Join(dir, "started"), filepath.Join(dir, "child-pid"), filepath.Join(dir, "cleaned")
	target, teardown := filepath.Join(dir, "target"), filepath.Join(dir, "teardown")
	writeExec(t, target, "#!/usr/bin/env bash\ntrap '' TERM\nsh -c 'trap \"\" TERM; while :; do sleep 1; done' &\nprintf '%s\\n' \"$!\" >\""+childPID+"\"\ntouch \""+started+"\"\nwait\n")
	writeExec(t, teardown, "#!/usr/bin/env bash\n[[ -n \"${WORKCELL_STARTUP_SAMPLE_TOKEN}\" && -z \"${WORKCELL_STARTUP_SESSION_ID}\" ]]\ntouch \""+cleaned+"\"\n")
	env := liveEnv(map[string]string{"WORKCELL_STARTUP_MODES": "cold", "WORKCELL_STARTUP_ITERATIONS": "1", "WORKCELL_STARTUP_TEARDOWN": teardown})
	env, argv := completeLiveFixture(t, env, []string{target})
	cmd := exec.Command(filepath.Join(repoRoot(t), filepath.FromSlash(driver)), append([]string{"--"}, argv...)...)
	cmd.Dir, cmd.Env = repoRoot(t), hermeticEnv(env)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("measured target did not start")
		}
	}
	if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("signaled benchmark unexpectedly passed")
	}
	pidBytes, _ := os.ReadFile(childPID)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Errorf("TERM-ignoring descendant survived: %v", err)
	}
	if _, err := os.Stat(cleaned); err != nil {
		t.Errorf("cleanup did not run after signal: %v", err)
	}
}
func TestLifecycleFailuresAreJoinedAndVerifierGetsIndependentBudget(t *testing.T) {
	dir := t.TempDir()
	teardown, verify, verified := filepath.Join(dir, "teardown"), filepath.Join(dir, "verify"), filepath.Join(dir, "verified")
	writeExec(t, teardown, "#!/usr/bin/env bash\nsleep 1\n")
	writeExec(t, verify, "#!/usr/bin/env bash\ntouch \""+verified+"\"\nprintf 'absent session_id=%s sample_token=%s\\n' \"${WORKCELL_STARTUP_SESSION_ID}\" \"${WORKCELL_STARTUP_SAMPLE_TOKEN}\"\n")
	cfg := config{target: []string{"false"}, teardown: teardown, cleanupCheck: verify, teardownTimeout: 50 * time.Millisecond, verifyTimeout: 2 * time.Second}
	_, err := measureOne(context.Background(), cfg, "cold", 1, "1", io.Discard)
	if err == nil {
		t.Fatal("combined lifecycle failure unexpectedly passed")
	}
	for _, want := range []string{"teardown:", "cold launch:", "target output:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("joined error missing %q: %v", want, err)
		}
	}
	if _, err := os.Stat(verified); err != nil {
		t.Errorf("absence verifier did not get an independent budget: %v", err)
	}
}
func TestDriverRejectsInvalidNumericControls(t *testing.T) {
	// Invalid numeric controls (RUNS=0, non-integer) must fail fast, not exit 0.
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
	// Positional argv after -- preserves a spaced argument without parsing.
	dir := t.TempDir()
	argvF := filepath.Join(dir, "argv")
	injectedF := filepath.Join(dir, "must-not-exist")
	rec := filepath.Join(dir, "record.sh")
	writeExec(t, rec, "#!/usr/bin/env bash\n"+
		"set -euo pipefail\n"+
		": > \"${ARGV_FILE}\"\n"+
		"for a in \"$@\"; do printf '%s\\n' \"$a\" >> \"${ARGV_FILE}\"; done\n"+
		"printf 'session_id=argv-sample\\nsample_token=%s\\n' \"${WORKCELL_STARTUP_SAMPLE_TOKEN}\"\n")
	env := liveEnv(map[string]string{
		"WORKCELL_STARTUP_MODES":      "cold",
		"ARGV_FILE":                   argvF,
		"WORKCELL_STARTUP_ITERATIONS": "1",
		"WORKCELL_STARTUP_WARMUP":     "0",
	})
	code, out := runLiveDriver(t, env, rec, "alpha", "beta gamma", "; touch "+injectedF)
	if code != 0 {
		t.Fatalf("driver exit %d: %s", code, out)
	}
	data, err := os.ReadFile(argvF)
	if err != nil {
		t.Fatalf("read argv file: %v", err)
	}
	got := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	want := []string{"alpha", "beta gamma", "; touch " + injectedF}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("target argv = %q, want %q (word-splitting would break the spaced arg)\n%s", got, want, out)
	}
	if _, err := os.Stat(injectedF); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("metacharacter argument was evaluated as shell: %v", err)
	}
	if _, err := parseCapture([]byte("session_id=--all\nsample_token=token\n"), "token"); err == nil {
		t.Fatal("accepted an option-shaped session id")
	}
}

func TestDriverLiveRequiresPrepHooks(t *testing.T) {
	// On a LIVE run a missing mode prep hook must fail fast (naming mode + env var); dry-run needs none.
	live := liveEnv(nil)
	delete(live, "WORKCELL_STARTUP_COLD_PREP")
	code, out := runLiveDriver(t, live)
	if code != 2 {
		t.Fatalf("missing cold prep should exit 2, got %d: %s", code, out)
	}
	if !strings.Contains(out, "WORKCELL_STARTUP_COLD_PREP") || !strings.Contains(out, "cold") {
		t.Errorf("missing-prep error must name the mode and the env var: %s", out)
	}
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
	// Runtime client present but daemon unusable must cleanly skip. Fake clients whose health probe fails, first on PATH.
	dir := t.TempDir()
	for _, name := range []string{"colima", "container", "docker"} {
		writeExec(t, filepath.Join(dir, name), "#!/usr/bin/env bash\nexit 1\n")
	}
	env := map[string]string{
		// Fakes first, then the real system bins the driver needs.
		"PATH": dir + string(os.PathListSeparator) + os.Getenv("PATH"),
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
	// A single-run live capture has no repeatability evidence, so RUNS >= 2 is required.
	live := liveEnv(map[string]string{"WORKCELL_STARTUP_RUNS": "1"})
	code, out := runLiveDriver(t, live)
	if code == 0 {
		t.Fatalf("live run with RUNS=1 should fail fast, got exit 0: %s", out)
	}
	if !strings.Contains(out, "WORKCELL_STARTUP_RUNS") || !strings.Contains(out, ">= 2") {
		t.Errorf("error should require RUNS >= 2 for a live run: %s", out)
	}
	// Dry-run with RUNS=1 is a rehearsal, not gated, and must keep working.
	code, out = runScript(t, driver, map[string]string{
		"WORKCELL_STARTUP_SAMPLES_NS": "10 20 30 40 50",
		"WORKCELL_STARTUP_RUNS":       "1",
	})
	if code != 0 {
		t.Fatalf("dry-run with RUNS=1 should still pass, got %d: %s", code, out)
	}
}

func TestDriverZeroMedianIsUnstable(t *testing.T) {
	// A 0 median in one run vs nonzero in another is degenerate (impossible), not a 0% spread; the gate must fail.
	code, out := runScript(t, driver,
		map[string]string{"WORKCELL_STARTUP_SAMPLES_NS": "0 0 0;10 20 30"})
	if code == 0 {
		t.Fatalf("zero-vs-nonzero medians should fail the gate, got %d: %s", code, out)
	}
	if !strings.Contains(out, "Stability gate: UNSTABLE") {
		t.Errorf("expected UNSTABLE verdict for a zero median: %s", out)
	}
	if strings.Contains(out, "Stability gate: STABLE") {
		t.Errorf("a zero median must not read as STABLE: %s", out)
	}
}

func TestDriverStabilityThresholdIsConfigurable(t *testing.T) {
	// The same 5% spread passes the default threshold but a 1% threshold rejects it.
	env := map[string]string{
		"WORKCELL_STARTUP_SAMPLES_NS":    "10 20 30;11 21 31",
		"WORKCELL_STARTUP_STABILITY_PCT": "1",
	}
	code, out := runScript(t, driver, env)
	if code == 0 {
		t.Fatalf("5%% spread under a 1%% threshold should fail, got %d: %s", code, out)
	}
	if !strings.Contains(out, "UNSTABLE") {
		t.Errorf("missing UNSTABLE verdict at tight threshold: %s", out)
	}
	code, out = runScript(t, driver, map[string]string{"WORKCELL_STARTUP_SAMPLES_NS": "10000;11504"})
	if code == 0 || !strings.Contains(out, "| UNSTABLE |") {
		t.Fatalf("15.04%% spread rounded to a passing decision: %s", out)
	}
}
