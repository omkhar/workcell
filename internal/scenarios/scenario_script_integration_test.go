// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package scenarios

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

func writeScenarioScript(tb testing.TB, root, relativePath, body string) {
	tb.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		tb.Fatal(err)
	}
}

func writeScenarioManifest(tb testing.TB, root string, scenarios []map[string]any) string {
	tb.Helper()
	manifestPath := filepath.Join(root, "manifest.json")
	data, err := json.MarshalIndent(map[string]any{
		"version":   1,
		"scenarios": scenarios,
	}, "", "  ")
	if err != nil {
		tb.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		tb.Fatal(err)
	}
	return manifestPath
}

func runScenarioScript(tb testing.TB, scriptPath string, env map[string]string, args ...string) (int, string, string) {
	tb.Helper()
	cmd := exec.Command(scriptPath, args...)
	cmd.Dir = repoRoot(tb)
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	output, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(output), ""
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), string(output), ""
	}
	tb.Fatalf("run %s failed: %v", scriptPath, err)
	return 0, "", ""
}

func TestVerifyScenarioCoverageRejectsOrphanScripts(t *testing.T) {
	t.Parallel()

	scenarioRoot := t.TempDir()
	writeScenarioScript(t, scenarioRoot, "shared/test-secretless.sh", "#!/bin/sh\nexit 0\n")
	writeScenarioScript(t, scenarioRoot, "shared/test-orphan.sh", "#!/bin/sh\nexit 0\n")
	manifestPath := writeScenarioManifest(t, scenarioRoot, []map[string]any{
		{
			"id":                   "shared/secretless",
			"description":          "Secretless fixture",
			"lane":                 "secretless",
			"platform":             "any",
			"manual":               false,
			"providers":            []string{"codex"},
			"persona":              "developer",
			"test_file":            "shared/test-secretless.sh",
			"requires_credentials": false,
		},
	})

	code, stdout, _ := runScenarioScript(
		t,
		filepath.Join(repoRoot(t), "scripts", "verify-scenario-coverage.sh"),
		map[string]string{
			"WORKCELL_SCENARIO_ROOT":     scenarioRoot,
			"WORKCELL_SCENARIO_MANIFEST": manifestPath,
		},
	)
	if code != 1 {
		t.Fatalf("verify-scenario-coverage exit code = %d stdout=%q", code, stdout)
	}
	if !strings.Contains(stdout, "Scenario scripts missing from manifest") {
		t.Fatalf("stdout %q does not contain orphan warning", stdout)
	}
	if !strings.Contains(stdout, "shared/test-orphan.sh") {
		t.Fatalf("stdout %q does not mention orphan script", stdout)
	}
}

func TestRunScenarioTestsRepoRequiredOnlySkipsCertificationEntries(t *testing.T) {
	t.Parallel()

	scenarioRoot := t.TempDir()
	writeScenarioScript(t, scenarioRoot, "shared/test-required.sh", "#!/bin/sh\nset -eu\nprintf 'required-ran\\n'\n")
	writeScenarioScript(t, scenarioRoot, "shared/test-certification.sh", "#!/bin/sh\nset -eu\necho certification-ran >&2\nexit 1\n")
	writeScenarioScript(t, scenarioRoot, "shared/test-provider.sh", "#!/bin/sh\nset -eu\necho provider-ran >&2\nexit 1\n")
	writeScenarioScript(t, scenarioRoot, "shared/test-manual.sh", "#!/bin/sh\nset -eu\necho manual-ran >&2\nexit 1\n")
	manifestPath := writeScenarioManifest(t, scenarioRoot, []map[string]any{
		{
			"id":                   "shared/required",
			"description":          "Repo-required fixture",
			"lane":                 "secretless",
			"platform":             "any",
			"validation_tier":      "repo-required",
			"manual":               false,
			"providers":            []string{"codex"},
			"persona":              "developer",
			"test_file":            "shared/test-required.sh",
			"requires_credentials": false,
		},
		{
			"id":                   "shared/certification",
			"description":          "Certification fixture",
			"lane":                 "secretless",
			"platform":             "any",
			"validation_tier":      "certification",
			"manual":               false,
			"providers":            []string{"codex"},
			"persona":              "developer",
			"test_file":            "shared/test-certification.sh",
			"requires_credentials": false,
		},
		{
			"id":                   "shared/provider",
			"description":          "Provider fixture",
			"lane":                 "provider-e2e",
			"platform":             "any",
			"manual":               false,
			"providers":            []string{"codex"},
			"persona":              "developer",
			"test_file":            "shared/test-provider.sh",
			"requires_credentials": true,
		},
		{
			"id":                   "shared/manual",
			"description":          "Manual fixture",
			"lane":                 "secretless",
			"platform":             "any",
			"manual":               true,
			"providers":            []string{"codex"},
			"persona":              "developer",
			"test_file":            "shared/test-manual.sh",
			"requires_credentials": false,
		},
	})

	code, stdout, _ := runScenarioScript(
		t,
		filepath.Join(repoRoot(t), "scripts", "run-scenario-tests.sh"),
		map[string]string{
			"WORKCELL_SCENARIO_ROOT":     scenarioRoot,
			"WORKCELL_SCENARIO_MANIFEST": manifestPath,
		},
	)
	if code != 0 {
		t.Fatalf("run-scenario-tests exit code = %d stdout=%q", code, stdout)
	}
	for _, want := range []string{
		"PASS shared/required",
		"SKIP shared/certification (validation tier certification)",
		"SKIP shared/provider (lane provider-e2e)",
		"SKIP shared/manual (manual lane)",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout %q does not contain %q", stdout, want)
		}
	}
}

func TestRunScenarioTestsCertificationOnlySkipsRepoRequiredEntries(t *testing.T) {
	t.Parallel()

	scenarioRoot := t.TempDir()
	writeScenarioScript(t, scenarioRoot, "shared/test-required.sh", "#!/bin/sh\nset -eu\necho required-ran >&2\nexit 1\n")
	writeScenarioScript(t, scenarioRoot, "shared/test-certification.sh", "#!/bin/sh\nset -eu\nprintf 'certification-ran\\n'\n")
	manifestPath := writeScenarioManifest(t, scenarioRoot, []map[string]any{
		{
			"id":                   "shared/required",
			"description":          "Repo-required fixture",
			"lane":                 "secretless",
			"platform":             "any",
			"validation_tier":      "repo-required",
			"manual":               false,
			"providers":            []string{"codex"},
			"persona":              "developer",
			"test_file":            "shared/test-required.sh",
			"requires_credentials": false,
		},
		{
			"id":                   "shared/certification",
			"description":          "Certification fixture",
			"lane":                 "secretless",
			"platform":             "any",
			"validation_tier":      "certification",
			"manual":               false,
			"providers":            []string{"codex"},
			"persona":              "developer",
			"test_file":            "shared/test-certification.sh",
			"requires_credentials": false,
		},
	})

	code, stdout, _ := runScenarioScript(
		t,
		filepath.Join(repoRoot(t), "scripts", "run-scenario-tests.sh"),
		map[string]string{
			"WORKCELL_SCENARIO_ROOT":     scenarioRoot,
			"WORKCELL_SCENARIO_MANIFEST": manifestPath,
		},
		"--certification-only",
	)
	if code != 0 {
		t.Fatalf("run-scenario-tests exit code = %d stdout=%q", code, stdout)
	}
	for _, want := range []string{
		"SKIP shared/required (validation tier repo-required)",
		"PASS shared/certification",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout %q does not contain %q", stdout, want)
		}
	}
}

func TestRunScenarioTestsAllIncludesCertificationByDefault(t *testing.T) {
	t.Parallel()

	scenarioRoot := t.TempDir()
	writeScenarioScript(t, scenarioRoot, "shared/test-required.sh", "#!/bin/sh\nset -eu\nprintf 'required-ran\\n'\n")
	writeScenarioScript(t, scenarioRoot, "shared/test-certification.sh", "#!/bin/sh\nset -eu\nprintf 'certification-ran\\n'\n")
	writeScenarioScript(t, scenarioRoot, "shared/test-provider.sh", "#!/bin/sh\nset -eu\nprintf 'provider-ran\\n'\n")
	manifestPath := writeScenarioManifest(t, scenarioRoot, []map[string]any{
		{
			"id":                   "shared/required",
			"description":          "Repo-required fixture",
			"lane":                 "secretless",
			"platform":             "any",
			"validation_tier":      "repo-required",
			"manual":               false,
			"providers":            []string{"codex"},
			"persona":              "developer",
			"test_file":            "shared/test-required.sh",
			"requires_credentials": false,
		},
		{
			"id":                   "shared/certification",
			"description":          "Certification fixture",
			"lane":                 "secretless",
			"platform":             "any",
			"validation_tier":      "certification",
			"manual":               false,
			"providers":            []string{"codex"},
			"persona":              "developer",
			"test_file":            "shared/test-certification.sh",
			"requires_credentials": false,
		},
		{
			"id":                   "shared/provider",
			"description":          "Provider fixture",
			"lane":                 "provider-e2e",
			"platform":             "any",
			"validation_tier":      "repo-required",
			"manual":               false,
			"providers":            []string{"codex"},
			"persona":              "developer",
			"test_file":            "shared/test-provider.sh",
			"requires_credentials": true,
		},
	})

	code, stdout, _ := runScenarioScript(
		t,
		filepath.Join(repoRoot(t), "scripts", "run-scenario-tests.sh"),
		map[string]string{
			"WORKCELL_SCENARIO_ROOT":     scenarioRoot,
			"WORKCELL_SCENARIO_MANIFEST": manifestPath,
		},
		"--all",
	)
	if code != 0 {
		t.Fatalf("run-scenario-tests --all exit code = %d stdout=%q", code, stdout)
	}
	for _, want := range []string{
		"PASS shared/required",
		"PASS shared/certification",
		"PASS shared/provider",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout %q does not contain %q", stdout, want)
		}
	}
	// A passing scenario's own output stays quiet.
	if strings.Contains(stdout, "-ran") {
		t.Fatalf("stdout %q shows a passing scenario's output", stdout)
	}
}

func TestRunScenarioTestsFailsWhenManifestValidationFails(t *testing.T) {
	t.Parallel()

	scenarioRoot := t.TempDir()
	writeScenarioScript(t, scenarioRoot, "shared/test-secretless.sh", "#!/bin/sh\nexit 0\n")
	manifestPath := writeScenarioManifest(t, scenarioRoot, []map[string]any{
		{
			"id":                   "shared/duplicate",
			"description":          "First duplicate",
			"lane":                 "secretless",
			"platform":             "any",
			"manual":               false,
			"providers":            []string{"codex"},
			"persona":              "developer",
			"test_file":            "shared/test-secretless.sh",
			"requires_credentials": false,
		},
		{
			"id":                   "shared/duplicate",
			"description":          "Second duplicate",
			"lane":                 "secretless",
			"platform":             "any",
			"manual":               false,
			"providers":            []string{"codex"},
			"persona":              "developer",
			"test_file":            "shared/test-secretless.sh",
			"requires_credentials": false,
		},
	})

	code, stdout, _ := runScenarioScript(
		t,
		filepath.Join(repoRoot(t), "scripts", "run-scenario-tests.sh"),
		map[string]string{
			"WORKCELL_SCENARIO_ROOT":     scenarioRoot,
			"WORKCELL_SCENARIO_MANIFEST": manifestPath,
		},
	)
	if code != 1 {
		t.Fatalf("run-scenario-tests exit code = %d stdout=%q", code, stdout)
	}
	if !strings.Contains(stdout, "duplicate scenario id") {
		t.Fatalf("stdout %q does not contain manifest failure", stdout)
	}
	if strings.Contains(stdout, "Scenario tests passed") {
		t.Fatalf("stdout %q unexpectedly reported success", stdout)
	}
}

func TestRunScenarioTestsPrintsAFailedScenarioOutput(t *testing.T) {
	t.Parallel()

	scenarioRoot := t.TempDir()
	writeScenarioScript(t, scenarioRoot, "shared/test-passing.sh", "#!/bin/sh\nset -eu\necho passing-noise\necho passing-noise-err >&2\n")
	writeScenarioScript(t, scenarioRoot, "shared/test-failing.sh", "#!/bin/sh\nset -eu\ni=1\nwhile [ \"$i\" -le 250 ]; do echo \"failing-line-$i\"; i=$((i + 1)); done\necho failing-cause >&2\nexit 3\n")
	scenarios := []map[string]any{}
	for _, name := range []string{"failing", "passing"} {
		scenarios = append(scenarios, map[string]any{
			"id":                   "shared/" + name,
			"description":          name + " fixture",
			"lane":                 "secretless",
			"platform":             "any",
			"validation_tier":      "repo-required",
			"manual":               false,
			"providers":            []string{"codex"},
			"persona":              "developer",
			"test_file":            "shared/test-" + name + ".sh",
			"requires_credentials": false,
		})
	}
	manifestPath := writeScenarioManifest(t, scenarioRoot, scenarios)

	for _, jobs := range []string{"1", "2"} {
		code, stdout, _ := runScenarioScript(
			t,
			filepath.Join(repoRoot(t), "scripts", "run-scenario-tests.sh"),
			map[string]string{
				"WORKCELL_SCENARIO_ROOT":     scenarioRoot,
				"WORKCELL_SCENARIO_MANIFEST": manifestPath,
				"WORKCELL_SCENARIO_JOBS":     jobs,
			},
		)
		if code != 1 {
			t.Fatalf("jobs %s: run-scenario-tests exit code = %d stdout=%q", jobs, code, stdout)
		}
		header := "--- shared/failing output (exit 3; last 200 of 251 lines) ---\n"
		block := header + "failing-line-52\n"
		for _, want := range []string{"FAIL shared/failing", "PASS shared/passing", block, "failing-line-250\nfailing-cause\n--- end shared/failing output ---\n"} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("jobs %s: stdout %q does not contain %q", jobs, stdout, want)
			}
		}
		for _, unwanted := range []string{"failing-line-51\n", "passing-noise"} {
			if strings.Contains(stdout, unwanted) {
				t.Fatalf("jobs %s: stdout %q contains %q", jobs, stdout, unwanted)
			}
		}
		if strings.Index(stdout, header) > strings.Index(stdout, "Results: passed=1 failed=1") {
			t.Fatalf("jobs %s: the failed output follows the summary: %q", jobs, stdout)
		}
	}
}

// lockedBuffer collects a command's output while the test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

var (
	scenarioManifestToolOnce sync.Once
	errScenarioManifestTool  error
)

// buildScenarioManifestTool runs the manifest tool once, so that a deadline
// that starts after it does not include a cold go run build.
func buildScenarioManifestTool(tb testing.TB, manifestPath string) {
	tb.Helper()
	scenarioManifestToolOnce.Do(func() {
		cmd := exec.Command(filepath.Join(repoRoot(tb), "scripts", "lib", "scenario_manifest"), "list-tsv", manifestPath)
		cmd.Dir = repoRoot(tb)
		if output, err := cmd.CombinedOutput(); err != nil {
			errScenarioManifestTool = fmt.Errorf("scenario_manifest list-tsv: %w: %s", err, output)
		}
	})
	if errScenarioManifestTool != nil {
		tb.Fatal(errScenarioManifestTool)
	}
}

// processGone reports whether a process has exited. A killed process that
// init has not reaped yet shows as Z.
func processGone(pid string) bool {
	state, _ := exec.Command("ps", "-o", "stat=", "-p", pid).Output()
	s := strings.TrimSpace(string(state))
	return s == "" || strings.HasPrefix(s, "Z")
}

// runStoppedScenario starts the runner on a passing and a hung scenario,
// calls beforeSignal with the runner's PID once the passing scenario is
// recorded and the hung one runs, and then stops the runner with signal. It
// checks that the runner exits with code, prints the hung scenario's
// captured tail and nothing of a record that ran on, stops the scenario and
// its child, and removes its temporary files.
func runStoppedScenario(t *testing.T, jobs string, signal syscall.Signal, name string, code int, beforeSignal func(pid int)) {
	t.Helper()
	scenarioRoot := t.TempDir()
	tmpDir := t.TempDir()
	ready := filepath.Join(scenarioRoot, "ready")
	child := filepath.Join(scenarioRoot, "child")
	hungPID := filepath.Join(scenarioRoot, "hung-pid")
	writeScenarioScript(t, scenarioRoot, "shared/test-passing.sh", "#!/bin/sh\necho passing-noise\n")
	writeScenarioScript(t, scenarioRoot, "shared/test-hung.sh", "#!/bin/sh\necho hung-line-1\necho hung-line-2\necho hung-cause >&2\necho \"$$\" >\"$HUNG_PID\"\nsleep 30 &\necho \"$!\" >\"$HUNG_CHILD\"\n: >\"$HUNG_READY\"\nwait\n")
	scenarios := []map[string]any{}
	for _, scenarioName := range []string{"passing", "hung"} {
		scenarios = append(scenarios, map[string]any{
			"id": "shared/" + scenarioName, "description": scenarioName + " fixture", "lane": "secretless",
			"platform": "any", "validation_tier": "repo-required", "manual": false,
			"providers": []string{"codex"}, "persona": "developer",
			"test_file": "shared/test-" + scenarioName + ".sh", "requires_credentials": false,
		})
	}
	manifestPath := writeScenarioManifest(t, scenarioRoot, scenarios)
	buildScenarioManifestTool(t, manifestPath)
	cmd := exec.Command(filepath.Join(repoRoot(t), "scripts", "run-scenario-tests.sh"))
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(),
		"WORKCELL_SCENARIO_ROOT="+scenarioRoot,
		"WORKCELL_SCENARIO_MANIFEST="+manifestPath,
		"WORKCELL_SCENARIO_JOBS="+jobs,
		"HUNG_READY="+ready,
		"HUNG_CHILD="+child,
		"HUNG_PID="+hungPID,
		"TMPDIR="+tmpDir)
	var out lockedBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	// The passing scenario must be recorded before the signal, or the runner
	// rightly reports it as stopped too.
	const startTimeout = 20 * time.Second
	deadline := time.Now().Add(startTimeout)
	for {
		_, err := os.Stat(ready)
		if err == nil && strings.Contains(out.String(), "PASS shared/passing\n") {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("within %s of the start, the hung scenario had not signalled that it runs (%v) or the passing one had not passed: %q", startTimeout, err, out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if beforeSignal != nil {
		beforeSignal(cmd.Process.Pid)
	}
	if err := cmd.Process.Signal(signal); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != code {
			t.Fatalf("runner exit = %v, want %d; output %q", err, code, out.String())
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("the runner did not stop on %s: %q", name, out.String())
	}
	// The runner reaps the scenario and kills its process group before it
	// exits; init reaps the child.
	for _, pidFile := range []string{hungPID, child} {
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			t.Fatal(err)
		}
		pid := strings.TrimSpace(string(raw))
		for deadline := time.Now().Add(3 * time.Second); !processGone(pid); time.Sleep(50 * time.Millisecond) {
			if time.Now().After(deadline) {
				_ = exec.Command("kill", "-9", pid).Run()
				t.Fatalf("the hung scenario's process %s (%s) still runs after %s", pid, filepath.Base(pidFile), name)
			}
		}
	}
	got := out.String()
	want := "--- shared/hung output (stopped by " + name + "; 3 lines) ---\nhung-line-1\nhung-line-2\nhung-cause\n--- end shared/hung output ---\n"
	if !strings.Contains(got, "STOPPED shared/hung\n"+want) || strings.Contains(got, "passing-noise") {
		t.Fatalf("output %q, want the hung scenario block %q and no passing output", got, want)
	}
	// A record that outlives the stop writes FAIL and its result files while
	// the exit trap removes the results directory.
	for _, unwanted := range []string{"FAIL shared/hung", "cannot remove", "No such file"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("output %q contains %q: the hung scenario's record ran on after %s", got, unwanted, name)
		}
	}
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		t.Errorf("the runner left %s in TMPDIR after %s", entry.Name(), name)
	}
}

// TestRunScenarioTestsPrintsAStoppedScenarioOutput stops the runner while a
// scenario hangs, as a CI timeout, a Ctrl-C or a closed terminal does. The
// runner must print the captured tail of the hung scenario, and stop the
// scenario and its children.
func TestRunScenarioTestsPrintsAStoppedScenarioOutput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		signal syscall.Signal
		name   string
		code   int
	}{{syscall.SIGTERM, "SIGTERM", 143}, {syscall.SIGINT, "SIGINT", 130}, {syscall.SIGHUP, "SIGHUP", 129}} {
		for _, jobs := range []string{"1", "2"} {
			t.Run("jobs "+jobs+" "+tc.name, func(t *testing.T) {
				t.Parallel()
				runStoppedScenario(t, jobs, tc.signal, tc.name, tc.code, nil)
			})
		}
	}
}

// stopTestScenario is a scenario fixture for startScenarioRunner.
type stopTestScenario struct {
	name string
	body string
}

// startScenarioRunner starts the runner on fixtures, in manifest order, with
// env added, and waits until a fixture has created $HUNG_READY. It returns the
// runner, its output and a channel that gets its Wait result.
func startScenarioRunner(t *testing.T, fixtures []stopTestScenario, env ...string) (*exec.Cmd, *lockedBuffer, <-chan error, string) {
	t.Helper()
	scenarioRoot := t.TempDir()
	ready := filepath.Join(scenarioRoot, "ready")
	scenarios := []map[string]any{}
	for _, fixture := range fixtures {
		writeScenarioScript(t, scenarioRoot, "shared/test-"+fixture.name+".sh", fixture.body)
		scenarios = append(scenarios, map[string]any{
			"id": "shared/" + fixture.name, "description": fixture.name + " fixture", "lane": "secretless",
			"platform": "any", "validation_tier": "repo-required", "manual": false,
			"providers": []string{"codex"}, "persona": "developer",
			"test_file": "shared/test-" + fixture.name + ".sh", "requires_credentials": false,
		})
	}
	manifestPath := writeScenarioManifest(t, scenarioRoot, scenarios)
	buildScenarioManifestTool(t, manifestPath)
	cmd := exec.Command(filepath.Join(repoRoot(t), "scripts", "run-scenario-tests.sh"))
	cmd.Dir = repoRoot(t)
	cmd.Env = append(append(os.Environ(),
		"WORKCELL_SCENARIO_ROOT="+scenarioRoot,
		"WORKCELL_SCENARIO_MANIFEST="+manifestPath,
		"HUNG_READY="+ready,
		"SCENARIO_DIR="+scenarioRoot,
		"TMPDIR="+t.TempDir()), env...)
	out := &lockedBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	const startTimeout = 20 * time.Second
	for deadline := time.Now().Add(startTimeout); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("within %s of the start, no scenario had signalled that it runs: %q", startTimeout, out.String())
		}
	}
	time.Sleep(100 * time.Millisecond)
	return cmd, out, done, scenarioRoot
}

// waitRunnerExit waits up to timeout for the runner to exit with code.
func waitRunnerExit(t *testing.T, cmd *exec.Cmd, out *lockedBuffer, done <-chan error, code int, timeout time.Duration) {
	t.Helper()
	select {
	case err := <-done:
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != code {
			t.Fatalf("runner exit = %v, want %d; output %q", err, code, out.String())
		}
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		t.Fatalf("the runner did not exit within %s: %q", timeout, out.String())
	}
}

// TestRunScenarioTestsGivesAStoppedScenarioTimeToCleanUp stops the runner
// while a scenario runs. The scenario's cleanup trap gets the grace to finish,
// a second signal or the end of WORKCELL_SCENARIO_STOP_GRACE ends it, and a
// scenario that failed before the stop still has its output printed.
func TestRunScenarioTestsGivesAStoppedScenarioTimeToCleanUp(t *testing.T) {
	t.Parallel()

	// The cleanup trap runs when SIGTERM ends the scenario.
	cleanupBody := func(seconds string) string {
		return "#!/bin/bash\necho \"$$\" >\"$SCENARIO_DIR/pid\"\ncleanup() { echo cleanup-start; sleep " + seconds + "; echo cleaned >\"$SCENARIO_DIR/marker\"; }\ntrap cleanup EXIT\n: >\"$HUNG_READY\"\nsleep 30\n"
	}
	scenarioGone := func(t *testing.T, scenarioRoot string) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(scenarioRoot, "pid"))
		if err != nil {
			t.Fatal(err)
		}
		pid := strings.TrimSpace(string(raw))
		for deadline := time.Now().Add(3 * time.Second); !processGone(pid); time.Sleep(50 * time.Millisecond) {
			if time.Now().After(deadline) {
				_ = exec.Command("kill", "-9", pid).Run()
				t.Fatalf("the scenario %s still runs after the stop", pid)
			}
		}
	}

	t.Run("cleanup finishes on one signal", func(t *testing.T) {
		t.Parallel()
		cmd, out, done, scenarioRoot := startScenarioRunner(t, []stopTestScenario{{"cleanup", cleanupBody("2")}})
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		waitRunnerExit(t, cmd, out, done, 143, 20*time.Second)
		if _, err := os.Stat(filepath.Join(scenarioRoot, "marker")); err != nil {
			t.Fatalf("the cleanup trap did not finish: %v; output %q", err, out.String())
		}
		if got := out.String(); !strings.Contains(got, "Waiting up to 60s for scenario cleanup") || !strings.Contains(got, "STOPPED shared/cleanup\n") {
			t.Fatalf("output %q lacks the wait notice or the STOPPED block", got)
		}
		scenarioGone(t, scenarioRoot)
	})

	// A second signal of the same kind, as a second Ctrl-C, kills at once.
	for _, tc := range []struct {
		signal syscall.Signal
		name   string
		code   int
	}{{syscall.SIGINT, "SIGINT", 130}, {syscall.SIGTERM, "SIGTERM", 143}} {
		t.Run("a second "+tc.name+" kills at once", func(t *testing.T) {
			t.Parallel()
			cmd, out, done, scenarioRoot := startScenarioRunner(t, []stopTestScenario{{"cleanup", cleanupBody("30")}})
			if err := cmd.Process.Signal(tc.signal); err != nil {
				t.Fatal(err)
			}
			for deadline := time.Now().Add(10 * time.Second); !strings.Contains(out.String(), "Waiting up to"); time.Sleep(20 * time.Millisecond) {
				if time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					t.Fatalf("the runner did not wait for the cleanup: %q", out.String())
				}
			}
			if err := cmd.Process.Signal(tc.signal); err != nil {
				t.Fatal(err)
			}
			waitRunnerExit(t, cmd, out, done, tc.code, 5*time.Second)
			if _, err := os.Stat(filepath.Join(scenarioRoot, "marker")); err == nil {
				t.Fatalf("the cleanup trap finished although a second signal came: %q", out.String())
			}
			scenarioGone(t, scenarioRoot)
		})
	}

	// The scenario stops the runner as it starts. A runner that blocked in
	// wait for a running scenario would miss a signal whose trap ran just
	// before that wait; no test here can time a signal into that gap, so this
	// one shows only that a stop from a running serial scenario is acted on.
	t.Run("a signal as a serial scenario starts stops it", func(t *testing.T) {
		t.Parallel()
		body := "#!/bin/bash\necho \"$$\" >\"$SCENARIO_DIR/pid\"\n: >\"$HUNG_READY\"\nkill -TERM \"$PPID\"\nsleep 30\n"
		cmd, out, done, scenarioRoot := startScenarioRunner(t, []stopTestScenario{{"selfstop", body}}, "WORKCELL_SCENARIO_JOBS=1")
		waitRunnerExit(t, cmd, out, done, 143, 10*time.Second)
		if got := out.String(); !strings.Contains(got, "STOPPED shared/selfstop\n") {
			t.Fatalf("output %q lacks the STOPPED block", got)
		}
		scenarioGone(t, scenarioRoot)
	})

	t.Run("the grace ends for a scenario that ignores SIGTERM", func(t *testing.T) {
		t.Parallel()
		body := "#!/bin/bash\ntrap '' TERM INT HUP\necho \"$$\" >\"$SCENARIO_DIR/pid\"\n: >\"$HUNG_READY\"\nwhile :; do sleep 1; done\n"
		cmd, out, done, scenarioRoot := startScenarioRunner(t, []stopTestScenario{{"stubborn", body}}, "WORKCELL_SCENARIO_STOP_GRACE=1")
		if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
			t.Fatal(err)
		}
		waitRunnerExit(t, cmd, out, done, 129, 10*time.Second)
		if got := out.String(); !strings.Contains(got, "Waiting up to 1s for scenario cleanup") || !strings.Contains(got, "STOPPED shared/stubborn\n") {
			t.Fatalf("output %q lacks the wait notice or the STOPPED block", got)
		}
		scenarioGone(t, scenarioRoot)
	})

	for _, jobs := range []string{"1", "2"} {
		t.Run("jobs "+jobs+" prints a failed scenario's output on a stop", func(t *testing.T) {
			t.Parallel()
			fixtures := []stopTestScenario{
				{"failing", "#!/bin/sh\necho failing-cause\nexit 3\n"},
				{"hung", "#!/bin/sh\necho \"$$\" >\"$SCENARIO_DIR/pid\"\n: >\"$HUNG_READY\"\nsleep 30\n"},
			}
			if jobs == "2" {
				// The hung scenario starts first, and the failing one ends
				// while the runner waits for it.
				fixtures[0], fixtures[1] = fixtures[1], fixtures[0]
			}
			cmd, out, done, scenarioRoot := startScenarioRunner(t, fixtures, "WORKCELL_SCENARIO_JOBS="+jobs)
			for deadline := time.Now().Add(10 * time.Second); !strings.Contains(out.String(), "FAIL shared/failing\n"); time.Sleep(20 * time.Millisecond) {
				if time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					t.Fatalf("the failing scenario was not recorded: %q", out.String())
				}
			}
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			waitRunnerExit(t, cmd, out, done, 143, 10*time.Second)
			got := out.String()
			failed := strings.Index(got, "--- shared/failing output (exit 3; 1 lines) ---\nfailing-cause\n--- end shared/failing output ---\n")
			stopped := strings.Index(got, "STOPPED shared/hung\n")
			if failed < 0 || stopped < 0 || failed > stopped {
				t.Fatalf("output %q, want the failing scenario's block before the STOPPED block", got)
			}
			scenarioGone(t, scenarioRoot)
		})
	}
}
