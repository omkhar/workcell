// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/host/auditseal"
	"github.com/omkhar/workcell/internal/host/sessions"
)

// newRestoreFixture is a fork fixture whose session ended: a signed exit
// record follows the launch and snapshot records.
func newRestoreFixture(t *testing.T, launch ...string) *forkFixture {
	t.Helper()
	if len(launch) == 0 {
		launch = []string{"vm_cpu=4", "vm_memory_gib=10", "vm_disk_gib=80", "container_cpu=unmanaged", "container_memory=8g"}
	}
	f := newForkFixtureWithVM(t, "managed-tier1", "managed-mutable", launch...)
	f.appendRecord(t, "event=exit", "source=host-stop-fallback", "exit_status=0")
	f.sign(t)
	return f
}

func (f *forkFixture) restore(extra ...string) (string, error) {
	var out bytes.Buffer
	args := append([]string{"--root=" + f.root, "--signing-dir=" + f.signingDir}, extra...)
	err := restoreMain(args, &out, io.Discard)
	return out.String(), err
}

func TestRestoreMainEmitsPlanFromSignedRecords(t *testing.T) {
	f := newRestoreFixture(t)
	out, err := f.restore("--id", "parent-1", "--snapshot", "snap-1")
	if err != nil {
		t.Fatalf("restoreMain error = %v", err)
	}
	origin := sha256.Sum256([]byte("/tmp/origin-repo"))
	want := strings.Join([]string{
		"session_id=parent-1", "profile=wcl-fixture", "workspace=/tmp/clone",
		"origin_hash=" + hex.EncodeToString(origin[:]), "launch_head=" + forkFixtureHead, "snapshot_id=snap-1",
		"commit=" + forkFixtureCommit, "tree=" + forkFixtureTree,
	}, "\n") + "\n"
	if out != want {
		t.Fatalf("plan =\n%s\nwant\n%s", out, want)
	}
}

func TestRestoreMainRequiresSignedExitRecord(t *testing.T) {
	f := newForkFixture(t, "managed-tier1")
	if _, err := f.restore("--id", "parent-1", "--snapshot", "snap-1"); err == nil || !strings.Contains(err.Error(), "signed exit record") {
		t.Fatalf("running session error = %v", err)
	}
	// An exit record after the signed head is not proof.
	f.appendRecord(t, "event=exit", "source=host-stop-fallback", "exit_status=0")
	if _, err := f.restore("--id", "parent-1", "--snapshot", "snap-1"); err == nil || !strings.Contains(err.Error(), "signed exit record") {
		t.Fatalf("unsigned exit error = %v", err)
	}
}

func TestRestoreMainRefusesDirectWorkspace(t *testing.T) {
	for _, origin := range []string{"workspace_origin=/tmp/clone", "workspace_origin="} {
		f := newRestoreFixture(t, "vm_cpu=4", "vm_memory_gib=10", "vm_disk_gib=80", "container_cpu=unmanaged", "container_memory=8g", origin)
		if _, err := f.restore("--id", "parent-1", "--snapshot", "snap-1"); err == nil || !strings.Contains(err.Error(), "isolated session workspace") {
			t.Fatalf("%s error = %v", origin, err)
		}
	}
}

func TestRestoreMainRefusesUnsignedOrUnknownSnapshot(t *testing.T) {
	f := newRestoreFixture(t)
	f.appendRecord(t, "event=session_snapshot", "source=host-cli", "snapshot_id=forged",
		"tree="+forkFixtureTree, "commit="+forkFixtureCommit, "snapshot_workspace=/tmp/clone", "snapshot_head="+forkFixtureHead)
	for _, id := range []string{"forged", "snap-9"} {
		if _, err := f.restore("--id", "parent-1", "--snapshot", id); err == nil || !strings.Contains(err.Error(), "no signed session_snapshot record "+id) {
			t.Fatalf("%s error = %v", id, err)
		}
	}
}

func TestRestoreMainRefusesSnapshotOfAnotherWorkspace(t *testing.T) {
	f := newRestoreFixture(t)
	f.appendRecord(t, "event=session_snapshot", "source=host-cli", "snapshot_id=snap-2",
		"tree="+forkFixtureTree, "commit="+forkFixtureCommit, "snapshot_workspace=/tmp/other", "snapshot_head="+forkFixtureHead)
	f.sign(t)
	if _, err := f.restore("--id", "parent-1", "--snapshot", "snap-2"); err == nil || !strings.Contains(err.Error(), "did not capture the signed launch") {
		t.Fatalf("capture error = %v", err)
	}
}

func TestRestoreMainBindsRecordToSignedLaunch(t *testing.T) {
	f := newRestoreFixture(t)
	if err := sessions.WriteSessionRecord(f.recordPath, map[string]string{
		"session_id": "parent-1", "profile": "wcl-fixture", "target_provider": "colima", "execution_path": "managed-tier1",
		"agent": "codex", "mode": "strict", "status": "running", "live_status": "running",
		"monitor_pid": "4242", "session_audit_dir": "/tmp/audit-fixture", "workspace": "/tmp/other-clone", "workspace_origin": "/tmp/origin-repo",
		"git_head": forkFixtureHead, "started_at": "2026-07-08T00:00:00Z", "audit_log_path": f.logPath,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.restore("--id", "parent-1", "--snapshot", "snap-1"); err == nil || !strings.Contains(err.Error(), "workspace or git head does not match") {
		t.Fatalf("changed workspace error = %v", err)
	}
}

func TestRestoreMainRefusesUnsealedOrAttachedSession(t *testing.T) {
	f := newRestoreFixture(t)
	if err := os.Remove(auditseal.SealPathForRecord(f.recordPath)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.restore("--id", "parent-1", "--snapshot", "snap-1"); err == nil || !strings.Contains(err.Error(), "requires a signed audit chain") {
		t.Fatalf("unsealed error = %v", err)
	}
	g := newRestoreFixture(t)
	// WriteSessionRecord merges into the old record, so remove it first.
	if err := os.Remove(g.recordPath); err != nil {
		t.Fatal(err)
	}
	if err := sessions.WriteSessionRecord(g.recordPath, map[string]string{
		"session_id": "parent-1", "profile": "wcl-fixture", "target_provider": "colima", "agent": "codex", "mode": "strict",
		"status": "exited", "workspace": "/tmp/clone", "started_at": "2026-07-08T00:00:00Z", "finished_at": "2026-07-08T00:01:00Z", "exit_status": "0", "final_assurance": "managed-mutable", "audit_log_path": g.logPath,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.restore("--id", "parent-1", "--snapshot", "snap-1"); err == nil || !strings.Contains(err.Error(), "only works for detached sessions") {
		t.Fatalf("attached error = %v", err)
	}
}

func TestRestoreMainValidatesArguments(t *testing.T) {
	cases := map[string][]string{
		"requires --id":                {"--signing-dir=/s", "--snapshot", "snap-1"},
		"requires --snapshot":          {"--signing-dir=/s", "--id", "parent-1"},
		"requires --signing-dir":       {"--id", "parent-1", "--snapshot", "snap-1"},
		"is not a snapshot id":         {"--signing-dir=/s", "--id", "parent-1", "--snapshot", "../x"},
		"--signing-dir only once":      {"--signing-dir=/s", "--signing-dir=/t", "--id", "parent-1", "--snapshot", "snap-1"},
		"Unsupported workcell session": {"--signing-dir=/s", "--id", "parent-1", "--snapshot", "snap-1", "--force"},
	}
	for want, args := range cases {
		if err := restoreMain(args, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%v error = %v, want %q", args, err, want)
		}
	}
}

// restoreTestSnapshots captures two snapshots of one workspace into one store
// and returns the options to restore the first.
func restoreTestSnapshots(t *testing.T) (opts restoreApplyOptions, capture snapshotCaptureOptions, second string) {
	t.Helper()
	workspace, head := snapshotTestWorkspace(t)
	capture = snapshotTestOptions(t, workspace, head)
	tree, commit, err := snapshotCapture(capture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "tracked.txt"), []byte("later\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(workspace, "new.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "other.txt"), []byte("other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	later := capture
	later.SnapshotID = "snap-2"
	if _, second, err = snapshotCapture(later); err != nil {
		t.Fatal(err)
	}
	return restoreApplyOptions{
		GitBin: capture.GitBin, StoreRoot: capture.StoreRoot, Workspace: workspace,
		OriginHash: snapshotTestOrigin, Commit: commit, Tree: tree, Head: head,
	}, capture, second
}

// TestRestoreApplyUsesSignedObjectIDNotStoreRef is the ref-tamper test of the
// snapshot-store-host-owned invariant: a store ref that names another commit
// does not change what restore applies, and the session clone .git is not read.
func TestRestoreApplyUsesSignedObjectIDNotStoreRef(t *testing.T) {
	opts, capture, second := restoreTestSnapshots(t)
	store := filepath.Join(capture.StoreRoot, snapshotTestOrigin+".git")
	snapshotTestGit(t, store, "update-ref", "refs/workcell/snapshots/session-1/snap-1", second)
	if err := os.WriteFile(filepath.Join(opts.Workspace, "junk.txt"), []byte("junk\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A core.worktree in the clone config would move a git run that reads it.
	elsewhere := t.TempDir()
	snapshotTestGit(t, opts.Workspace, "config", "core.worktree", elsewhere)
	gitDirBefore := snapshotTestGit(t, opts.Workspace, "--git-dir=.git", "rev-parse", "HEAD")
	indexBefore, err := os.ReadFile(filepath.Join(opts.Workspace, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	args := []string{"--git-bin", opts.GitBin, "--store-root", opts.StoreRoot, "--workspace", opts.Workspace,
		"--origin-hash", opts.OriginHash, "--commit", opts.Commit, "--tree", opts.Tree, "--head", opts.Head}
	if err := restoreApplyMain(args, &out); err != nil {
		t.Fatalf("restoreApplyMain error = %v", err)
	}
	if out.String() != "tree="+opts.Tree+"\ncommit="+opts.Commit+"\n" {
		t.Fatalf("output = %q", out.String())
	}
	for name, want := range map[string]string{"tracked.txt": "updated\n", "new.txt": "new\n"} {
		if got, err := os.ReadFile(filepath.Join(opts.Workspace, name)); err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
	for _, name := range []string{"other.txt", "junk.txt"} {
		if _, err := os.Lstat(filepath.Join(opts.Workspace, name)); !os.IsNotExist(err) {
			t.Fatalf("%s stayed after the restore: %v", name, err)
		}
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatalf("restore wrote into the core.worktree path: %v", entries)
	}
	indexAfter, err := os.ReadFile(filepath.Join(opts.Workspace, ".git", "index"))
	if err != nil || !bytes.Equal(indexBefore, indexAfter) {
		t.Fatalf("restore changed the session clone index: %v", err)
	}
	if got := snapshotTestGit(t, opts.Workspace, "--git-dir=.git", "rev-parse", "HEAD"); got != gitDirBefore {
		t.Fatalf("restore moved HEAD to %s", got)
	}
}

func TestRestoreApplyRefusesUnsignedValues(t *testing.T) {
	opts, _, second := restoreTestSnapshots(t)
	cases := map[string]func(*restoreApplyOptions){
		"another commit":        func(o *restoreApplyOptions) { o.Commit = second },
		"another tree":          func(o *restoreApplyOptions) { o.Tree = strings.Repeat("2", 40) },
		"another head":          func(o *restoreApplyOptions) { o.Head = strings.Repeat("4", 40) },
		"a commit not in store": func(o *restoreApplyOptions) { o.Commit = strings.Repeat("3", 40) },
		"a relative workspace":  func(o *restoreApplyOptions) { o.Workspace = "ws" },
		"a short origin hash":   func(o *restoreApplyOptions) { o.OriginHash = "00" },
	}
	for name, mutate := range cases {
		changed := opts
		mutate(&changed)
		if err := restoreApply(changed); err == nil {
			t.Errorf("%s: restoreApply succeeded", name)
		}
	}
	// An option-shaped value is refused before it reaches a git argument list.
	optionShaped := opts
	optionShaped.Commit = "--upload-pack=x"
	if err := restoreApply(optionShaped); err == nil || !strings.Contains(err.Error(), "not an object id") {
		t.Fatalf("option-shaped commit error = %v", err)
	}
	// A symlinked workspace is refused before git runs.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(opts.Workspace, link); err != nil {
		t.Fatal(err)
	}
	symlinked := opts
	symlinked.Workspace = link
	if err := restoreApply(symlinked); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("symlinked workspace error = %v", err)
	}
	// A symlink in an ancestor of the workspace is refused too.
	ancestorLink := filepath.Join(t.TempDir(), "parent-link")
	if err := os.Symlink(filepath.Dir(opts.Workspace), ancestorLink); err != nil {
		t.Fatal(err)
	}
	viaAncestor := opts
	viaAncestor.Workspace = filepath.Join(ancestorLink, filepath.Base(opts.Workspace))
	if err := restoreApply(viaAncestor); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("workspace behind a symlinked ancestor error = %v", err)
	}
	// None of the refusals changed the worktree.
	if got, err := os.ReadFile(filepath.Join(opts.Workspace, "tracked.txt")); err != nil || string(got) != "later\n" {
		t.Fatalf("tracked.txt = %q, %v", got, err)
	}
}
