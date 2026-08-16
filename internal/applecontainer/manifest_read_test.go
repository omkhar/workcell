// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func testWorkspaceManifest() WorkspaceManifest {
	return WorkspaceManifest{
		Version:               1,
		TargetKind:            TargetKind,
		TargetProvider:        Provider,
		TargetID:              "tid",
		WorkspaceTransport:    WorkspaceTransport,
		SourceWorkspace:       "/source",
		MaterializationID:     "mid",
		MaterializedWorkspace: "/state/targets/local_vm/apple-container/tid/materializations/mid/workspace",
		ExcludedPaths:         []string{".git"},
		Entries:               []WorkspaceEntry{},
	}
}

func canonicalManifestForTest(t *testing.T, manifest WorkspaceManifest) []byte {
	t.Helper()
	data, err := json.Marshal(manifest)
	mustNil(t, err)
	return append(data, '\n')
}

func TestPersistedWorkspaceManifestRejectsNonExactData(t *testing.T) {
	stateRoot := t.TempDir()
	manifestPath := filepath.Join(stateRoot, "materialization.json")
	valid := canonicalManifestForTest(t, testWorkspaceManifest())
	mustNil(t, os.WriteFile(manifestPath, valid, 0o600))
	var decoded WorkspaceManifest
	mustNil(t, readPersistedManifest(stateRoot, manifestPath, &decoded))

	duplicateEntries := testWorkspaceManifest()
	duplicateEntries.Entries = []WorkspaceEntry{
		{Path: "same", Kind: "dir", Mode: fs.ModeDir | 0o755},
		{Path: "same", Kind: "dir", Mode: fs.ModeDir | 0o755},
	}
	unicodeAliases := testWorkspaceManifest()
	unicodeAliases.Entries = []WorkspaceEntry{
		{Path: "Cafe\u0301", Kind: "dir", Mode: fs.ModeDir | 0o755},
		{Path: "Caf\u00e9", Kind: "dir", Mode: fs.ModeDir | 0o755},
	}
	emptyEntry := testWorkspaceManifest()
	emptyEntry.Entries = []WorkspaceEntry{{}}
	nullEntries := bytes.Replace(valid, []byte(`"entries":[]`), []byte(`"entries":null`), 1)
	duplicateKey := bytes.Replace(valid, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)
	unknownKey := bytes.Replace(valid, []byte(`{"version":1`), []byte(`{"unknown":true,"version":1`), 1)

	for _, testCase := range []struct {
		name string
		data []byte
	}{
		{name: "empty", data: nil},
		{name: "short", data: valid[:len(valid)/2]},
		{name: "trailing-object", data: append(append([]byte(nil), valid...), []byte("{}\n")...)},
		{name: "trailing-delimiter", data: append(append([]byte(nil), valid...), '}')},
		{name: "unknown-key", data: unknownKey},
		{name: "duplicate-key", data: duplicateKey},
		{name: "null-entries", data: nullEntries},
		{name: "duplicate-entry", data: canonicalManifestForTest(t, duplicateEntries)},
		{name: "unicode-entry-alias", data: canonicalManifestForTest(t, unicodeAliases)},
		{name: "empty-entry", data: canonicalManifestForTest(t, emptyEntry)},
		{name: "noncanonical-whitespace", data: append([]byte(" \n"), valid...)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			mustNil(t, os.WriteFile(manifestPath, testCase.data, 0o600))
			var got WorkspaceManifest
			if err := readPersistedManifest(stateRoot, manifestPath, &got); err == nil {
				t.Fatal("invalid persisted workspace manifest was accepted")
			}
		})
	}
}

func TestReadFileSafeBoundedAcceptsExactAndRejectsOverLimit(t *testing.T) {
	const limit = int64(4096)
	stateRoot := t.TempDir()
	path := filepath.Join(stateRoot, "manifest.json")
	for _, testCase := range []struct {
		name    string
		size    int64
		wantErr bool
	}{
		{name: "exact", size: limit},
		{name: "over", size: limit + 1, wantErr: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			mustNil(t, os.WriteFile(path, []byte(strings.Repeat("x", int(testCase.size))), 0o600))
			got, err := readFileSafeBounded(stateRoot, path, "manifest", limit)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("readFileSafeBounded() error = %v, wantErr %t", err, testCase.wantErr)
			}
			if !testCase.wantErr && int64(len(got)) != limit {
				t.Fatalf("read size = %d, want %d", len(got), limit)
			}
		})
	}
}

func TestPersistedManifestRejectsHardlinkAndSpecialLeaf(t *testing.T) {
	stateRoot := t.TempDir()
	manifestPath := filepath.Join(stateRoot, "materialization.json")
	outside := filepath.Join(t.TempDir(), "outside.json")
	mustNil(t, os.WriteFile(outside, canonicalManifestForTest(t, testWorkspaceManifest()), 0o600))
	mustNil(t, os.Link(outside, manifestPath))
	var manifest WorkspaceManifest
	if err := readPersistedManifest(stateRoot, manifestPath, &manifest); err == nil || !strings.Contains(err.Error(), "multiply linked") {
		t.Fatalf("hardlinked manifest error = %v", err)
	}

	mustNil(t, os.Remove(manifestPath))
	mustNil(t, unix.Mkfifo(manifestPath, 0o600))
	result := make(chan error, 1)
	go func() { result <- readPersistedManifest(stateRoot, manifestPath, &manifest) }()
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("FIFO manifest error = %v", err)
		}
	case <-time.After(time.Second):
		fd, _ := unix.Open(manifestPath, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if fd >= 0 {
			defer unix.Close(fd)
		}
		select {
		case <-result:
		case <-time.After(time.Second):
		}
		t.Fatal("FIFO manifest read blocked")
	}
}

func TestPersistedManifestRejectsPostValidationParentRebinding(t *testing.T) {
	root := t.TempDir()
	parent, replacement := filepath.Join(root, "parent"), filepath.Join(root, "replacement")
	manifest := testWorkspaceManifest()
	for _, dir := range []string{parent, replacement} {
		mustNil(t, os.Mkdir(dir, 0o700))
		mustNil(t, os.WriteFile(filepath.Join(dir, "manifest.json"), canonicalManifestForTest(t, manifest), 0o600))
	}
	validated := false
	calls := 0
	openParent := func(root, path string) (int, error) {
		calls++
		if calls == 2 && validated {
			mustNil(t, os.Rename(parent, parent+".saved"))
			mustNil(t, os.Rename(replacement, parent))
		}
		return openParentDirNoCreate(root, path)
	}
	var got WorkspaceManifest
	if err := readPersistedManifestWithParent(root, filepath.Join(parent, "manifest.json"), &got, openParent, func([]byte) error {
		validated = true
		return nil
	}); err == nil || calls != 2 {
		t.Fatalf("parent rebind result: opens=%d error=%v", calls, err)
	}
}

func TestPersistedManifestUsesWorkspaceByteLimit(t *testing.T) {
	stateRoot := t.TempDir()
	manifestPath := filepath.Join(stateRoot, "materialization.json")
	mustNil(t, os.WriteFile(manifestPath, nil, 0o600))
	mustNil(t, os.Truncate(manifestPath, workspaceManifestMaxBytes+1))
	var manifest WorkspaceManifest
	err := readPersistedManifest(stateRoot, manifestPath, &manifest)
	if err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("over-limit persisted manifest error = %v", err)
	}
}

func TestPersistedWorkspaceManifestRejectsPathAndMetadataConfusion(t *testing.T) {
	base := testWorkspaceManifest()
	validDigest := strings.Repeat("a", 64)
	mutations := []func(*WorkspaceManifest){
		func(m *WorkspaceManifest) { m.SourceWorkspace = "source\nevent=forged" },
		func(m *WorkspaceManifest) { m.MaterializedWorkspace = string([]byte{'/', 'x', 0xff}) },
		func(m *WorkspaceManifest) {
			m.Entries = []WorkspaceEntry{{Path: "../escape", Kind: "file", Mode: 0o600, SHA256: validDigest}}
		},
		func(m *WorkspaceManifest) {
			m.Entries = []WorkspaceEntry{{Path: "file", Kind: "file", Mode: 0o600, SHA256: "ABC" + strings.Repeat("a", 61)}}
		},
		func(m *WorkspaceManifest) {
			m.Entries = []WorkspaceEntry{{Path: "link", Kind: "symlink", Mode: fs.ModeSymlink | 0o777, LinkTarget: "/outside"}}
		},
		func(m *WorkspaceManifest) {
			m.Entries = []WorkspaceEntry{
				{Path: "file", Kind: "file", Mode: 0o600, SHA256: validDigest},
				{Path: "link", Kind: "symlink", Mode: fs.ModeSymlink | 0o777, LinkTarget: strings.Repeat("./", workspaceMaxLinkBytes/2) + "file"},
			}
		},
	}
	for index, mutate := range mutations {
		manifest := base
		mutate(&manifest)
		if err := validateWorkspaceManifestStructure(manifest); err == nil {
			t.Fatalf("mutation %d was accepted", index)
		}
	}
}
