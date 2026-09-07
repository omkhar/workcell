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

// TestVerifyReleaseOutputsRejectsSymlinkedAssetsDir covers a fail-open path in
// scripts/verify-release-outputs.sh. `find` emits no children for a
// command-line symlink, so a symlinked --assets-dir skipped the directory
// inventory walk entirely, while every per-asset `-f && ! -L` check still
// resolved through the symlinked parent and passed. The verifier exited 0 for a
// release directory holding an unexpected file. The run must fail closed on the
// symlink itself, before any signature check.
func TestVerifyReleaseOutputsRejectsSymlinkedAssetsDir(t *testing.T) {
	t.Parallel()
	script := filepath.Join(repoRoot(t), "scripts", "verify-release-outputs.sh")
	digest := strings.Repeat("c", 40)
	assets := t.TempDir()
	link := filepath.Join(t.TempDir(), "assets-link")
	if err := os.Symlink(assets, link); err != nil {
		t.Fatal(err)
	}

	run := func(dir string) (int, string) {
		t.Helper()
		cmd := exec.Command(script,
			"--assets-dir", dir,
			"--repo", "omkhar/workcell",
			"--tag", "v1.2.3",
			"--image-repository", "ghcr.io/omkhar/workcell",
			"--source-digest", digest,
			"--workflow-digest", digest,
		)
		cmd.Env = []string{"BASH_ENV=", "ENV="}
		out, err := cmd.CombinedOutput()
		if err == nil {
			return 0, string(out)
		}
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running release-output verifier: %v\n%s", err, out)
		}
		return exitErr.ExitCode(), string(out)
	}

	const rejection = "assets directory must not be a symlink"
	if code, out := run(link); code == 0 || !strings.Contains(out, rejection) {
		t.Fatalf("expected symlinked assets directory rejection, got %d\n%s", code, out)
	}

	// Negative control: the same directory reached by its real path must clear
	// the symlink gate, so the check above rejects the symlink rather than
	// anything else about the fixture.
	if _, out := run(assets); strings.Contains(out, rejection) {
		t.Fatalf("non-symlinked assets directory was rejected as a symlink\n%s", out)
	}
}
