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
	t.Parallel()

	root := t.TempDir()
	valid := filepath.Join(root, "mounts.json")
	exact := append([]byte("[]"), bytes.Repeat([]byte{' '}, int(maxRuntimeMountSpecBytes-2))...)
	if err := os.WriteFile(valid, exact, 0o600); err != nil {
		t.Fatal(err)
	}
	if mounts, err := ListDirectMounts(valid); err != nil || len(mounts) != 0 {
		t.Fatalf("ListDirectMounts exact limit = %#v, %v", mounts, err)
	}

	oversized := filepath.Join(root, "oversized.json")
	if err := os.WriteFile(oversized, append(exact, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListDirectMounts(oversized); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("ListDirectMounts oversized error = %v, want byte-limit rejection", err)
	}

	target := filepath.Join(root, "target.json")
	if err := os.WriteFile(target, []byte("[]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
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
	if err := os.WriteFile(filepath.Join(realParent, "mounts.json"), []byte("[]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
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
	t.Parallel()

	root := t.TempDir()
	manifest := []byte(`{"credentials":{"a":{}}}`)
	exact := append(manifest, bytes.Repeat([]byte{' '}, int(maxRuntimeManifestBytes)-len(manifest))...)
	manifestPath := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(manifestPath, exact, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RewriteBundleCredentialOverride(manifestPath, "", "a", "override.txt"); err != nil {
		t.Fatalf("RewriteBundleCredentialOverride exact limit: %v", err)
	}

	oversized := filepath.Join(root, "oversized.json")
	if err := os.WriteFile(oversized, append(exact, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RewriteBundleCredentialOverride(oversized, "", "a", "override.txt"); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("RewriteBundleCredentialOverride oversized error = %v, want byte-limit rejection", err)
	}

	outside := filepath.Join(root, "outside.json")
	original := []byte(`{"credentials":{"a":{}}}` + "\n")
	if err := os.WriteFile(outside, original, 0o600); err != nil {
		t.Fatal(err)
	}
	leafLink := filepath.Join(root, "leaf-link.json")
	if err := os.Symlink(filepath.Base(outside), leafLink); err != nil {
		t.Skipf("os.Symlink unavailable: %v", err)
	}
	if err := RewriteBundleCredentialOverride(leafLink, "", "a", "override.txt"); err == nil {
		t.Fatal("RewriteBundleCredentialOverride accepted a leaf symlink")
	}
	if data, err := os.ReadFile(outside); err != nil || !bytes.Equal(data, original) {
		t.Fatalf("leaf symlink changed outside manifest: %q, %v", data, err)
	}

	realParent := filepath.Join(root, "real-parent")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	parentTarget := filepath.Join(realParent, "manifest.json")
	if err := os.WriteFile(parentTarget, original, 0o600); err != nil {
		t.Fatal(err)
	}
	linkedParent := filepath.Join(root, "linked-parent")
	if err := os.Symlink(filepath.Base(realParent), linkedParent); err != nil {
		t.Skipf("os.Symlink unavailable: %v", err)
	}
	if err := RewriteBundleCredentialOverride(filepath.Join(linkedParent, "manifest.json"), "", "a", "override.txt"); err == nil {
		t.Fatal("RewriteBundleCredentialOverride accepted a symlinked parent")
	}
	if data, err := os.ReadFile(parentTarget); err != nil || !bytes.Equal(data, original) {
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
