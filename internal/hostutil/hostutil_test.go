// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package hostutil

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCanonicalizePathResolvesHomeAndSymlinks(t *testing.T) {
	tmp := t.TempDir()
	realHome := filepath.Join(tmp, "real-home")
	linkHome := filepath.Join(tmp, "link-home")
	if err := os.MkdirAll(realHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realHome, linkHome); err != nil {
		t.Fatal(err)
	}

	got, err := canonicalizeForTest(t, "~/debug/workcell.log", linkHome)
	if err != nil {
		t.Fatal(err)
	}

	canonicalHome, err := filepath.EvalSymlinks(realHome)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalHome, "debug", "workcell.log")
	if got != want {
		t.Fatalf("canonicalizeForTest() = %q, want %q", got, want)
	}
}

func TestCanonicalizePathResolvesMissingSuffixBehindSymlink(t *testing.T) {
	tmp := t.TempDir()
	realRoot := filepath.Join(tmp, "real-root")
	linkRoot := filepath.Join(tmp, "link-root")
	if err := os.MkdirAll(realRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatal(err)
	}

	got, err := canonicalizeForTest(t, filepath.Join(linkRoot, "missing", "child"), realRoot)
	if err != nil {
		t.Fatal(err)
	}

	canonicalRoot, err := filepath.EvalSymlinks(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalRoot, "missing", "child")
	if got != want {
		t.Fatalf("canonicalizeForTest() = %q, want %q", got, want)
	}
}

func TestCanonicalizePathFromUsesExplicitBaseForRelativePaths(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	base := filepath.Join(tmp, "workspace")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	canonicalBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}

	got, err := CanonicalizePathFrom(filepath.Join("configs", "policy.toml"), base)
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(canonicalBase, "configs", "policy.toml")
	if got != want {
		t.Fatalf("CanonicalizePathFrom() = %q, want %q", got, want)
	}
}

func TestWriteGitHubReleaseCreatePayload(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	outputPath := filepath.Join(tmp, "create.json")
	if err := WriteGitHubReleaseCreatePayload("v1.2.3", outputPath); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != `{"tag_name":"v1.2.3","draft":true,"generate_release_notes":true}` {
		t.Fatalf("unexpected payload: %s", data)
	}
}

func TestWriteGitHubReleaseMetadata(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	releaseJSONPath := filepath.Join(tmp, "release.json")
	outputPath := filepath.Join(tmp, "metadata.bin")
	releaseJSON := map[string]any{
		"id":         123,
		"upload_url": "https://uploads.github.com/repos/example/workcell/releases/123/assets{?name,label}",
		"draft":      true,
		"immutable":  false,
		"assets": []map[string]any{
			{"name": "workcell-linux-amd64.tar.gz", "id": 11},
			{"name": "workcell-linux-arm64.tar.gz"},
		},
	}
	data, err := json.Marshal(releaseJSON)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(releaseJSONPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteGitHubReleaseMetadata(releaseJSONPath, []string{
		filepath.Join("/tmp", "workcell-linux-amd64.tar.gz"),
		filepath.Join("/tmp", "workcell-linux-arm64.tar.gz"),
	}, outputPath); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}

	records := bytes.Split(got, []byte{0})
	if len(records) > 0 && len(records[len(records)-1]) == 0 {
		records = records[:len(records)-1]
	}
	want := [][]byte{
		[]byte("123"),
		[]byte("https://uploads.github.com/repos/example/workcell/releases/123/assets"),
		[]byte("true"),
		[]byte("false"),
		[]byte("workcell-linux-amd64.tar.gz"),
		[]byte("11"),
		[]byte("workcell-linux-arm64.tar.gz"),
		[]byte(""),
	}
	if len(records) != len(want) {
		t.Fatalf("unexpected record count: got %d want %d", len(records), len(want))
	}
	for i := range want {
		if !bytes.Equal(records[i], want[i]) {
			t.Fatalf("record %d = %q, want %q", i, records[i], want[i])
		}
	}
}

func TestEncodeReleaseAssetName(t *testing.T) {
	t.Parallel()
	got := EncodeReleaseAssetName("workcell a+b.tar.gz")
	want := "workcell%20a%2Bb.tar.gz"
	if got != want {
		t.Fatalf("EncodeReleaseAssetName() = %q, want %q", got, want)
	}
}

func TestWriteReleaseBundleManifest(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	outputPath := filepath.Join(tmp, "bundle", "manifest.json")
	if err := WriteReleaseBundleManifest(outputPath, "HEAD", "workcell.tar.gz", "workcell/", 123, "sha256:aaa", "sha256:bbb"); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}

	want := strings.Join([]string{
		"{",
		`  "archive_ref": "HEAD",`,
		`  "bundle_name": "workcell.tar.gz",`,
		`  "bundle_prefix": "workcell/",`,
		`  "bundle_sha256": "sha256:aaa",`,
		`  "checksums_sha256": "sha256:bbb",`,
		`  "source_date_epoch": 123`,
		"}",
		"",
	}, "\n")
	if string(got) != want {
		t.Fatalf("unexpected manifest:\n%s", got)
	}
}

func TestDirectMountCacheKeyMatchesNULTerminatedHash(t *testing.T) {
	t.Parallel()
	got := DirectMountCacheKey("/host/auth.json", "/opt/workcell/host-inputs/credentials/codex-auth.json")

	sum := sha256.Sum256([]byte("/host/auth.json\x00/opt/workcell/host-inputs/credentials/codex-auth.json\x00"))
	want := hex.EncodeToString(sum[:8])
	if got != want {
		t.Fatalf("DirectMountCacheKey() = %q, want %q", got, want)
	}
}

func TestColimaProfileStatusMissingProfileReturnsNoMatch(t *testing.T) {
	t.Parallel()
	input := []byte(strings.Join([]string{
		`{"name":"default","status":"Running"}`,
		`{"name":"workcell-test","status":"Stopped"}`,
		"",
	}, "\n"))

	_, err := ColimaProfileStatus(input, "does-not-exist")
	if !IsNoMatch(err) {
		t.Fatalf("ColimaProfileStatus() err = %v, want IsNoMatch", err)
	}
}

func TestProfileLockIsStaleReportsMalformedOwnerMetadata(t *testing.T) {
	t.Parallel()
	lockDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(lockDir, "owner.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	stale, err := ProfileLockIsStale(lockDir)
	if err == nil {
		t.Fatal("ProfileLockIsStale() error = nil, want parse error")
	}
	if stale {
		t.Fatal("ProfileLockIsStale() stale = true, want false on malformed owner metadata")
	}
}

func TestProfileLockIsStaleReportsIncompleteOwnerMetadata(t *testing.T) {
	t.Parallel()
	lockDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(lockDir, "owner.json"), []byte(`{"pid":`+strconv.Itoa(os.Getpid())+`}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stale, err := ProfileLockIsStale(lockDir)
	if err == nil {
		t.Fatal("ProfileLockIsStale() error = nil, want incomplete metadata error")
	}
	if stale {
		t.Fatal("ProfileLockIsStale() stale = true, want false on incomplete owner metadata")
	}
}

func TestProfileLockIsStaleRecognizesLiveOwner(t *testing.T) {
	t.Parallel()
	lockDir := t.TempDir()
	started, err := processStartTime(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, "owner.json"), []byte(`{"pid":`+strconv.Itoa(os.Getpid())+`,"started":"`+started+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stale, err := ProfileLockIsStale(lockDir)
	if err != nil {
		t.Fatalf("ProfileLockIsStale() error = %v", err)
	}
	if stale {
		t.Fatal("ProfileLockIsStale() stale = true, want false for live owner")
	}
}

func TestAcquireProfileLockCreatesOwnerAtomically(t *testing.T) {
	t.Parallel()
	lockDir := filepath.Join(t.TempDir(), "profile.lock")

	acquired, err := AcquireProfileLock(lockDir, os.Getpid())
	if err != nil {
		t.Fatalf("AcquireProfileLock() error = %v", err)
	}
	if !acquired {
		t.Fatal("AcquireProfileLock() = false, want true")
	}

	content, err := os.ReadFile(filepath.Join(lockDir, "owner.json"))
	if err != nil {
		t.Fatalf("read owner.json: %v", err)
	}
	var owner struct {
		PID     int    `json:"pid"`
		Started string `json:"started"`
	}
	if err := json.Unmarshal(content, &owner); err != nil {
		t.Fatalf("unmarshal owner.json: %v", err)
	}
	if owner.PID != os.Getpid() {
		t.Fatalf("owner PID = %d, want %d", owner.PID, os.Getpid())
	}
	if owner.Started == "" {
		t.Fatal("owner.Started = empty, want process start time")
	}
}

func TestAcquireProfileLockReturnsFalseWhenLockExists(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	lockDir := filepath.Join(parent, "profile.lock")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}

	acquired, err := AcquireProfileLock(lockDir, os.Getpid())
	if err != nil {
		t.Fatalf("AcquireProfileLock() error = %v", err)
	}
	if acquired {
		t.Fatal("AcquireProfileLock() = true, want false for existing lock")
	}

	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".pending.") {
			t.Fatalf("temporary lock dir leaked: %s", entry.Name())
		}
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

func canonicalizeForTest(t *testing.T, path, home string) (string, error) {
	t.Helper()
	t.Setenv("HOME", home)
	return CanonicalizePath(path)
}
