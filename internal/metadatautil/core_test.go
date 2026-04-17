// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWalkFilesSkipsExcludedPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "adapters", "keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "adapters", "node_modules", "skip"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "adapters", "keep", "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "adapters", "node_modules", "skip", "b.txt"), []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := walkFiles(root, "adapters", "node_modules", "target")
	if err != nil {
		t.Fatalf("walkFiles() error = %v", err)
	}
	want := []string{filepath.Join("adapters", "keep", "a.txt")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("walkFiles() = %#v, want %#v", got, want)
	}
}

func TestWalkRepoFilesSkipsExcludedPaths(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".git", "dist", "tmp", "node_modules", "target", "pkg"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dist", "ignore.txt"), []byte("ignore"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "target", "ignore.txt"), []byte("ignore"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "skip.pyc"), []byte("skip"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := walkRepoFiles(root)
	if err != nil {
		t.Fatalf("walkRepoFiles() error = %v", err)
	}
	want := []string{filepath.Join("pkg", "keep.txt")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("walkRepoFiles() = %#v, want %#v", got, want)
	}
}

func TestGitTrackedFilesExcludesUntrackedFiles(t *testing.T) {
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s) error = %v", root, err)
	}

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v failed: %v\n%s", args, err, output)
		}
	}

	run("git", "init", "-q", canonicalRoot)
	run("git", "-C", canonicalRoot, "config", "user.name", "Workcell Tests")
	run("git", "-C", canonicalRoot, "config", "user.email", "workcell-tests@example.com")
	run("git", "-C", canonicalRoot, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(canonicalRoot, "tracked.txt"), []byte("tracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "-C", canonicalRoot, "add", "tracked.txt")
	run("git", "-C", canonicalRoot, "commit", "-q", "-m", "init")
	if err := os.WriteFile(filepath.Join(canonicalRoot, "tracked.txt"), []byte("modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonicalRoot, "scratch.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, tracked, err := gitTrackedFiles(canonicalRoot)
	if err != nil {
		t.Fatalf("gitTrackedFiles() error = %v", err)
	}
	if !tracked {
		t.Fatal("gitTrackedFiles() should report tracked repository context")
	}
	want := []string{"tracked.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gitTrackedFiles() = %#v, want %#v", got, want)
	}
}

func TestGitTrackedFilesRejectsTrackedSymlinks(t *testing.T) {
	root := t.TempDir()
	outsideRoot := t.TempDir()
	outsidePath := filepath.Join(outsideRoot, "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s) error = %v", root, err)
	}

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v failed: %v\n%s", args, err, output)
		}
	}

	run("git", "init", "-q", canonicalRoot)
	run("git", "-C", canonicalRoot, "config", "user.name", "Workcell Tests")
	run("git", "-C", canonicalRoot, "config", "user.email", "workcell-tests@example.com")
	run("git", "-C", canonicalRoot, "config", "commit.gpgsign", "false")
	if err := os.Symlink(outsidePath, filepath.Join(canonicalRoot, "leak.txt")); err != nil {
		t.Fatal(err)
	}
	run("git", "-C", canonicalRoot, "add", "leak.txt")
	run("git", "-C", canonicalRoot, "commit", "-q", "-m", "add leak")

	_, tracked, err := gitTrackedFiles(canonicalRoot)
	if !tracked {
		t.Fatal("gitTrackedFiles() should report tracked repository context")
	}
	if err == nil {
		t.Fatal("gitTrackedFiles() unexpectedly accepted a tracked symlink")
	}
	if !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("gitTrackedFiles() error = %v, want symlink rejection", err)
	}
}

func TestDigestMapRejectsSymlinkedInputs(t *testing.T) {
	root := t.TempDir()
	outsideRoot := t.TempDir()
	outsidePath := filepath.Join(outsideRoot, "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(root, "leak.txt")); err != nil {
		t.Fatal(err)
	}

	_, err := digestMap(root, []string{"leak.txt"})
	if err == nil {
		t.Fatal("digestMap() unexpectedly accepted a symlink")
	}
	if !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("digestMap() error = %v, want symlink rejection", err)
	}
}

func TestGenerateControlPlaneManifestIncludesDetachedStdinWrapper(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	outputPath := filepath.Join(t.TempDir(), "control-plane-manifest.json")
	if err := GenerateControlPlaneManifest(root, outputPath); err != nil {
		t.Fatalf("GenerateControlPlaneManifest() error = %v", err)
	}

	var manifest struct {
		RuntimeArtifacts []struct {
			RepoPath    string `json:"repo_path"`
			RuntimePath string `json:"runtime_path"`
		} `json:"runtime_artifacts"`
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", outputPath, err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	for _, artifact := range manifest.RuntimeArtifacts {
		if artifact.RepoPath == "runtime/container/detached-stdin-wrapper.sh" &&
			artifact.RuntimePath == "/usr/local/libexec/workcell/detached-stdin-wrapper.sh" {
			return
		}
	}
	t.Fatal("control-plane manifest missing detached stdin wrapper attestation")
}

func TestControlPlaneParityRowsIncludePrivilegedAndDetachedWrappers(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	outputPath := filepath.Join(t.TempDir(), "control-plane-manifest.json")
	if err := GenerateControlPlaneManifest(root, outputPath); err != nil {
		t.Fatalf("GenerateControlPlaneManifest() error = %v", err)
	}

	rows, err := ControlPlaneParityRows(outputPath)
	if err != nil {
		t.Fatalf("ControlPlaneParityRows() error = %v", err)
	}

	for _, want := range []string{
		"path\tapt-broker\t/usr/local/libexec/workcell/apt-broker.sh",
		"path\tdevelopment-wrapper\t/usr/local/libexec/workcell/development-wrapper.sh",
		"path\tdetached-stdin-wrapper\t/usr/local/libexec/workcell/detached-stdin-wrapper.sh",
		"path\tsudo-wrapper\t/usr/local/libexec/workcell/sudo-wrapper.sh",
	} {
		found := false
		for _, row := range rows {
			if row == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("ControlPlaneParityRows() missing %q in %#v", want, rows)
		}
	}
}
