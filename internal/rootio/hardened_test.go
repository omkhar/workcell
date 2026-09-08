// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package rootio_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/omkhar/workcell/internal/rootio"
)

// Each row is a leaf shape the reviewer found accepted by a check that read
// only the file type, or only the name.
func TestRequireSingleLinkedRegular(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	regular := filepath.Join(root, "regular")
	if err := os.WriteFile(regular, []byte("x"), 0o600); err != nil {
		t.Fatalf("write the regular file: %v", err)
	}
	// A hard link raises the link count of both names, so the clean case needs
	// a file that nothing links to.
	shared := filepath.Join(root, "shared")
	if err := os.WriteFile(shared, []byte("x"), 0o600); err != nil {
		t.Fatalf("write the shared file: %v", err)
	}
	linked := filepath.Join(root, "linked")
	if err := os.Link(shared, linked); err != nil {
		t.Fatalf("create the hard link: %v", err)
	}
	symlink := filepath.Join(root, "symlink")
	if err := os.Symlink(regular, symlink); err != nil {
		t.Fatalf("create the symlink: %v", err)
	}
	fifo := filepath.Join(root, "fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("create the FIFO: %v", err)
	}
	directory := filepath.Join(root, "directory")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatalf("create the directory: %v", err)
	}

	cases := []struct {
		name    string
		path    string
		wantErr string
	}{
		{name: "a single-linked regular file", path: regular},
		{name: "a hard link", path: linked, wantErr: "multiply linked"},
		{name: "a symlink", path: symlink, wantErr: "not a regular file"},
		{name: "a FIFO", path: fifo, wantErr: "not a regular file"},
		{name: "a directory", path: directory, wantErr: "not a regular file"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var info unix.Stat_t
			if err := unix.Lstat(testCase.path, &info); err != nil {
				t.Fatalf("stat the fixture: %v", err)
			}
			err := rootio.RequireSingleLinkedRegular(&info, "fixture", testCase.path)
			if testCase.wantErr == "" {
				if err != nil {
					t.Fatalf("expected a clean result, found %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("expected an error containing %q, found %v", testCase.wantErr, err)
			}
		})
	}
	// A missing stat is an error, not a pass: a caller that forgot to stat
	// would otherwise take the clean branch.
	if err := rootio.RequireSingleLinkedRegular(nil, "fixture", "missing"); err == nil {
		t.Fatal("expected a missing stat to fail")
	}
}

func TestMkdirAllSyncedAt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		relative string
		wantErr  string
	}{
		{name: "one component", relative: "one"},
		{name: "nested components", relative: "one/two/three"},
		{name: "an existing tree", relative: "one/two/three"},
		{name: "an absolute path", relative: "/one", wantErr: "must be relative"},
		{name: "a parent escape", relative: "one/../../two", wantErr: "must stay within"},
		{name: "an empty path", relative: "", wantErr: "must be relative"},
		{name: "a bare dot", relative: ".", wantErr: "names no directory"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			parent := openDirectory(t, root)
			defer parent.Close() //nolint:errcheck // test fixture
			if testCase.name == "an existing tree" {
				if err := os.MkdirAll(filepath.Join(root, testCase.relative), 0o700); err != nil {
					t.Fatalf("pre-create the tree: %v", err)
				}
			}
			err := rootio.MkdirAllSyncedAt(parent, testCase.relative, 0o700)
			if testCase.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("expected an error containing %q, found %v", testCase.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected a clean result, found %v", err)
			}
			info, statErr := os.Stat(filepath.Join(root, testCase.relative))
			if statErr != nil || !info.IsDir() {
				t.Fatalf("expected a directory at %s, found %v", testCase.relative, statErr)
			}
		})
	}
}

// A symlink in the middle of the path is refused rather than followed, so the
// tree cannot be redirected outside the opened parent.
func TestMkdirAllSyncedAtRejectsASymlinkedComponent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatalf("create the outside directory: %v", err)
	}
	inside := filepath.Join(root, "inside")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatalf("create the inside directory: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(inside, "link")); err != nil {
		t.Fatalf("create the symlinked component: %v", err)
	}
	parent := openDirectory(t, inside)
	defer parent.Close() //nolint:errcheck // test fixture

	if err := rootio.MkdirAllSyncedAt(parent, "link/below", 0o700); err == nil {
		t.Fatal("expected a symlinked component to fail")
	}
	if _, err := os.Stat(filepath.Join(outside, "below")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the call created a directory through the symlink")
	}
}

func TestStageAndPublishAt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	parent := openDirectory(t, root)
	defer parent.Close() //nolint:errcheck // test fixture

	if err := rootio.StageAndPublishAt(parent, "record", []byte("first"), 0o600, ""); err != nil {
		t.Fatalf("publish the first record: %v", err)
	}
	// Publication replaces, so the second write wins.
	if err := rootio.StageAndPublishAt(parent, "record", []byte("second"), 0o600, ""); err != nil {
		t.Fatalf("replace the record: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "record"))
	if err != nil || string(content) != "second" {
		t.Fatalf("expected the replacement content, found %q %v", content, err)
	}
	// The mode is set through the descriptor, so a restrictive umask cannot
	// publish an unreadable file.
	info, err := os.Stat(filepath.Join(root, "record"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("expected mode 0600, found %v %v", info.Mode().Perm(), err)
	}
	// Only the published name is left; every staged sibling is gone.
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "record" {
		t.Fatalf("expected only the published record, found %v %v", entries, err)
	}
}

func TestStageAndCreateAtRefusesAnExistingName(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	parent := openDirectory(t, root)
	defer parent.Close() //nolint:errcheck // test fixture

	if err := rootio.StageAndCreateAt(parent, "record", []byte("first"), 0o600, ""); err != nil {
		t.Fatalf("create the record: %v", err)
	}
	err := rootio.StageAndCreateAt(parent, "record", []byte("second"), 0o600, "")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected the second create to fail, found %v", err)
	}
	content, readErr := os.ReadFile(filepath.Join(root, "record"))
	if readErr != nil || string(content) != "first" {
		t.Fatalf("expected the first record to survive, found %q %v", content, readErr)
	}
	// The refused create leaves no staged sibling behind.
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected only the published record, found %v %v", entries, err)
	}
}

// The published file is a single-linked regular file, so the hardened readers
// accept what the publisher wrote.
func TestStageAndPublishAtLeavesOneLink(t *testing.T) {
	t.Parallel()
	for _, publish := range []struct {
		name  string
		write func(*os.File, string, []byte, os.FileMode, string) error
	}{
		{"replace", rootio.StageAndPublishAt},
		{"create", rootio.StageAndCreateAt},
	} {
		t.Run(publish.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			parent := openDirectory(t, root)
			defer parent.Close() //nolint:errcheck // test fixture
			if err := publish.write(parent, "record", []byte("x"), 0o600, ""); err != nil {
				t.Fatalf("publish the record: %v", err)
			}
			var info unix.Stat_t
			if err := unix.Lstat(filepath.Join(root, "record"), &info); err != nil {
				t.Fatalf("stat the record: %v", err)
			}
			if err := rootio.RequireSingleLinkedRegular(&info, "record", "record"); err != nil {
				t.Fatalf("the published record fails the reader's own rule: %v", err)
			}
		})
	}
}

func TestStageAndPublishAtRejectsAnUnsafeName(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	parent := openDirectory(t, root)
	defer parent.Close() //nolint:errcheck // test fixture

	for _, testCase := range []struct{ name, leaf, prefix string }{
		{"a path in the name", "sub/record", ""},
		{"a parent escape in the name", "..", ""},
		{"a path in the prefix", "record", "sub/tmp-"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if err := rootio.StageAndPublishAt(parent, testCase.leaf, []byte("x"), 0o600, testCase.prefix); err == nil {
				t.Fatal("expected the unsafe name to fail")
			}
		})
	}
}

func openDirectory(t *testing.T, path string) *os.File {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open the directory: %v", err)
	}
	return file
}
