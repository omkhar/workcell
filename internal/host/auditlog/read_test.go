// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package auditlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRepeated(t *testing.T, path string, size int, value string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		t.Fatal(err)
	}
	for size > 0 {
		chunk := value
		if len(chunk) > size {
			chunk = chunk[:size]
		}
		if _, err := file.WriteString(chunk); err != nil {
			t.Fatal(err)
		}
		size -= len(chunk)
	}
}

func TestForEachLineAcceptsExactLineLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	writeRepeated(t, path, MaxLineBytes, "x")
	called := false
	if err := ForEachLine(path, func(line string, lineNumber int) error {
		called = true
		if len(line) != MaxLineBytes || lineNumber != 1 {
			t.Fatalf("callback received line length %d number %d", len(line), lineNumber)
		}
		return nil
	}); err != nil {
		t.Fatalf("ForEachLine() exact line limit error = %v", err)
	}
	if !called {
		t.Fatal("ForEachLine() did not report the exact-limit line")
	}
}

func TestForEachLineAcceptsExactLogLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	writeRepeated(t, path, MaxLogBytes, strings.Repeat("x", MaxLineBytes-1)+"\n")
	if err := ForEachLine(path, func(string, int) error { return nil }); err != nil {
		t.Fatalf("ForEachLine() exact log limit error = %v", err)
	}
}

func TestForEachLineRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.log")
	path := filepath.Join(dir, "audit.log")
	if err := os.WriteFile(target, []byte("session_id=attacker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := ForEachLine(path, func(string, int) error { return nil }); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("ForEachLine() error = %v, want symlink rejection", err)
	}
}

func TestForEachLineRejectsHardLink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.log")
	path := filepath.Join(dir, "audit.log")
	if err := os.WriteFile(target, []byte("session_id=attacker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, path); err != nil {
		t.Fatal(err)
	}
	if err := ForEachLine(path, func(string, int) error { return nil }); err == nil || !strings.Contains(err.Error(), "expected one") {
		t.Fatalf("ForEachLine() error = %v, want hard-link rejection", err)
	}
}

func TestForEachLineRejectsNonOwnerOnlyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	if err := os.WriteFile(path, []byte("session_id=attacker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ForEachLine(path, func(string, int) error { return nil }); err == nil || !strings.Contains(err.Error(), "owner-only") {
		t.Fatalf("ForEachLine() error = %v, want owner-only rejection", err)
	}
}

func TestForEachLineRejectsWritableParent(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "unsafe")
	if err := os.Mkdir(parent, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o777); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "audit.log")
	if err := os.WriteFile(path, []byte("session_id=attacker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ForEachLine(path, func(string, int) error { return nil }); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("ForEachLine() error = %v, want unsafe-parent rejection", err)
	}
}

func TestForEachLineRejectsSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "parent")
	outsideDir := filepath.Join(root, "outside")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "audit.log"), []byte("session_id=real\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outsideDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "audit.log"), []byte("session_id=outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(realDir, filepath.Join(root, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, realDir); err != nil {
		t.Fatal(err)
	}
	if err := ForEachLine(filepath.Join(realDir, "audit.log"), func(string, int) error { return nil }); err == nil {
		t.Fatal("ForEachLine() followed a symlinked parent")
	}
}
