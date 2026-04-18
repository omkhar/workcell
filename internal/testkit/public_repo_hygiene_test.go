// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writePublicRepoHygieneFixture(t *testing.T, readme string) string {
	t.Helper()

	root := t.TempDir()
	for _, dir := range []string{
		filepath.Join(root, "scripts"),
		filepath.Join(root, ".agents"),
		filepath.Join(root, "docs"),
		filepath.Join(root, "man"),
		filepath.Join(root, "policy"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	scriptBytes, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "check-public-repo-hygiene.sh"))
	if err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(root, "scripts", "check-public-repo-hygiene.sh")
	if err := os.WriteFile(scriptPath, scriptBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}

	return scriptPath
}

func TestCheckPublicRepoHygieneRejectsBareAbsoluteHomePath(t *testing.T) {
	t.Parallel()

	scriptPath := writePublicRepoHygieneFixture(t, "Leaked maintainer path: /Users/alice")
	cmd := exec.Command("/bin/bash", scriptPath)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("check-public-repo-hygiene unexpectedly accepted a bare absolute home path")
	}
	if !strings.Contains(string(output), "machine-specific absolute home paths") {
		t.Fatalf("unexpected output: %s", output)
	}
	if !strings.Contains(string(output), "/Users/alice") {
		t.Fatalf("output did not include the leaked path: %s", output)
	}
}

func TestCheckPublicRepoHygieneAllowsExamplePlaceholderPath(t *testing.T) {
	t.Parallel()

	scriptPath := writePublicRepoHygieneFixture(t, "Portable example path: /Users/example")
	cmd := exec.Command("/bin/bash", scriptPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("check-public-repo-hygiene rejected the example placeholder: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Public repo hygiene check passed.") {
		t.Fatalf("unexpected output: %s", output)
	}
}
