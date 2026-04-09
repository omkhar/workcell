// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCommandCapturesExitCodeAndStreams(t *testing.T) {
	t.Parallel()

	result, err := RunCommand(context.Background(), CommandSpec{
		Path: "/bin/sh",
		Args: []string{"-c", "printf out; printf err >&2; exit 7"},
	})
	if err != nil {
		t.Fatalf("RunCommand returned error: %v", err)
	}
	if result.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", result.ExitCode)
	}
	if string(result.Stdout) != "out" {
		t.Fatalf("stdout = %q, want %q", result.Stdout, "out")
	}
	if string(result.Stderr) != "err" {
		t.Fatalf("stderr = %q, want %q", result.Stderr, "err")
	}
}

func TestSnapshotTreeCapturesModesAndSymlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dirPath := filepath.Join(root, "dir")
	if err := os.Mkdir(dirPath, 0o750); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(dirPath, "file.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(root, "link.txt")
	if err := os.Symlink(filepath.Join("dir", "file.txt"), linkPath); err != nil {
		t.Fatal(err)
	}

	entries, err := SnapshotTree(root)
	if err != nil {
		t.Fatalf("SnapshotTree returned error: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}
	if entries[0].Path != "dir" || entries[0].Kind != "dir" {
		t.Fatalf("dir entry = %#v", entries[0])
	}
	if entries[1].Path != filepath.ToSlash(filepath.Join("dir", "file.txt")) || entries[1].Kind != "file" {
		t.Fatalf("file entry = %#v", entries[1])
	}
	sum := sha256.Sum256([]byte("hello"))
	if entries[1].SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("file sha256 = %q, want %q", entries[1].SHA256, hex.EncodeToString(sum[:]))
	}
	if entries[2].Path != "link.txt" || entries[2].Kind != "symlink" {
		t.Fatalf("symlink entry = %#v", entries[2])
	}
	if entries[2].LinkTarget != filepath.Join("dir", "file.txt") {
		t.Fatalf("symlink target = %q, want %q", entries[2].LinkTarget, filepath.Join("dir", "file.txt"))
	}
}

func TestCompareDirectoryTreesDetectsMismatch(t *testing.T) {
	t.Parallel()

	left := t.TempDir()
	right := t.TempDir()
	if err := os.WriteFile(filepath.Join(left, "value.txt"), []byte("left"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(right, "value.txt"), []byte("right"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := CompareDirectoryTrees(left, right)
	if err == nil {
		t.Fatal("CompareDirectoryTrees returned nil error")
	}
	if !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("error %q does not mention sha256 mismatch", err)
	}
}

func TestRunParityCaseComparesOutputsAndTrees(t *testing.T) {
	t.Parallel()

	leftRoot := t.TempDir()
	rightRoot := t.TempDir()
	script := `printf "same\n"; printf "same\n" >&2; mkdir -p "$1/out"; printf "payload\n" > "$1/out/data.txt"`
	caseSpec := ParityCase{
		Name: "demo",
		Left: CommandSpec{
			Path: "/bin/sh",
			Args: []string{"-c", script, "sh", leftRoot},
		},
		Right: CommandSpec{
			Path: "/bin/sh",
			Args: []string{"-c", script, "sh", rightRoot},
		},
		TreePairs: []TreePair{{LeftRoot: leftRoot, RightRoot: rightRoot}},
	}

	if err := RunParityCase(context.Background(), caseSpec); err != nil {
		t.Fatalf("RunParityCase returned error: %v", err)
	}
}
