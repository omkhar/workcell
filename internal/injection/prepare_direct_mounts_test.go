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
