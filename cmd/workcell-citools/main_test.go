// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWrongArityExitsWithUsageCode(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestCitoolsHelperProcess", "--", "tree-compare", "only-one-root")
	cmd.Env = append(os.Environ(), "WORKCELL_CITOOLS_HELPER_PROCESS=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("workcell-citools tree-compare with one arg exited 0")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("cmd.Run() error = %T %v, want ExitError", err, err)
	}
	if exitErr.ExitCode() != 2 {
		t.Fatalf("exit code = %d, want 2; stderr=%q", exitErr.ExitCode(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("stderr = %q, want usage text", stderr.String())
	}
}

func TestNoArgsExitsWithUsageCode(t *testing.T) {
	assertCitoolsUsageExit(t)
}

func TestUnknownCommandExitsWithUsageCode(t *testing.T) {
	assertCitoolsUsageExit(t, "definitely-not-a-subcommand")
}

func TestUpstreamFetchCommandsRejectWrongArity(t *testing.T) {
	assertCitoolsUsageExit(t, "github-api-get")
	assertCitoolsUsageExit(t, "github-release-asset", "owner/repository", "asset")
	assertCitoolsUsageExit(t, "upstream-get", "profile", "one", "two", "three")
}

// assertCitoolsUsageExit runs the binary (via the helper-process trick) with
// the given argv tail and asserts a usage exit (code 2 + "usage:" on stderr).
func assertCitoolsUsageExit(t *testing.T, argv ...string) {
	t.Helper()
	code, stderr := runCitools(t, argv...)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "usage:") && !strings.Contains(stderr, "unknown command") {
		t.Fatalf("stderr = %q, want usage or unknown-command text", stderr)
	}
}

// runCitools runs the binary (via the helper-process trick) with the given
// argv tail and returns its exit code and captured stderr.
func runCitools(t *testing.T, argv ...string) (int, string) {
	t.Helper()
	runArgs := append([]string{"-test.run=TestCitoolsHelperProcess", "--"}, argv...)
	cmd := exec.Command(os.Args[0], runArgs...)
	cmd.Env = append(os.Environ(), "WORKCELL_CITOOLS_HELPER_PROCESS=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return 0, stderr.String()
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("cmd.Run() error = %T %v, want ExitError", err, err)
	}
	return exitErr.ExitCode(), stderr.String()
}

// TestWorkcellCheckBatchStopsAtFirstFailureWithByteIdenticalStderr proves the
// batch runs checks in argv order and that the first failure's exit code and
// stderr are byte-identical to running the failing check's individual
// subcommand: against an empty root both listed checks fail with different
// messages, so whichever is listed first must own the batch's entire stderr.
func TestWorkcellCheckBatchStopsAtFirstFailureWithByteIdenticalStderr(t *testing.T) {
	emptyRoot := t.TempDir()
	hookExec := "workcell-precommit-hook-exec"
	pinGate := "workcell-precommit-upstream-pin-gate"

	hookExecCode, hookExecStderr := runCitools(t, hookExec, emptyRoot)
	pinGateCode, pinGateStderr := runCitools(t, pinGate, emptyRoot)
	if hookExecCode != 1 || pinGateCode != 1 {
		t.Fatalf("individual exit codes = %d/%d, want 1/1", hookExecCode, pinGateCode)
	}
	if hookExecStderr == pinGateStderr {
		t.Fatalf("individual checks share stderr %q; cannot prove ordering", hookExecStderr)
	}

	tests := []struct {
		name   string
		checks []string
		want   string
	}{
		{"hook-exec first", []string{hookExec, pinGate}, hookExecStderr},
		{"pin-gate first", []string{pinGate, hookExec}, pinGateStderr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			batchArgs := append([]string{"workcell-check-batch", emptyRoot}, tt.checks...)
			batchCode, batchStderr := runCitools(t, batchArgs...)
			if batchCode != 1 {
				t.Fatalf("batch exit code = %d, want 1; stderr=%q", batchCode, batchStderr)
			}
			if batchStderr != tt.want {
				t.Fatalf("batch stderr = %q, want first failure's individual stderr %q", batchStderr, tt.want)
			}
		})
	}
}

// TestWorkcellCheckBatchSettingsPathFormAndSuccess runs a mixed batch (both
// SETTINGS_PATH forms plus a ROOT_DIR check) against the real repo and
// requires a clean exit.
func TestWorkcellCheckBatchSettingsPathFormAndSuccess(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(repoRoot, "adapters", "claude", ".claude", "settings.json")
	code, stderr := runCitools(t, "workcell-check-batch", repoRoot,
		"workcell-claude-mcp-project-servers="+settings,
		"workcell-claude-guard-bash-hook="+settings,
		"workcell-precommit-hook-exec")
	if code != 0 {
		t.Fatalf("batch exit code = %d, want 0; stderr=%q", code, stderr)
	}
}

// TestWorkcellCheckBatchUsageErrors covers the malformed invocations that must
// exit 2 with usage text: an unknown check, a non-batchable subcommand, a
// ROOT_DIR check given =ARG, a SETTINGS_PATH check missing =ARG, and a missing
// check list.
func TestWorkcellCheckBatchUsageErrors(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name string
		argv []string
	}{
		{"unknown check", []string{"workcell-check-batch", root, "definitely-not-a-check"}},
		{"non-batchable subcommand", []string{"workcell-check-batch", root, "tree-compare"}},
		{"root-dir check with =ARG", []string{"workcell-check-batch", root, "workcell-precommit-hook-exec=" + root}},
		{"settings-path check without =ARG", []string{"workcell-check-batch", root, "workcell-claude-mcp-project-servers"}},
		{"missing check list", []string{"workcell-check-batch", root}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertCitoolsUsageExit(t, tt.argv...)
		})
	}
}

func TestCitoolsHelperProcess(t *testing.T) {
	if os.Getenv("WORKCELL_CITOOLS_HELPER_PROCESS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"workcell-citools"}, os.Args[i+1:]...)
			main()
			os.Exit(0)
		}
	}
	os.Exit(2)
}

// TestHardeningProfileConformanceParsesSidecarCommand proves the production
// gate runs the parsed sidecar check: a flag the profile scan still finds as
// text, because a longer flag contains it, must fail the gate. The same holds for the
// sole-route function.
func TestHardeningProfileConformanceParsesSidecarCommand(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"policy/hardening-profile.toml",
		"scripts/workcell",
		"scripts/lib/launcher/egress-endpoints.sh",
		"scripts/lib/launcher/generated-adapters.sh",
	} {
		data, err := os.ReadFile(filepath.Join("..", "..", rel))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := cmdHardeningProfileConformance([]string{root}); err != nil {
		t.Fatalf("real tree rejected: %v", err)
	}
	script := filepath.Join(root, "scripts", "lib", "launcher", "egress-endpoints.sh")
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(string) string{
		"sidecar flag": func(s string) string { return strings.Replace(s, "    --read-only \\\n", "    --read-only-x \\\n", 1) },
		"sole route decoy": func(s string) string {
			route := `RUNTIME_NETWORK_ARGS=(--network "${EGRESS_PROXY_NETWORK}" --dns 127.0.0.1)`
			return strings.Replace(s, route, "echo '"+route+"'\n  RUNTIME_NETWORK_ARGS=(--network bridge)", 1)
		},
	} {
		mutated := mutate(string(data))
		if mutated == string(data) {
			t.Fatalf("%s: mutation changed nothing", name)
		}
		if err := os.WriteFile(script, []byte(mutated), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := cmdHardeningProfileConformance([]string{root}); err == nil || !strings.Contains(err.Error(), "egress proxy") {
			t.Fatalf("%s: error = %v, want the parsed egress proxy check to fail", name, err)
		}
	}
}
