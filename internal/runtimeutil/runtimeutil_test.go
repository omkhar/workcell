package runtimeutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
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
