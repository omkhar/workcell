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

func TestRunScenarioTestsSecretlessOnlySkipsNonSecretlessEntries(t *testing.T) {
	t.Parallel()

	scenarioRoot := t.TempDir()
	writeScenarioScript(t, scenarioRoot, "shared/test-secretless.sh", "#!/bin/sh\nset -eu\nprintf 'secretless-ran\\n'\n")
	writeScenarioScript(t, scenarioRoot, "shared/test-provider.sh", "#!/bin/sh\nset -eu\necho provider-ran >&2\nexit 1\n")
	writeScenarioScript(t, scenarioRoot, "shared/test-manual.sh", "#!/bin/sh\nset -eu\necho manual-ran >&2\nexit 1\n")
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
		"--secretless-only",
	)
	if code != 0 {
		t.Fatalf("run-scenario-tests exit code = %d stdout=%q", code, stdout)
	}
	for _, want := range []string{
		"secretless-ran",
		"PASS shared/secretless",
		"SKIP shared/provider (lane provider-e2e)",
		"SKIP shared/manual (manual lane)",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout %q does not contain %q", stdout, want)
		}
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
