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
