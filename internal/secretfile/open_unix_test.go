//go:build unix

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package secretfile

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSecret(tb testing.TB, dir, name string, mode os.FileMode) string {
	tb.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("secret-content\n"), mode); err != nil {
		tb.Fatal(err)
	}
	// WriteFile mode is masked by umask; force the exact mode under test.
	if err := os.Chmod(path, mode); err != nil {
		tb.Fatal(err)
	}
	return path
}

func TestOpenAcceptsOwnedPrivateRegularFile(t *testing.T) {
	path := writeSecret(t, t.TempDir(), "credential", 0o600)

	file, err := Open(path, "credential file", os.Getuid())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(content) != "secret-content\n" {
		t.Fatalf("unexpected content: %q", content)
	}
}

func TestOpenRejectsRelativePath(t *testing.T) {
	if _, err := Open("relative/credential", "credential file", os.Getuid()); err == nil || !strings.Contains(err.Error(), "must point at a file") {
		t.Fatalf("expected relative-path rejection, got %v", err)
	}
}

func TestOpenRejectsMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent")
	if _, err := Open(path, "credential file", os.Getuid()); err == nil || !strings.Contains(err.Error(), "must point at a file") {
		t.Fatalf("expected missing-file rejection, got %v", err)
	}
}

func TestOpenRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir, "credential file", os.Getuid()); err == nil || !strings.Contains(err.Error(), "must point at a file") {
		t.Fatalf("expected directory rejection, got %v", err)
	}
}

func TestOpenRejectsLeafSymlink(t *testing.T) {
	dir := t.TempDir()
	target := writeSecret(t, dir, "credential", 0o600)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(link, "credential file", os.Getuid()); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestOpenRejectsSymlinkParentComponent(t *testing.T) {
	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSecret(t, realDir, "credential", 0o600)
	linkDir := filepath.Join(dir, "linkdir")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatal(err)
	}

	// Linux reports ENOTDIR for O_DIRECTORY|O_NOFOLLOW on a symlink, so the
	// rejection surfaces as "must point at a file" rather than the symlink
	// message; either way the open must fail closed.
	_, err := Open(filepath.Join(linkDir, "credential"), "credential file", os.Getuid())
	if err == nil || (!strings.Contains(err.Error(), "must not be a symlink") && !strings.Contains(err.Error(), "must point at a file")) {
		t.Fatalf("expected parent-symlink rejection, got %v", err)
	}
}

func TestOpenRejectsWrongOwner(t *testing.T) {
	path := writeSecret(t, t.TempDir(), "credential", 0o600)

	if _, err := Open(path, "credential file", os.Getuid()+1); err == nil || !strings.Contains(err.Error(), "must be owned by uid") {
		t.Fatalf("expected wrong-owner rejection, got %v", err)
	}
}

func TestOpenRejectsGroupOrWorldAccess(t *testing.T) {
	for _, mode := range []os.FileMode{0o640, 0o604, 0o660, 0o606, 0o644} {
		path := writeSecret(t, t.TempDir(), "credential", mode)
		if _, err := Open(path, "credential file", os.Getuid()); err == nil || !strings.Contains(err.Error(), "must not be group/world-accessible") {
			t.Fatalf("mode %o: expected permission rejection, got %v", mode, err)
		}
	}
}
