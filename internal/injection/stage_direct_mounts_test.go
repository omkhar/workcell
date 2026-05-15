// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/host/hoststate"
)

func writeMountSpec(t *testing.T, path string, entries []map[string]any) {
	t.Helper()
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestStageDirectMountsMissingSpecReturnsEmpty(t *testing.T) {
	bundleRoot := t.TempDir()
	args, err := StageDirectMounts(bundleRoot, filepath.Join(bundleRoot, "does-not-exist"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(args) != 0 {
		t.Fatalf("expected no args, got %v", args)
	}
}

func TestStageDirectMountsEmptyBundleRootRejected(t *testing.T) {
	specPath := filepath.Join(t.TempDir(), "spec.json")
	writeMountSpec(t, specPath, []map[string]any{})
	_, err := StageDirectMounts("", specPath)
	if err == nil {
		t.Fatalf("expected error for empty bundle root")
	}
	if !strings.Contains(err.Error(), "injection bundle root") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStageDirectMountsRejectsNonAbsoluteSource(t *testing.T) {
	bundleRoot := t.TempDir()
	specPath := filepath.Join(bundleRoot, "spec.json")
	writeMountSpec(t, specPath, []map[string]any{
		{"source": "relative.txt", "mount_path": "/opt/workcell/host-inputs/foo"},
	})
	_, err := StageDirectMounts(bundleRoot, specPath)
	if err == nil || !strings.Contains(err.Error(), "not absolute") {
		t.Fatalf("expected 'not absolute' error, got %v", err)
	}
}

func TestStageDirectMountsRejectsMissingSource(t *testing.T) {
	bundleRoot := t.TempDir()
	specPath := filepath.Join(bundleRoot, "spec.json")
	writeMountSpec(t, specPath, []map[string]any{
		{"source": "/nonexistent/path/does/not/exist", "mount_path": "/opt/workcell/host-inputs/foo"},
	})
	_, err := StageDirectMounts(bundleRoot, specPath)
	if err == nil {
		t.Fatalf("expected error for missing source")
	}
}

func TestStageDirectMountsRejectsMountPathOutsideHostInputs(t *testing.T) {
	bundleRoot := t.TempDir()
	sourceFile := filepath.Join(bundleRoot, "source.txt")
	if err := os.WriteFile(sourceFile, []byte("hello"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	specPath := filepath.Join(bundleRoot, "spec.json")
	writeMountSpec(t, specPath, []map[string]any{
		{"source": sourceFile, "mount_path": "/etc/passwd"},
	})
	_, err := StageDirectMounts(bundleRoot, specPath)
	if err == nil || !strings.Contains(err.Error(), "outside the managed host-input root") {
		t.Fatalf("expected outside-host-input error, got %v", err)
	}
}

// TestStageDirectMountsRejectsTraversalOutOfHostInputs pins the
// filepath.Clean guard: a `..` segment in the mount path must not let
// an attacker escape the managed host-input root.  Without the Clean
// normalisation, strings.HasPrefix("/opt/workcell/host-inputs/../etc/foo",
// "/opt/workcell/host-inputs/") returns true and the mount would be
// staged into the container at `/opt/etc/foo`.
func TestStageDirectMountsRejectsTraversalOutOfHostInputs(t *testing.T) {
	bundleRoot := t.TempDir()
	sourceFile := filepath.Join(bundleRoot, "source.txt")
	if err := os.WriteFile(sourceFile, []byte("hello"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	for _, mount := range []string{
		"/opt/workcell/host-inputs/../etc/foo",
		"/opt/workcell/host-inputs",          // exact root, no trailing slash
		"/opt/workcell/host-inputs/./../etc", // mixed `.` and `..`
		"/opt/workcell/host-inputs/.",        // dot-only suffix collapses to the root
	} {
		specPath := filepath.Join(bundleRoot, "spec.json")
		writeMountSpec(t, specPath, []map[string]any{
			{"source": sourceFile, "mount_path": mount},
		})
		_, err := StageDirectMounts(bundleRoot, specPath)
		if err == nil || !strings.Contains(err.Error(), "outside the managed host-input root") {
			t.Fatalf("expected outside-host-input error for mount=%q, got %v", mount, err)
		}
	}
}

func TestStageDirectMountsStagesFileAndReturnsDockerArgs(t *testing.T) {
	bundleRoot := t.TempDir()
	sourceFile := filepath.Join(bundleRoot, "src", "secret.json")
	if err := os.MkdirAll(filepath.Dir(sourceFile), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(sourceFile, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	mountPath := "/opt/workcell/host-inputs/credentials/secret.json"
	specPath := filepath.Join(bundleRoot, "spec.json")
	writeMountSpec(t, specPath, []map[string]any{
		{"source": sourceFile, "mount_path": mountPath},
	})

	args, err := StageDirectMounts(bundleRoot, specPath)
	if err != nil {
		t.Fatalf("StageDirectMounts: %v", err)
	}
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %v", args)
	}
	if args[0] != "-v" {
		t.Fatalf("expected '-v' first, got %q", args[0])
	}
	hash := hoststate.DirectMountCacheKey(sourceFile, mountPath)
	expectedStaged := filepath.Join(bundleRoot, "direct-mounts", hash)
	expectedArg := expectedStaged + ":" + mountPath + ":ro"
	if args[1] != expectedArg {
		t.Fatalf("expected %q, got %q", expectedArg, args[1])
	}
	if _, err := os.Stat(expectedStaged); err != nil {
		t.Fatalf("expected staged file to exist: %v", err)
	}
}

func TestStageDirectMountsStagesDirectoryRecursively(t *testing.T) {
	bundleRoot := t.TempDir()
	sourceDir := filepath.Join(bundleRoot, "src-dir")
	nested := filepath.Join(sourceDir, "nested", "file.txt")
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(nested, []byte("payload"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	mountPath := "/opt/workcell/host-inputs/configs"
	specPath := filepath.Join(bundleRoot, "spec.json")
	writeMountSpec(t, specPath, []map[string]any{
		{"source": sourceDir, "mount_path": mountPath},
	})

	args, err := StageDirectMounts(bundleRoot, specPath)
	if err != nil {
		t.Fatalf("StageDirectMounts: %v", err)
	}
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %v", args)
	}
	hash := hoststate.DirectMountCacheKey(sourceDir, mountPath)
	stagedNested := filepath.Join(bundleRoot, "direct-mounts", hash, "nested", "file.txt")
	data, err := os.ReadFile(stagedNested)
	if err != nil {
		t.Fatalf("read staged file: %v", err)
	}
	if string(data) != "payload" {
		t.Fatalf("staged content mismatch: %q", string(data))
	}
}

// TestStageDirectMountsSkipsSymlinkToFile pins the cautious-staging
// rule: a regular-file symlink under a directory source must NOT be
// dereferenced into the staged tree (otherwise an attacker-controlled
// link like `~/.aws/credentials -> /etc/passwd` would surface host
// files inside the container).  The staged tree should still contain
// the legitimate sibling file alongside the dropped link.
func TestStageDirectMountsSkipsSymlinkToFile(t *testing.T) {
	bundleRoot := t.TempDir()
	sourceDir := filepath.Join(bundleRoot, "src-dir")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	realFile := filepath.Join(sourceDir, "real.txt")
	if err := os.WriteFile(realFile, []byte("real"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	linkPath := filepath.Join(sourceDir, "link.txt")
	if err := os.Symlink("/etc/hosts", linkPath); err != nil {
		t.Skipf("os.Symlink unavailable: %v", err)
	}
	mountPath := "/opt/workcell/host-inputs/configs"
	specPath := filepath.Join(bundleRoot, "spec.json")
	writeMountSpec(t, specPath, []map[string]any{
		{"source": sourceDir, "mount_path": mountPath},
	})

	if _, err := StageDirectMounts(bundleRoot, specPath); err != nil {
		t.Fatalf("StageDirectMounts: %v", err)
	}
	hash := hoststate.DirectMountCacheKey(sourceDir, mountPath)
	stagedRoot := filepath.Join(bundleRoot, "direct-mounts", hash)
	stagedReal := filepath.Join(stagedRoot, "real.txt")
	if data, err := os.ReadFile(stagedReal); err != nil || string(data) != "real" {
		t.Fatalf("expected real.txt staged with content 'real', got data=%q err=%v", string(data), err)
	}
	stagedLink := filepath.Join(stagedRoot, "link.txt")
	if _, err := os.Lstat(stagedLink); err == nil {
		t.Fatalf("symlink should have been skipped, but staged entry exists at %s", stagedLink)
	}
}

// TestStageDirectMountsSkipsSymlinkToDir pins the same cautious-
// staging rule for directory-targeting symlinks: a symlinked
// subdirectory must NOT be recursively followed into the staged
// tree.  Without this guard, a link like `inside/secrets -> /etc`
// would copy every file under /etc into the container.
func TestStageDirectMountsSkipsSymlinkToDir(t *testing.T) {
	bundleRoot := t.TempDir()
	sourceDir := filepath.Join(bundleRoot, "src-dir")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	realSub := filepath.Join(sourceDir, "real-sub")
	if err := os.MkdirAll(realSub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realSub, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	linkedDir := filepath.Join(sourceDir, "link-sub")
	if err := os.Symlink("/etc", linkedDir); err != nil {
		t.Skipf("os.Symlink unavailable: %v", err)
	}
	mountPath := "/opt/workcell/host-inputs/configs"
	specPath := filepath.Join(bundleRoot, "spec.json")
	writeMountSpec(t, specPath, []map[string]any{
		{"source": sourceDir, "mount_path": mountPath},
	})

	if _, err := StageDirectMounts(bundleRoot, specPath); err != nil {
		t.Fatalf("StageDirectMounts: %v", err)
	}
	hash := hoststate.DirectMountCacheKey(sourceDir, mountPath)
	stagedRoot := filepath.Join(bundleRoot, "direct-mounts", hash)
	stagedKeep := filepath.Join(stagedRoot, "real-sub", "keep.txt")
	if data, err := os.ReadFile(stagedKeep); err != nil || string(data) != "keep" {
		t.Fatalf("expected real-sub/keep.txt staged with content 'keep', got data=%q err=%v", string(data), err)
	}
	stagedLink := filepath.Join(stagedRoot, "link-sub")
	if _, err := os.Lstat(stagedLink); err == nil {
		t.Fatalf("symlinked dir should have been skipped, but staged entry exists at %s", stagedLink)
	}
}

func TestStageDirectMountsStripsGroupOtherPermissions(t *testing.T) {
	bundleRoot := t.TempDir()
	sourceFile := filepath.Join(bundleRoot, "src.txt")
	if err := os.WriteFile(sourceFile, []byte("hi"), 0o666); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	mountPath := "/opt/workcell/host-inputs/x"
	specPath := filepath.Join(bundleRoot, "spec.json")
	writeMountSpec(t, specPath, []map[string]any{
		{"source": sourceFile, "mount_path": mountPath},
	})

	if _, err := StageDirectMounts(bundleRoot, specPath); err != nil {
		t.Fatalf("StageDirectMounts: %v", err)
	}
	hash := hoststate.DirectMountCacheKey(sourceFile, mountPath)
	staged := filepath.Join(bundleRoot, "direct-mounts", hash)
	info, err := os.Stat(staged)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("expected group/other bits cleared, got %o", info.Mode().Perm())
	}
}
