// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package runtimeutil

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCanonicalizePath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	got, err := CanonicalizePath(link)
	if err != nil {
		t.Fatalf("CanonicalizePath error: %v", err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("CanonicalizePath = %q want %q", got, want)
	}
}

func TestResolveIPs(t *testing.T) {
	t.Parallel()

	ips, err := ResolveIPs("127.0.0.1")
	if err != nil {
		t.Fatalf("ResolveIPs error: %v", err)
	}
	if !reflect.DeepEqual(ips, []string{"127.0.0.1"}) {
		t.Fatalf("ResolveIPs = %#v", ips)
	}
}

func TestListDirectMountsAndRewriteBundleCredentialOverride(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifestPath := filepath.Join(root, "manifest.json")
	mountSpecPath := filepath.Join(root, "bundle.mounts.json")
	if err := os.WriteFile(mountSpecPath, []byte(`[
  {"source":"host/a.txt","mount_path":"/opt/workcell/host-inputs/credentials/a.txt"},
  {"source":"host/b.txt","mount_path":"/opt/workcell/host-inputs/credentials/b.txt"}
]`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"credentials": map[string]any{
			"a": map[string]any{
				"mount_path": "/opt/workcell/host-inputs/credentials/a.txt",
			},
			"b": map[string]any{
				"mount_path": "/opt/workcell/host-inputs/credentials/b.txt",
			},
		},
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	directMounts, err := ListDirectMounts(mountSpecPath)
	if err != nil {
		t.Fatalf("ListDirectMounts error: %v", err)
	}
	if len(directMounts) != 2 || directMounts[0].Source != "host/a.txt" || directMounts[1].MountPath != "/opt/workcell/host-inputs/credentials/b.txt" {
		t.Fatalf("ListDirectMounts = %#v", directMounts)
	}

	if err := RewriteBundleCredentialOverride(manifestPath, mountSpecPath, "a", "override.txt"); err != nil {
		t.Fatalf("RewriteBundleCredentialOverride error: %v", err)
	}
	rewritten, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(rewritten, &parsed); err != nil {
		t.Fatal(err)
	}
	credentials := parsed["credentials"].(map[string]any)
	if got := credentials["a"].(map[string]any)["source"]; got != "override.txt" {
		t.Fatalf("override source = %v", got)
	}
	if got := credentials["b"].(map[string]any)["source"]; got != "host/b.txt" {
		t.Fatalf("mount-derived source = %v", got)
	}
}

func TestRewriteBundleCredentialOverrideRejectsManifestSymlinkEscape(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outsideDir := filepath.Join(root, "outside")
	workspaceDir := filepath.Join(root, "workspace")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}

	outsideManifestPath := filepath.Join(outsideDir, "manifest.json")
	if err := os.WriteFile(outsideManifestPath, []byte(`{"credentials":{"a":{"mount_path":"m"}}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(workspaceDir, "manifest.json")
	if err := os.Symlink(filepath.Join("..", "outside", "manifest.json"), manifestPath); err != nil {
		t.Fatal(err)
	}

	if err := RewriteBundleCredentialOverride(manifestPath, "", "a", "override.txt"); err == nil {
		t.Fatal("expected symlink escape to fail")
	}
	data, err := os.ReadFile(outsideManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"credentials":{"a":{"mount_path":"m"}}}`+"\n" {
		t.Fatalf("outside manifest changed unexpectedly: %s", data)
	}
}

func TestListDirectMountsRejectsUnsafeInputsAndAcceptsExactLimit(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, "mounts.json")
	exact := append([]byte("[]"), bytes.Repeat([]byte{' '}, int(maxRuntimeMountSpecBytes-2))...)
	writeRuntimeFile(t, valid, exact)
	if mounts, err := ListDirectMounts(valid); err != nil || len(mounts) != 0 {
		t.Fatalf("ListDirectMounts exact limit = %#v, %v", mounts, err)
	}
	oversized := filepath.Join(root, "oversized.json")
	writeRuntimeFile(t, oversized, append(exact, ' '))
	if _, err := ListDirectMounts(oversized); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("ListDirectMounts oversized error = %v, want byte-limit rejection", err)
	}
	target := filepath.Join(root, "target.json")
	writeRuntimeFile(t, target, []byte("[]\n"))
	leafLink := filepath.Join(root, "leaf-link.json")
	if err := os.Symlink(filepath.Base(target), leafLink); err != nil {
		t.Skipf("os.Symlink unavailable: %v", err)
	}
	if _, err := ListDirectMounts(leafLink); err == nil {
		t.Fatal("ListDirectMounts accepted a leaf symlink")
	}
	realParent := filepath.Join(root, "real-parent")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	writeRuntimeFile(t, filepath.Join(realParent, "mounts.json"), []byte("[]\n"))
	linkedParent := filepath.Join(root, "linked-parent")
	if err := os.Symlink(filepath.Base(realParent), linkedParent); err != nil {
		t.Skipf("os.Symlink unavailable: %v", err)
	}
	if _, err := ListDirectMounts(filepath.Join(linkedParent, "mounts.json")); err == nil {
		t.Fatal("ListDirectMounts accepted a symlinked parent")
	}
	fifo := filepath.Join(root, "mounts.fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("unix.Mkfifo unavailable: %v", err)
	}
	if _, err := ListDirectMounts(fifo); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("ListDirectMounts FIFO error = %v, want regular-file rejection", err)
	}
}

func TestRewriteBundleCredentialOverrideRejectsUnsafeManifestAndAcceptsExactLimit(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "manifest.json")
	writeRuntimeManifestForOutputSize(t, manifestPath, maxRuntimeManifestBytes)
	if err := RewriteBundleCredentialOverride(manifestPath, "", "a", "override.txt"); err != nil {
		t.Fatalf("RewriteBundleCredentialOverride exact limit: %v", err)
	}
	if info, err := os.Stat(manifestPath); err != nil || info.Size() != maxRuntimeManifestBytes {
		t.Fatalf("RewriteBundleCredentialOverride exact output size = %v, %v", info, err)
	}
	overLimit := filepath.Join(root, "over-limit.json")
	original := writeRuntimeManifestForOutputSize(t, overLimit, maxRuntimeManifestBytes+1)
	if err := RewriteBundleCredentialOverride(overLimit, "", "a", "override.txt"); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("RewriteBundleCredentialOverride oversized error = %v, want byte-limit rejection", err)
	}
	if got, err := os.ReadFile(overLimit); err != nil || !bytes.Equal(got, original) {
		t.Fatal("RewriteBundleCredentialOverride published an over-limit manifest")
	}
	inputOverLimit := filepath.Join(root, "input-over-limit.json")
	writeRuntimeFile(t, inputOverLimit, append(original, bytes.Repeat([]byte{' '}, int(maxRuntimeManifestBytes)-len(original)+1)...))
	if err := RewriteBundleCredentialOverride(inputOverLimit, "", "a", "override.txt"); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("RewriteBundleCredentialOverride oversized input error = %v, want byte-limit rejection", err)
	}
	outside := filepath.Join(root, "outside.json")
	outsideOriginal := []byte(`{"credentials":{"a":{}}}` + "\n")
	writeRuntimeFile(t, outside, outsideOriginal)
	leafLink := filepath.Join(root, "leaf-link.json")
	if err := os.Symlink(filepath.Base(outside), leafLink); err != nil {
		t.Skipf("os.Symlink unavailable: %v", err)
	}
	if err := RewriteBundleCredentialOverride(leafLink, "", "a", "override.txt"); err == nil {
		t.Fatal("RewriteBundleCredentialOverride accepted a leaf symlink")
	}
	if data, err := os.ReadFile(outside); err != nil || !bytes.Equal(data, outsideOriginal) {
		t.Fatalf("leaf symlink changed outside manifest: %q, %v", data, err)
	}
	hardLinkedManifest := filepath.Join(root, "hardlink-manifest.json")
	if err := os.Chmod(outside, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, hardLinkedManifest); err != nil {
		t.Fatal(err)
	}
	outsideBefore, outsideErr := os.Stat(outside)
	manifestBefore, manifestErr := os.Stat(hardLinkedManifest)
	if outsideErr != nil || manifestErr != nil || !os.SameFile(outsideBefore, manifestBefore) {
		t.Fatalf("hard-link precondition = %v, %v", outsideErr, manifestErr)
	}
	if err := RewriteBundleCredentialOverride(hardLinkedManifest, "", "a", "override.txt"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(outside); err != nil || !bytes.Equal(data, outsideOriginal) {
		t.Fatalf("hard-link changed outside manifest: %q, %v", data, err)
	}
	outsideAfter, outsideErr := os.Stat(outside)
	manifestAfter, manifestErr := os.Stat(hardLinkedManifest)
	if outsideErr != nil || manifestErr != nil || os.SameFile(outsideAfter, manifestAfter) || outsideAfter.Mode().Perm() != 0o640 || manifestAfter.Mode().Perm() != 0o600 {
		t.Fatalf("hard-link postcondition = %v, %v", outsideErr, manifestErr)
	}
	if data, err := os.ReadFile(hardLinkedManifest); err != nil || !bytes.Contains(data, []byte(`"source":"override.txt"`)) {
		t.Fatalf("hard-link manifest override = %q, %v", data, err)
	}
	realParent := filepath.Join(root, "real-parent")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	parentTarget := filepath.Join(realParent, "manifest.json")
	writeRuntimeFile(t, parentTarget, outsideOriginal)
	linkedParent := filepath.Join(root, "linked-parent")
	if err := os.Symlink(filepath.Base(realParent), linkedParent); err != nil {
		t.Skipf("os.Symlink unavailable: %v", err)
	}
	if err := RewriteBundleCredentialOverride(filepath.Join(linkedParent, "manifest.json"), "", "a", "override.txt"); err == nil {
		t.Fatal("RewriteBundleCredentialOverride accepted a symlinked parent")
	}
	if data, err := os.ReadFile(parentTarget); err != nil || !bytes.Equal(data, outsideOriginal) {
		t.Fatalf("parent symlink changed target manifest: %q, %v", data, err)
	}
	fifo := filepath.Join(root, "manifest.fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("unix.Mkfifo unavailable: %v", err)
	}
	if err := RewriteBundleCredentialOverride(fifo, "", "a", "override.txt"); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("RewriteBundleCredentialOverride FIFO error = %v, want regular-file rejection", err)
	}
}

func writeRuntimeManifestForOutputSize(t *testing.T, path string, outputSize int64) []byte {
	t.Helper()
	output := map[string]any{"credentials": map[string]any{"a": map[string]any{"source": "override.txt"}}, "padding": ""}
	data, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	paddingSize := outputSize - int64(len(data)+1)
	if paddingSize < 0 {
		t.Fatalf("runtime manifest output limit %d is too small", outputSize)
	}
	output["padding"] = strings.Repeat("p", int(paddingSize))
	expected, err := json.Marshal(output)
	if err != nil || int64(len(expected)+1) != outputSize {
		t.Fatalf("runtime manifest fixture size = %d, want %d: %v", len(expected)+1, outputSize, err)
	}
	input := map[string]any{"credentials": map[string]any{"a": map[string]any{"source": "x"}}, "padding": output["padding"]}
	data, err = json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	writeRuntimeFile(t, path, data)
	return data
}

func writeRuntimeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
