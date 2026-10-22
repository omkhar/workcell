// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

type archiveItem struct {
	header  tar.Header
	content string
}

func buildTestArchive(t *testing.T, items ...archiveItem) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, item := range items {
		header := item.header
		if header.Mode == 0 {
			header.Mode = 0o644
		}
		header.Size = int64(len(item.content))
		if err := writer.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(item.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func frameArchive(id string, archive []byte) []byte {
	sum := sha256.Sum256(archive)
	return append([]byte(fmt.Sprintf("%s %s %s %d\n", archiveMagic, id, hex.EncodeToString(sum[:]), len(archive))), archive...)
}

// receiveFrame runs the receiver over an in-memory stream and returns the ack.
func receiveFrame(t *testing.T, root string, frame []byte, limits workspaceLimits) (string, error) {
	t.Helper()
	var ack bytes.Buffer
	err := receiveWorkspace(struct {
		io.Reader
		io.Writer
	}{bytes.NewReader(frame), &ack}, root, limits)
	return ack.String(), err
}

func dir(name string) archiveItem {
	return archiveItem{header: tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0o755}}
}
func file(name, content string) archiveItem {
	return archiveItem{header: tar.Header{Name: name, Typeflag: tar.TypeReg}, content: content}
}
func link(name, target string) archiveItem {
	return archiveItem{header: tar.Header{Name: name, Typeflag: tar.TypeSymlink, Linkname: target}}
}

func TestReceiveWorkspaceUnpacksAValidArchive(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ack, err := receiveFrame(t, root, frameArchive("m1", buildTestArchive(t, dir("src"), file("src/a.txt", "alpha"), link("src/b", "a.txt"))), defaultWorkspaceLimits)
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "materializations", "m1", MaterializedWorktree)
	if !strings.HasPrefix(ack, "ok ") || !strings.HasSuffix(ack, " "+workspace+"\n") {
		t.Fatalf("ack = %q", ack)
	}
	if content, err := os.ReadFile(filepath.Join(workspace, "src", "b")); err != nil || string(content) != "alpha" {
		t.Fatalf("unpacked link content = %q, err = %v", content, err)
	}
	if info, err := os.Stat(filepath.Dir(workspace)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("materialization root = %v, %v; want mode 0700", info, err)
	}
}

func TestReceiveWorkspaceRejectsHostileArchives(t *testing.T) {
	t.Parallel()

	small := workspaceLimits{maxFileBytes: 8, maxBytes: 12, maxEntries: 4}
	for name, tc := range map[string]struct {
		items  []archiveItem
		limits workspaceLimits
		want   string
	}{
		"parent traversal":       {items: []archiveItem{file("../escape", "x")}, want: "name is unsafe"},
		"absolute name":          {items: []archiveItem{file("/etc/escape", "x")}, want: "name is unsafe"},
		"unclean name":           {items: []archiveItem{dir("a"), file("a/../b", "x")}, want: "name is unsafe"},
		"excluded .git":          {items: []archiveItem{dir(".git"), file(".git/config", "x")}, want: "excluded"},
		"duplicate name":         {items: []archiveItem{file("a", "x"), file("a", "y")}, want: "collision"},
		"missing parent":         {items: []archiveItem{file("a/b", "x")}, want: "no parent directory"},
		"write through symlink":  {items: []archiveItem{dir("d"), link("l", "d"), file("l/x", "x")}, want: "no parent directory"},
		"absolute symlink":       {items: []archiveItem{link("l", "/etc/passwd")}, want: "absolute path"},
		"escaping symlink":       {items: []archiveItem{link("l", "../outside")}, want: "escapes"},
		"symlink chain escape":   {items: []archiveItem{link("a", "."), link("b", "a/..")}, want: "escapes"},
		"dangling symlink":       {items: []archiveItem{link("l", "missing")}, want: "does not resolve"},
		"hard link":              {items: []archiveItem{file("a", "x"), {header: tar.Header{Name: "b", Typeflag: tar.TypeLink, Linkname: "a"}}}, want: "unsupported"},
		"fifo":                   {items: []archiveItem{{header: tar.Header{Name: "p", Typeflag: tar.TypeFifo}}}, want: "unsupported"},
		"char device":            {items: []archiveItem{{header: tar.Header{Name: "c", Typeflag: tar.TypeChar}}}, want: "unsupported"},
		"oversize file":          {items: []archiveItem{file("a", "123456789")}, limits: small, want: "per-file limit"},
		"oversize workspace":     {items: []archiveItem{file("a", "12345678"), file("b", "12345")}, limits: small, want: "aggregate"},
		"too many entries":       {items: []archiveItem{dir("a"), dir("b"), dir("c"), dir("d"), dir("e")}, limits: small, want: "more than 4 entries"},
		"invalid utf-8 name":     {items: []archiveItem{file("bad\xff", "x")}, want: "invalid"},
		"case-folded collision":  {items: []archiveItem{file("A", "x"), file("a", "y")}, want: "collision"},
		"file then child of it":  {items: []archiveItem{file("a", "x"), file("a/b", "y")}, want: "no parent directory"},
		"directory over symlink": {items: []archiveItem{dir("d"), link("l", "d"), dir("l/sub")}, want: "no parent directory"},
	} {
		limits := tc.limits
		if limits == (workspaceLimits{}) {
			limits = defaultWorkspaceLimits
		}
		root := t.TempDir()
		ack, err := receiveFrame(t, root, frameArchive("m1", buildTestArchive(t, tc.items...)), limits)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: receive error = %v, want %q", name, err, tc.want)
		}
		if !strings.HasPrefix(ack, "error ") {
			t.Fatalf("%s: ack = %q, want an error ack", name, ack)
		}
		if _, err := os.Lstat(filepath.Join(root, "materializations", "m1")); !os.IsNotExist(err) {
			t.Fatalf("%s: partial materialization remains: %v", name, err)
		}
	}
}

func TestReceiveWorkspaceVerifiesTheFrameBeforeUnpacking(t *testing.T) {
	t.Parallel()

	archive := buildTestArchive(t, file("a.txt", "alpha"))
	good := frameArchive("m1", archive)
	header, body, _ := bytes.Cut(good, []byte("\n"))
	fields := strings.Fields(string(header))
	for name, tc := range map[string]struct {
		frame  []byte
		limits workspaceLimits
		want   string
	}{
		"digest mismatch": {frame: append([]byte(fmt.Sprintf("%s m1 %s %d\n", archiveMagic, strings.Repeat("0", 64), len(archive))), body...), want: "digest mismatch"},
		"truncated":       {frame: good[:len(good)-10], want: "truncated"},
		"bad magic":       {frame: append([]byte("workcell-workspace-v0 m1 "+fields[2]+" "+fields[3]+"\n"), body...), want: "malformed"},
		"bad id":          {frame: append([]byte(archiveMagic+" .. "+fields[2]+" "+fields[3]+"\n"), body...), want: "single path segment"},
		"upper-case hex":  {frame: append([]byte(archiveMagic+" m1 "+strings.ToUpper(fields[2])+" "+fields[3]+"\n"), body...), want: "digest is malformed"},
		"negative size":   {frame: append([]byte(archiveMagic+" m1 "+fields[2]+" -1\n"), body...), want: "size must be"},
		"oversize":        {frame: good, limits: workspaceLimits{maxFileBytes: 1, maxBytes: 1, maxEntries: 0}, want: "size must be 0 to 1 bytes"},
		"no newline":      {frame: bytes.Repeat([]byte("x"), 5000), want: "header"},
	} {
		limits := tc.limits
		if limits == (workspaceLimits{}) {
			limits = defaultWorkspaceLimits
		}
		root := t.TempDir()
		if _, err := receiveFrame(t, root, tc.frame, limits); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: receive error = %v, want %q", name, err, tc.want)
		}
		if names, _ := os.ReadDir(root); len(names) != 0 {
			t.Fatalf("%s: receiver left %v in its root", name, names)
		}
	}
}

func TestWorkspaceArchiveIsDeterministicAcrossCopies(t *testing.T) {
	t.Parallel()

	var archives [2]bytes.Buffer
	for idx := range archives {
		staged, err := materializeInStateRoot(DefaultContract(), MaterializeRequest{StateRoot: t.TempDir(), TargetID: "t", MaterializationID: "m", SourceWorkspace: filepath.Join("testdata", "source-workspace")})
		if err != nil {
			t.Fatal(err)
		}
		if err := writeWorkspaceArchive(&archives[idx], staged.MaterializedWorkspace, staged.Manifest.Entries); err != nil {
			t.Fatal(err)
		}
	}
	if archives[0].Len() == 0 || !bytes.Equal(archives[0].Bytes(), archives[1].Bytes()) {
		t.Fatal("two copies of one workspace produced different archives")
	}
}

func TestWorkspaceArchiveRejectsAStagedFileThatDiffersFromItsManifest(t *testing.T) {
	t.Parallel()

	staged, err := materializeInStateRoot(DefaultContract(), MaterializeRequest{StateRoot: t.TempDir(), TargetID: "t", MaterializationID: "m", SourceWorkspace: filepath.Join("testdata", "source-workspace")})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range staged.Manifest.Entries {
		if entry.Kind == "file" {
			if err := os.WriteFile(filepath.Join(staged.MaterializedWorkspace, filepath.FromSlash(entry.Path)), []byte("tampered"), 0o600); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if err := writeWorkspaceArchive(&bytes.Buffer{}, staged.MaterializedWorkspace, staged.Manifest.Entries); err == nil || !strings.Contains(err.Error(), "differs from its manifest") {
		t.Fatalf("writeWorkspaceArchive() error = %v, want manifest mismatch", err)
	}
}

func TestSendWorkspaceArchiveRequiresAnAckForTheSameDigest(t *testing.T) {
	t.Parallel()

	digest := strings.Repeat("a", 64)
	for ack, wantErr := range map[string]bool{
		"ok " + digest + " /guest/workspace\n":           false,
		"ok " + strings.Repeat("b", 64) + " /guest/ws\n": true,
		"ok " + digest + " relative/workspace\n":         true,
		"error \"workspace archive digest mismatch\"\n":  true,
		"": true,
	} {
		var sent bytes.Buffer
		workspace, err := sendWorkspaceArchive(struct {
			io.Reader
			io.Writer
		}{strings.NewReader(ack), &sent}, "m1", strings.NewReader("tar"), 3, digest)
		if (err != nil) != wantErr {
			t.Fatalf("ack %q: error = %v, want error %v", ack, err, wantErr)
		}
		if !wantErr && workspace != "/guest/workspace" {
			t.Fatalf("ack %q: workspace = %q", ack, workspace)
		}
		if got, want := sent.String(), archiveMagic+" m1 "+digest+" 3\ntar"; got != want {
			t.Fatalf("sent = %q, want %q", got, want)
		}
	}
}

func TestWorkspaceArchiveRefusesASwappedStagedFile(t *testing.T) {
	t.Parallel()

	for name, swap := range map[string]func(string) error{
		"fifo":   func(path string) error { return syscall.Mkfifo(path, 0o600) },
		"escape": func(path string) error { return os.Symlink("/etc/hosts", path) },
		"oversize": func(path string) error {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				return err
			}
			return os.Truncate(path, defaultWorkspaceLimits.maxFileBytes+1)
		},
	} {
		staged, err := materializeInStateRoot(DefaultContract(), MaterializeRequest{StateRoot: t.TempDir(), TargetID: "t", MaterializationID: "m", SourceWorkspace: filepath.Join("testdata", "source-workspace")})
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range staged.Manifest.Entries {
			if entry.Kind == "file" {
				path := filepath.Join(staged.MaterializedWorkspace, filepath.FromSlash(entry.Path))
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := swap(path); err != nil {
					t.Fatal(err)
				}
				break
			}
		}
		var archive bytes.Buffer
		if err := writeWorkspaceArchive(&archive, staged.MaterializedWorkspace, staged.Manifest.Entries); err == nil {
			t.Fatalf("%s: a swapped staged file was archived", name)
		}
		if archive.Len() > 1<<20 {
			t.Fatalf("%s: %d bytes were archived before the refusal", name, archive.Len())
		}
	}
}
