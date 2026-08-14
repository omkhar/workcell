// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package hoststate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omkhar/workcell/internal/host/launcher"
	"github.com/omkhar/workcell/internal/rootio"
	"golang.org/x/sys/unix"
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

func TestManifestMetadataLinesUsesBoundedNoFollowReads(t *testing.T) {
	root := t.TempDir()
	exactPath := filepath.Join(root, "manifest.json")
	exact := append([]byte(`{"metadata":{}}`), bytes.Repeat([]byte{' '}, int(rootio.MaxManifestBytes)-len(`{"metadata":{}}`))...)
	if err := os.WriteFile(exactPath, exact, 0o600); err != nil {
		t.Fatal(err)
	}
	if lines, err := ManifestMetadataLines(exactPath); err != nil || len(lines) != 7 {
		t.Fatalf("ManifestMetadataLines exact limit = %#v, %v", lines, err)
	}

	overLimitPath := filepath.Join(root, "over-limit.json")
	if err := os.WriteFile(overLimitPath, append(exact, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ManifestMetadataLines(overLimitPath); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("ManifestMetadataLines oversized error = %v, want byte-limit rejection", err)
	}

	target := filepath.Join(root, "target.json")
	if err := os.WriteFile(target, []byte(`{"metadata":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	leafLink := filepath.Join(root, "manifest-link.json")
	if err := os.Symlink(filepath.Base(target), leafLink); err != nil {
		t.Skipf("os.Symlink unavailable: %v", err)
	}
	if _, err := ManifestMetadataLines(leafLink); err == nil {
		t.Fatal("ManifestMetadataLines accepted a leaf symlink")
	}

	realParent := filepath.Join(root, "real-parent")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realParent, "manifest.json"), []byte(`{"metadata":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	linkedParent := filepath.Join(root, "linked-parent")
	if err := os.Symlink(filepath.Base(realParent), linkedParent); err != nil {
		t.Skipf("os.Symlink unavailable: %v", err)
	}
	if _, err := ManifestMetadataLines(filepath.Join(linkedParent, "manifest.json")); err == nil {
		t.Fatal("ManifestMetadataLines accepted a symlinked parent")
	}

	fifo := filepath.Join(root, "manifest.fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("unix.Mkfifo unavailable: %v", err)
	}
	if _, err := ManifestMetadataLines(fifo); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("ManifestMetadataLines FIFO error = %v, want regular-file rejection", err)
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

func TestCleanupStaleInjectionBundlesPreservesLiveCurrentOwnerAndSidecars(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	bundleName := "workcell-injections.live"
	bundleDir := filepath.Join(root, bundleName)
	mountSidecar := filepath.Join(root, bundleName+".mounts.json")
	tokenSidecar := filepath.Join(root, bundleName+".copilot-token.env.live")
	tokenHandoffDir := filepath.Join(root, bundleName+".copilot-token-handoff.live")
	if err := os.MkdirAll(tokenHandoffDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launcher.WriteProfileOwner(filepath.Join(bundleDir, "owner.json"), os.Getpid()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{mountSidecar, tokenSidecar} {
		if err := os.WriteFile(path, []byte("test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
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
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("live injection artifact should remain: %s err=%v", path, err)
		}
	}
}

func TestCleanupStaleInjectionBundlesPreservesInvalidOwners(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		owner []byte
	}{
		{name: "malformed JSON", owner: []byte("{")},
		{name: "incomplete", owner: []byte(`{"pid":1}`)},
		{name: "wrong platform", owner: injectionOwnerJSON(t, os.Getpid(), otherPlatformGeneration())},
		{name: "malformed darwin seconds", owner: injectionOwnerJSON(t, os.Getpid(), "darwin:0.000000")},
		{name: "malformed darwin microseconds", owner: injectionOwnerJSON(t, os.Getpid(), "darwin:1.00000")},
		{name: "overflow darwin seconds", owner: injectionOwnerJSON(t, os.Getpid(), "darwin:9223372036854775808.000000")},
		{name: "malformed linux zero ticks", owner: injectionOwnerJSON(t, os.Getpid(), "linux:0")},
		{name: "malformed linux ticks", owner: injectionOwnerJSON(t, os.Getpid(), "linux:01")},
		{name: "overflow linux ticks", owner: injectionOwnerJSON(t, os.Getpid(), "linux:18446744073709551616")},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			bundleDir := filepath.Join(root, "workcell-injections.invalid")
			writeInjectionOwnerContent(t, bundleDir, test.owner)
			ageInjectionArtifact(t, bundleDir)

			if err := CleanupStaleInjectionBundles(root); err != nil {
				t.Fatalf("CleanupStaleInjectionBundles() error = %v", err)
			}
			if _, err := os.Stat(bundleDir); err != nil {
				t.Fatalf("invalid owner bundle should remain: %v", err)
			}
		})
	}
}

func TestCleanupStaleInjectionBundlesTreatsSimilarPrefixesAsLegacy(t *testing.T) {
	t.Parallel()

	for _, started := range []string{"darwinish:1.000000", "linuxish:1"} {
		t.Run(started, func(t *testing.T) {
			root := t.TempDir()
			bundleDir := filepath.Join(root, "workcell-injections.similar")
			writeInjectionOwner(t, bundleDir, os.Getpid(), started)
			ageInjectionArtifact(t, bundleDir)

			if err := CleanupStaleInjectionBundles(root); err != nil {
				t.Fatalf("CleanupStaleInjectionBundles() error = %v", err)
			}
			if _, err := os.Stat(bundleDir); !os.IsNotExist(err) {
				t.Fatalf("legacy mismatch bundle should be removed, err = %v", err)
			}
		})
	}
}

func TestCleanupStaleInjectionBundlesPreservesLiveOwnerDuringConcurrentCleanup(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	bundleDir := filepath.Join(root, "workcell-injections.concurrent")
	if err := launcher.WriteProfileOwner(filepath.Join(bundleDir, "owner.json"), os.Getpid()); err != nil {
		t.Fatal(err)
	}
	ageInjectionArtifact(t, bundleDir)

	const cleaners = 32
	errs := make(chan error, cleaners)
	var group sync.WaitGroup
	for range cleaners {
		group.Add(1)
		go func() {
			defer group.Done()
			errs <- CleanupStaleInjectionBundles(root)
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("CleanupStaleInjectionBundles() error = %v", err)
		}
	}
	if _, err := os.Stat(bundleDir); err != nil {
		t.Fatalf("live bundle should remain after concurrent cleanup: %v", err)
	}
}

func TestInjectionBundleIsLiveSupportsLegacyOwner(t *testing.T) {
	t.Parallel()

	bundleDir := filepath.Join(t.TempDir(), "workcell-injections.legacy")
	started, err := launcher.ProcessStartTime(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	writeInjectionOwner(t, bundleDir, os.Getpid(), started)

	live, err := injectionBundleIsLive(bundleDir, time.Now())
	if err != nil || !live {
		t.Fatalf("injectionBundleIsLive() = %v, %v; want true, nil", live, err)
	}
}

func TestInjectionBundleIsLiveRejectsDeadAndChangedOwners(t *testing.T) {
	t.Parallel()

	started := currentProcessGeneration(t)
	dead := exec.Command("/bin/sh", "-c", "exit 0")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		pid     int
		started string
	}{
		{name: "dead", pid: dead.ProcessState.Pid(), started: started},
		{name: "changed generation", pid: os.Getpid(), started: differentProcessGeneration(t, started)},
	} {
		t.Run(test.name, func(t *testing.T) {
			bundleDir := filepath.Join(t.TempDir(), "workcell-injections."+test.name)
			writeInjectionOwner(t, bundleDir, test.pid, test.started)

			live, err := injectionBundleIsLive(bundleDir, time.Now())
			if err != nil || live {
				t.Fatalf("injectionBundleIsLive() = %v, %v; want false, nil", live, err)
			}
		})
	}
}

func TestInjectionBundleIsLivePreservesMalformedOwner(t *testing.T) {
	t.Parallel()

	bundleDir := filepath.Join(t.TempDir(), "workcell-injections.malformed")
	if err := os.MkdirAll(bundleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "owner.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	live, err := injectionBundleIsLive(bundleDir, time.Now())
	if err == nil || !live {
		t.Fatalf("injectionBundleIsLive() = %v, %v; want true, non-nil", live, err)
	}
}

func writeInjectionOwner(t *testing.T, bundleDir string, pid int, started string) {
	t.Helper()
	writeInjectionOwnerContent(t, bundleDir, injectionOwnerJSON(t, pid, started))
}

func writeInjectionOwnerContent(t *testing.T, bundleDir string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(bundleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "owner.json"), content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func injectionOwnerJSON(t *testing.T, pid int, started string) []byte {
	t.Helper()
	payload, err := json.Marshal(struct {
		PID     int    `json:"pid"`
		Started string `json:"started"`
	}{PID: pid, Started: started})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func ageInjectionArtifact(t *testing.T, path string) {
	t.Helper()
	old := time.Now().Add(-13 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func otherPlatformGeneration() string {
	if runtime.GOOS == "darwin" {
		return "linux:1"
	}
	return "darwin:1.000000"
}

func currentProcessGeneration(t *testing.T) string {
	t.Helper()
	var seed string
	switch runtime.GOOS {
	case "darwin":
		seed = "darwin:1.000000"
	case "linux":
		seed = "linux:1"
	default:
		started, err := launcher.ProcessStartTime(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		return started
	}
	started, err := launcher.ObserveProcessGeneration(os.Getpid(), seed)
	if err != nil {
		t.Fatal(err)
	}
	return started
}

func differentProcessGeneration(t *testing.T, started string) string {
	t.Helper()
	if ticks, ok := strings.CutPrefix(started, "linux:"); ok {
		return "linux:" + ticks + "0"
	}
	seconds, microseconds, ok := strings.Cut(strings.TrimPrefix(started, "darwin:"), ".")
	if !ok || seconds == "" || len(microseconds) != 6 {
		return started + " changed"
	}
	if microseconds == "000000" {
		return "darwin:" + seconds + ".000001"
	}
	return "darwin:" + seconds + ".000000"
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
