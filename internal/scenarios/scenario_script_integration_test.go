// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package scenarios

import (
	"encoding/json"
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
