// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runReleaseOutputsInventoryGuard runs the shipped release-output verifier
// against one assets directory and reports its exit status and combined output.
//
// The run goes through a driver that sources the verifier and defines cosign as
// a shell function. The verifier pins PATH to a fixed trusted list, so a stub
// cannot be placed on PATH, but a function satisfies its `command -v cosign`
// precondition on a machine without cosign installed. The stub is never called:
// both guards under test reject before any signature verification.
func runReleaseOutputsInventoryGuard(t *testing.T, dir string) (int, string) {
	t.Helper()
	driver := filepath.Join(t.TempDir(), "release-outputs-inventory-driver.sh")
	script := fmt.Sprintf("#!/bin/bash\nsource %s\ncosign() { return 0; }\nmain \"$@\"\n",
		ShellQuote(filepath.Join(repoRoot(t), "scripts", "verify-release-outputs.sh")))
	if err := os.WriteFile(driver, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	digest := strings.Repeat("c", 40)
	cmd := exec.Command(driver,
		"--assets-dir", dir,
		"--repo", "omkhar/workcell",
		"--tag", "v1.2.3",
		"--image-repository", "ghcr.io/omkhar/workcell",
		"--source-digest", digest,
		"--workflow-digest", digest,
	)
	// Bash startup files are cleared so caller state cannot reach the verifier.
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

// TestVerifyReleaseOutputsRejectsSymlinkedAssetsDir covers a fail-open path in
// scripts/verify-release-outputs.sh. `find` emits no children for a
// command-line symlink, so a symlinked --assets-dir skipped the directory
// inventory walk entirely, while every per-asset `-f && ! -L` check still
// resolved through the symlinked parent and passed. The verifier exited 0 for a
// release directory holding an unexpected file. The run must fail closed on the
// symlink itself, before any signature check.
func TestVerifyReleaseOutputsRejectsSymlinkedAssetsDir(t *testing.T) {
	t.Parallel()
	assets := t.TempDir()
	link := filepath.Join(t.TempDir(), "assets-link")
	if err := os.Symlink(assets, link); err != nil {
		t.Fatal(err)
	}

	const rejection = "assets directory must not be a symlink"
	if code, out := runReleaseOutputsInventoryGuard(t, link); code == 0 || !strings.Contains(out, rejection) {
		t.Fatalf("expected symlinked assets directory rejection, got %d\n%s", code, out)
	}

	// Negative control: the same directory reached by its real path must clear
	// the symlink gate and reach the walk, so the check above rejects the
	// symlink rather than anything else about the fixture. Reaching the walk on
	// an empty directory also exercises the count assertion, which is what
	// catches a walk that completed successfully having seen nothing -- the
	// state a symlinked directory produces once the gate above is removed.
	if code, out := runReleaseOutputsInventoryGuard(t, assets); code == 0 ||
		!strings.Contains(out, "release directory listing is incomplete: read 0 of") {
		t.Fatalf("expected the real path to reach the inventory count check, got %d\n%s", code, out)
	}
}

// TestVerifyReleaseOutputsRejectsUnlistableAssetsDir covers a second fail-open
// in the same walk. `find` runs in a process substitution, whose nonzero exit
// Bash does not propagate, so a directory that permits traversal but not
// listing left the inventory loop with nothing to read while every per-asset
// check still opened the names it already expected. The verifier exited 0 with
// an unexpected file present but unseen. The walk must now observe `find`
// running to completion rather than inferring success from what it emitted.
func TestVerifyReleaseOutputsRejectsUnlistableAssetsDir(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permission bits, so enumeration cannot be denied")
	}
	assets := t.TempDir()
	// One decoy file: enumerable, the walk rejects it by name; unenumerable, it
	// is exactly what the fail-open used to hide.
	if err := os.WriteFile(filepath.Join(assets, "attacker.bin"), []byte("payload\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Negative control first, while the directory is still readable: the walk
	// sees the decoy and rejects it by name, proving the fixture is enumerable
	// and the assertion below is not passing on an inert path.
	if code, out := runReleaseOutputsInventoryGuard(t, assets); code == 0 || !strings.Contains(out, "unexpected release file: attacker.bin") {
		t.Fatalf("expected the readable fixture to be rejected by name, got %d\n%s", code, out)
	}

	// Execute-only: traversal succeeds, enumeration is denied.
	if err := os.Chmod(assets, 0o111); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(assets, 0o755) })

	// The walk must reject because `find` did not run to completion, not because
	// of what it managed to emit before failing.
	const rejection = "release directory listing failed"
	code, out := runReleaseOutputsInventoryGuard(t, assets)
	if code == 0 || !strings.Contains(out, rejection) {
		t.Fatalf("expected an unlistable assets directory to fail closed, got %d\n%s", code, out)
	}
}
