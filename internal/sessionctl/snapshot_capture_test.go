// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const snapshotTestOrigin = "0000000000000000000000000000000000000000000000000000000000000001"

func snapshotTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir,
		"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func snapshotTestWorkspace(t *testing.T) (workspace, head string) {
	t.Helper()
	workspace = filepath.Join(t.TempDir(), "ws")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	snapshotTestGit(t, workspace, "init", "-q")
	if err := os.WriteFile(filepath.Join(workspace, "tracked.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshotTestGit(t, workspace, "add", "tracked.txt")
	snapshotTestGit(t, workspace, "commit", "-q", "-m", "base")
	head = snapshotTestGit(t, workspace, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(workspace, "tracked.txt"), []byte("updated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "new.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return workspace, head
}

func snapshotTestOptions(t *testing.T, workspace, head string) snapshotCaptureOptions {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git is not available: %v", err)
	}
	return snapshotCaptureOptions{
		GitBin:     gitBin,
		StoreRoot:  filepath.Join(t.TempDir(), "workcell-snapshots"),
		Workspace:  workspace,
		GitHead:    head,
		OriginHash: snapshotTestOrigin,
		SessionID:  "session-1",
		SnapshotID: "snap-1",
	}
}

func TestSnapshotCapturePublishesCommitToStore(t *testing.T) {
	workspace, head := snapshotTestWorkspace(t)
	indexBefore, err := os.ReadFile(filepath.Join(workspace, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	opts := snapshotTestOptions(t, workspace, head)
	tree, commit, err := snapshotCapture(opts)
	if err != nil {
		t.Fatalf("snapshotCapture error = %v", err)
	}
	store := filepath.Join(opts.StoreRoot, snapshotTestOrigin+".git")
	ref := "refs/workcell/snapshots/session-1/snap-1"
	if got := snapshotTestGit(t, store, "rev-parse", ref); got != commit {
		t.Fatalf("store ref = %s, want %s", got, commit)
	}
	if got := snapshotTestGit(t, store, "rev-parse", commit+"^"); got != head {
		t.Fatalf("snapshot parent = %s, want %s", got, head)
	}
	if got := snapshotTestGit(t, store, "show", tree+":new.txt"); got != "new" {
		t.Fatalf("new.txt = %q", got)
	}
	snapshotTestGit(t, store, "fsck", "--strict", "--no-dangling")
	indexAfter, err := os.ReadFile(filepath.Join(workspace, ".git", "index"))
	if err != nil || !bytes.Equal(indexBefore, indexAfter) {
		t.Fatalf("workspace index changed: %v", err)
	}
	info, err := os.Stat(store)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("store mode = %v, %v; want 0700", info, err)
	}
}

// Negative control: a store directory replaced by a symlink is refused and
// nothing is written through it.
func TestSnapshotCaptureRefusesSymlinkedStore(t *testing.T) {
	workspace, head := snapshotTestWorkspace(t)
	opts := snapshotTestOptions(t, workspace, head)
	outside := t.TempDir()
	if err := os.MkdirAll(opts.StoreRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(opts.StoreRoot, snapshotTestOrigin+".git")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := snapshotCapture(opts); err == nil {
		t.Fatal("snapshotCapture accepted a symlinked store directory")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("snapshotCapture wrote through the symlink: %v", entries)
	}
}

func TestSnapshotCaptureRefusesSymlinkedStoreRoot(t *testing.T) {
	workspace, head := snapshotTestWorkspace(t)
	opts := snapshotTestOptions(t, workspace, head)
	outside := t.TempDir()
	if err := os.Symlink(outside, opts.StoreRoot); err != nil {
		t.Fatal(err)
	}
	if _, _, err := snapshotCapture(opts); err == nil {
		t.Fatal("snapshotCapture accepted a symlinked store root")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("snapshotCapture wrote through the symlink: %v", entries)
	}
}

func TestRequireSameDirectoryDetectsSwap(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "store")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close() //nolint:errcheck // test fixture
	if err := requireSameDirectory(held, dir); err != nil {
		t.Fatalf("unchanged directory rejected: %v", err)
	}
	if err := os.Rename(dir, filepath.Join(root, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := requireSameDirectory(held, dir); err == nil {
		t.Fatal("a swapped directory was accepted")
	}
}

func TestSnapshotCaptureRejectsUnsafeInputs(t *testing.T) {
	workspace, head := snapshotTestWorkspace(t)
	cases := map[string]func(*snapshotCaptureOptions){
		"option head":     func(o *snapshotCaptureOptions) { o.GitHead = "--output=/tmp/x" },
		"bad origin":      func(o *snapshotCaptureOptions) { o.OriginHash = "../escape" },
		"bad session":     func(o *snapshotCaptureOptions) { o.SessionID = "a/../b" },
		"bad snapshot":    func(o *snapshotCaptureOptions) { o.SnapshotID = "x y" },
		"relative store":  func(o *snapshotCaptureOptions) { o.StoreRoot = "store" },
		"missing git dir": func(o *snapshotCaptureOptions) { o.Workspace = t.TempDir() },
	}
	for name, mutate := range cases {
		opts := snapshotTestOptions(t, workspace, head)
		mutate(&opts)
		if _, _, err := snapshotCapture(opts); err == nil {
			t.Fatalf("%s: snapshotCapture succeeded", name)
		}
		if _, err := os.Stat(opts.StoreRoot); err == nil {
			t.Fatalf("%s: store created on rejection", name)
		}
	}
}

func TestSnapshotCaptureFailsForMissingGitHead(t *testing.T) {
	workspace, _ := snapshotTestWorkspace(t)
	opts := snapshotTestOptions(t, workspace, strings.Repeat("0", 40))
	if _, _, err := snapshotCapture(opts); err == nil {
		t.Fatal("snapshotCapture succeeded with a missing head")
	}
	if _, err := os.Stat(filepath.Join(opts.StoreRoot, snapshotTestOrigin+".git", "refs", "workcell")); err == nil {
		t.Fatal("a failed capture left a ref in the store")
	}
}

func TestSnapshotCaptureMainRejectsUnknownOption(t *testing.T) {
	var out bytes.Buffer
	if err := snapshotCaptureMain([]string{"--bogus"}, &out); err == nil || out.Len() != 0 {
		t.Fatalf("snapshotCaptureMain error = %v, out = %q", err, out.String())
	}
}
