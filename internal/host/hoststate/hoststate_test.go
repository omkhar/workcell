// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package hoststate

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDirectMountCacheKeyMatchesNULTerminatedHash(t *testing.T) {
	t.Parallel()
	got := DirectMountCacheKey("/host/auth.json", "/opt/workcell/host-inputs/credentials/codex-auth.json")

	sum := sha256.Sum256([]byte("/host/auth.json\x00/opt/workcell/host-inputs/credentials/codex-auth.json\x00"))
	want := hex.EncodeToString(sum[:8])
	if got != want {
		t.Fatalf("DirectMountCacheKey() = %q, want %q", got, want)
	}
}

func TestCleanupStaleLatestLogPointersSupportsTargetAndLegacyRoots(t *testing.T) {
	t.Parallel()

	scratchRoot := t.TempDir()
	targetRoot := filepath.Join(scratchRoot, "state-root")
	legacyRoot := filepath.Join(scratchRoot, "legacy-root")
	targetProfileDir := filepath.Join(targetRoot, "targets", "local_vm", "colima", "wcl-target")
	legacyProfileDir := filepath.Join(legacyRoot, "wcl-legacy")
	if err := os.MkdirAll(targetProfileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(legacyProfileDir, 0o755); err != nil {
		t.Fatal(err)
	}

	existingTarget := filepath.Join(scratchRoot, "existing-debug.log")
	if err := os.WriteFile(existingTarget, []byte("debug\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	targetPointer := filepath.Join(targetProfileDir, "workcell.latest-debug-log")
	legacyPointer := filepath.Join(legacyProfileDir, "workcell.latest-transcript-log")
	if err := os.WriteFile(targetPointer, []byte(existingTarget+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPointer, []byte(filepath.Join(scratchRoot, "missing-transcript.log")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := CleanupStaleLatestLogPointers(targetRoot); err != nil {
		t.Fatalf("CleanupStaleLatestLogPointers(target) error = %v", err)
	}
	if err := CleanupStaleLatestLogPointers(legacyRoot); err != nil {
		t.Fatalf("CleanupStaleLatestLogPointers(legacy) error = %v", err)
	}

	if _, err := os.Stat(targetPointer); err != nil {
		t.Fatalf("target pointer should remain: %v", err)
	}
	if _, err := os.Stat(legacyPointer); !os.IsNotExist(err) {
		t.Fatalf("legacy pointer should be removed, err = %v", err)
	}
}

func TestCleanupStaleSessionAuditDirsSupportsTargetAndLegacyRoots(t *testing.T) {
	t.Parallel()

	scratchRoot := t.TempDir()
	targetRoot := filepath.Join(scratchRoot, "state-root")
	legacyRoot := filepath.Join(scratchRoot, "legacy-root")
	targetProfileDir := filepath.Join(targetRoot, "targets", "local_vm", "colima", "wcl-target")
	legacyProfileDir := filepath.Join(legacyRoot, "wcl-legacy")
	if err := os.MkdirAll(targetProfileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(legacyProfileDir, 0o755); err != nil {
		t.Fatal(err)
	}

	targetStale := filepath.Join(targetProfileDir, "session-audit.stale")
	targetRecent := filepath.Join(targetProfileDir, "session-audit.recent")
	legacyStale := filepath.Join(legacyProfileDir, "session-audit.stale")
	legacyRecent := filepath.Join(legacyProfileDir, "session-audit.recent")
	for _, dir := range []string{targetStale, targetRecent, legacyStale, legacyRecent} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	old := time.Now().Add(-13 * time.Hour)
	for _, dir := range []string{targetStale, legacyStale} {
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
	}

	if err := CleanupStaleSessionAuditDirs(targetRoot); err != nil {
		t.Fatalf("CleanupStaleSessionAuditDirs(target) error = %v", err)
	}
	if err := CleanupStaleSessionAuditDirs(legacyRoot); err != nil {
		t.Fatalf("CleanupStaleSessionAuditDirs(legacy) error = %v", err)
	}

	for _, dir := range []string{targetStale, legacyStale} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("stale session-audit dir should be removed: %s err=%v", dir, err)
		}
	}
	for _, dir := range []string{targetRecent, legacyRecent} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("recent session-audit dir should remain: %s err=%v", dir, err)
		}
	}
}

func TestCleanupStaleInjectionBundlesRemovesCopilotTokenSidecar(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	bundleName := "workcell-injections.fixture"
	bundleDir := filepath.Join(root, bundleName)
	mountSidecar := filepath.Join(root, bundleName+".mounts.json")
	tokenSidecar := filepath.Join(root, bundleName+".copilot-token.env.fixture")
	tokenHandoffDir := filepath.Join(root, bundleName+".copilot-token-handoff.fixture")

	if err := os.MkdirAll(bundleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tokenHandoffDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mountSidecar, []byte("[]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenSidecar, []byte("WORKCELL_COPILOT_GITHUB_TOKEN=test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tokenHandoffDir, "copilot-github-token.txt"), []byte("test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-13 * time.Hour)
	for _, path := range []string{bundleDir, mountSidecar, tokenSidecar, tokenHandoffDir} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}

	if err := CleanupStaleInjectionBundles(root); err != nil {
		t.Fatalf("CleanupStaleInjectionBundles() error = %v", err)
	}
	for _, path := range []string{bundleDir, mountSidecar, tokenSidecar, tokenHandoffDir} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("stale injection artifact should be removed: %s err=%v", path, err)
		}
	}
}

func TestCleanupStaleInjectionBundlesConservativelyHandlesOrphanCopilotTokenSidecars(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	staleTokenSidecar := filepath.Join(root, "workcell-injections.stale.copilot-token.env.fixture")
	recentTokenSidecar := filepath.Join(root, "workcell-injections.recent.copilot-token.env.fixture")
	for _, path := range []string{staleTokenSidecar, recentTokenSidecar} {
		if err := os.WriteFile(path, []byte("WORKCELL_COPILOT_GITHUB_TOKEN=test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-13 * time.Hour)
	if err := os.Chtimes(staleTokenSidecar, old, old); err != nil {
		t.Fatal(err)
	}

	if err := CleanupStaleInjectionBundles(root); err != nil {
		t.Fatalf("CleanupStaleInjectionBundles() error = %v", err)
	}
	if _, err := os.Stat(staleTokenSidecar); !os.IsNotExist(err) {
		t.Fatalf("stale orphan Copilot token sidecar should be removed, err=%v", err)
	}
	if _, err := os.Stat(recentTokenSidecar); err != nil {
		t.Fatalf("recent orphan Copilot token sidecar should remain: %v", err)
	}
}

func TestCleanupStaleInjectionBundlesConservativelyHandlesOrphanCopilotTokenHandoffs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	staleTokenHandoff := filepath.Join(root, "workcell-injections.stale.copilot-token-handoff.fixture")
	recentTokenHandoff := filepath.Join(root, "workcell-injections.recent.copilot-token-handoff.fixture")
	for _, path := range []string{staleTokenHandoff, recentTokenHandoff} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "copilot-github-token.txt"), []byte("test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-13 * time.Hour)
	if err := os.Chtimes(staleTokenHandoff, old, old); err != nil {
		t.Fatal(err)
	}

	if err := CleanupStaleInjectionBundles(root); err != nil {
		t.Fatalf("CleanupStaleInjectionBundles() error = %v", err)
	}
	if _, err := os.Stat(staleTokenHandoff); !os.IsNotExist(err) {
		t.Fatalf("stale orphan Copilot token handoff should be removed, err=%v", err)
	}
	if _, err := os.Stat(recentTokenHandoff); err != nil {
		t.Fatalf("recent orphan Copilot token handoff should remain: %v", err)
	}
}

func TestCleanupStaleInjectionBundlesConservativelyHandlesStandaloneCopilotTokenHandoffs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	staleTokenHandoff := filepath.Join(root, "copilot-token-handoff.stale")
	recentTokenHandoff := filepath.Join(root, "copilot-token-handoff.recent")
	for _, path := range []string{staleTokenHandoff, recentTokenHandoff} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "copilot-github-token.txt"), []byte("test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-13 * time.Hour)
	if err := os.Chtimes(staleTokenHandoff, old, old); err != nil {
		t.Fatal(err)
	}

	if err := CleanupStaleInjectionBundles(root); err != nil {
		t.Fatalf("CleanupStaleInjectionBundles() error = %v", err)
	}
	if _, err := os.Stat(staleTokenHandoff); !os.IsNotExist(err) {
		t.Fatalf("stale standalone Copilot token handoff should be removed, err=%v", err)
	}
	if _, err := os.Stat(recentTokenHandoff); err != nil {
		t.Fatalf("recent standalone Copilot token handoff should remain: %v", err)
	}
}
