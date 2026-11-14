// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
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

func TestSendWorkspaceArchiveRejectsAnIDOutsideTheFrame(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"m 1", "m\n1", "m\r1", "", "a/b"} {
		var sent bytes.Buffer
		if _, err := sendWorkspaceArchive(struct {
			io.Reader
			io.Writer
		}{strings.NewReader(""), &sent}, id, strings.NewReader("tar"), 3, strings.Repeat("a", 64)); err == nil {
			t.Fatalf("id %q: send succeeded", id)
		}
		if sent.Len() != 0 {
			t.Fatalf("id %q: sent %q before the refusal", id, sent.String())
		}
	}
}

// TestReceiveWorkspaceRefusesASymlinkedRoot keeps the receiver from writing
// into, or deleting from, a directory that a symlink on the root path or at
// the materializations directory points to.
func TestReceiveWorkspaceRefusesASymlinkedRoot(t *testing.T) {
	t.Parallel()

	frame := frameArchive("m1", buildTestArchive(t, file("a.txt", "alpha")))
	for name, setup := range map[string]func(t *testing.T, base, outside string) string{
		"root is a symlink": func(t *testing.T, base, outside string) string {
			root := filepath.Join(base, "root")
			mustSymlink(t, outside, root)
			return root
		},
		"root ancestor is a symlink": func(t *testing.T, base, outside string) string {
			mustSymlink(t, filepath.Dir(outside), filepath.Join(base, "up"))
			return filepath.Join(base, "up", filepath.Base(outside))
		},
		"materializations escapes the root": func(t *testing.T, base, outside string) string {
			root := filepath.Join(base, "root")
			mustMkdir(t, root)
			mustSymlink(t, outside, filepath.Join(root, "materializations"))
			return root
		},
		"materializations stays inside the root": func(t *testing.T, base, outside string) string {
			root := filepath.Join(base, "root")
			mustMkdir(t, root)
			mustSymlink(t, outside, filepath.Join(root, "inside"))
			mustSymlink(t, "inside", filepath.Join(root, "materializations"))
			return root
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			outside := filepath.Join(t.TempDir(), "outside")
			mustMkdir(t, filepath.Join(outside, "m1"))
			if err := os.WriteFile(filepath.Join(outside, "m1", "keep"), []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			root := setup(t, base, outside)
			ack, err := receiveFrame(t, root, frame, defaultWorkspaceLimits)
			if err == nil || !strings.HasPrefix(ack, "error ") {
				t.Fatalf("receive error = %v, ack = %q; want a refusal", err, ack)
			}
			names, err := os.ReadDir(outside)
			if err != nil || len(names) != 1 || names[0].Name() != "m1" {
				t.Fatalf("symlink target now holds %v (%v), want only m1", names, err)
			}
			if content, err := os.ReadFile(filepath.Join(outside, "m1", "keep")); err != nil || string(content) != "keep" {
				t.Fatalf("symlink target lost its content: %q, %v", content, err)
			}
			if names, _ := os.ReadDir(filepath.Join(outside, "m1")); len(names) != 1 {
				t.Fatalf("symlink target m1 now holds %v", names)
			}
		})
	}
}

// TestReceiveWorkspaceReplacesASymlinkedMaterializationWithoutFollowingIt
// keeps the clean-up of an earlier materialization from deleting the target of
// a symlink that stands in its place.
func TestReceiveWorkspaceReplacesASymlinkedMaterializationWithoutFollowingIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, filepath.Join(root, "materializations"))
	mustSymlink(t, outside, filepath.Join(root, "materializations", "m1"))
	if _, err := receiveFrame(t, root, frameArchive("m1", buildTestArchive(t, file("a.txt", "alpha"))), defaultWorkspaceLimits); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(outside, "keep")); err != nil || string(content) != "keep" {
		t.Fatalf("symlink target lost its content: %q, %v", content, err)
	}
	if info, err := os.Lstat(filepath.Join(root, "materializations", "m1")); err != nil || !info.IsDir() {
		t.Fatalf("materialization = %v, %v; want a real directory", info, err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// TestReceiveWorkspaceRefusesARootSwappedDuringTheReceive swaps the receiver
// root between a real directory and a symlink while receives run. The swap
// timing varies from run to run, but no receive may ever write into the
// symlink target.
func TestReceiveWorkspaceRefusesARootSwappedDuringTheReceive(t *testing.T) {
	frame := frameArchive("m1", buildTestArchive(t, dir("d"), file("d/a.txt", "alpha"), link("l", "d/a.txt")))
	base := t.TempDir()
	root := filepath.Join(base, "root")
	held := filepath.Join(base, "held")
	swap := filepath.Join(base, "swap")
	outside := filepath.Join(base, "outside")
	mustMkdir(t, root)
	mustMkdir(t, outside)
	mustSymlink(t, outside, swap)
	var stop atomic.Bool
	done := make(chan error, 1)
	go func() {
		// Each rename moves a name to a free name: root is a directory,
		// then missing, then the symlink. A rename fails only when the
		// receiver created a name at root itself.
		for !stop.Load() {
			for _, step := range [][2]string{{root, held}, {swap, root}, {root, swap}, {held, root}} {
				if err := os.Rename(step[0], step[1]); err != nil {
					done <- err
					return
				}
			}
		}
		done <- nil
	}()
	for attempt := range 2000 {
		_, _ = receiveFrame(t, root, frame, defaultWorkspaceLimits)
		if names, _ := os.ReadDir(outside); len(names) != 0 {
			stop.Store(true)
			t.Fatalf("attempt %d: the receiver wrote %v into the symlink target (swap loop: %v)", attempt, names, <-done)
		}
	}
	stop.Store(true)
	if err := <-done; err != nil {
		t.Fatalf("the receiver created a name at its root: %v", err)
	}
}

// TestReceiveWorkspaceRefusesAPathSwappedBetweenItsCheckAndItsOpen swaps the
// receiver root, and then each receiver subdirectory, for a symlink in the gap
// between the check and the open. The identity check must refuse each swap,
// and nothing may land in the symlink target. Not parallel: it replaces
// beforeReceiverOpen.
func TestReceiveWorkspaceRefusesAPathSwappedBetweenItsCheckAndItsOpen(t *testing.T) {
	frame := frameArchive("m1", buildTestArchive(t, dir("d"), file("d/a.txt", "alpha")))
	for _, swapped := range []string{"", "materializations", filepath.Join("materializations", "m1"), filepath.Join("materializations", "m1", MaterializedWorktree)} {
		t.Run("root/"+swapped, func(t *testing.T) {
			// os.Root follows a relative symlink that stays in the parent,
			// so the decoy of a subdirectory is a sibling of it.
			base := t.TempDir()
			root := filepath.Join(base, "root")
			mustMkdir(t, root)
			path := filepath.Join(root, swapped)
			target, link := filepath.Join(base, "outside"), filepath.Join(base, "outside")
			if swapped != "" {
				target, link = filepath.Join(filepath.Dir(path), "decoy"), "decoy"
			}
			var fired bool
			beforeReceiverOpen = func(opened string) {
				if opened != path || fired {
					return
				}
				fired = true
				if err := os.Rename(path, path+".held"); err != nil {
					t.Error(err)
				}
				if err := os.MkdirAll(target, 0o700); err != nil {
					t.Error(err)
				}
				if err := os.Symlink(link, path); err != nil {
					t.Error(err)
				}
			}
			t.Cleanup(func() { beforeReceiverOpen = func(string) {} })
			_, err := receiveFrame(t, root, frame, defaultWorkspaceLimits)
			if !fired {
				t.Fatalf("the receiver never opened %s", path)
			}
			if err == nil || !strings.Contains(err.Error(), "changed while it was opened") {
				t.Fatalf("receive = %v, want the identity check to refuse the swap", err)
			}
			if names, _ := os.ReadDir(target); len(names) != 0 {
				t.Fatalf("the receiver wrote %v into the symlink target", names)
			}
		})
	}
}

// TestReceiveWorkspaceRemovesAReadOnlyMaterialization receives an ID with a
// 0555 directory that has an entry, then a copy that fails after the modes are
// set, then the first again. Without DAC override, as on a non-root guest, the
// 0555 directory blocks the removal of its entry, so the receiver must give the
// directories of a tree owner rwx before it removes the tree. Under root the
// test reruns itself under setpriv without DAC override.
func TestReceiveWorkspaceRemovesAReadOnlyMaterialization(t *testing.T) {
	if os.Geteuid() == 0 && os.Getenv("WORKCELL_TEST_NO_DAC") == "" {
		setpriv, err := exec.LookPath("setpriv")
		if err != nil {
			t.Skip("needs a non-root user or setpriv to drop DAC override")
		}
		drop := "--bounding-set=-dac_override,-dac_read_search"
		// Dropping a bounding capability needs CAP_SETPCAP, which a root in a
		// container can lack.
		if out, err := exec.Command(setpriv, drop, "true").CombinedOutput(); err != nil {
			t.Skipf("setpriv cannot drop DAC override here (it needs CAP_SETPCAP): %v: %s", err, out)
		}
		rerun := exec.Command(setpriv, drop, os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1", "-test.v")
		rerun.Env = append(os.Environ(), "WORKCELL_TEST_NO_DAC=1")
		if out, err := rerun.CombinedOutput(); err != nil || !bytes.Contains(out, []byte("--- PASS: "+t.Name())) {
			t.Fatalf("rerun without DAC override: %v\n%s", err, out)
		}
		return
	}
	root := t.TempDir()
	materialization := filepath.Join(root, "materializations", "m1")
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(materialization, MaterializedWorktree, "ro"), 0o755)
		_ = os.Chmod(filepath.Join(materialization, MaterializedWorktree, "ro", "inner"), 0o755)
	})
	// The inner read-only directory is reached only through the outer one, so
	// the walk must give a directory owner rwx before it lists it.
	readOnly, inner, sealed := dir("ro"), dir("ro/inner"), dir("sealed")
	readOnly.header.Mode, inner.header.Mode, sealed.header.Mode = 0o555, 0o555, 0o100
	frame := frameArchive("m1", buildTestArchive(t, readOnly, inner, file("ro/inner/f", "x")))
	// The sync opens each directory, which a 0100 directory refuses.
	late := frameArchive("m1", buildTestArchive(t, readOnly, inner, file("ro/inner/f", "x"), sealed))
	if _, err := receiveFrame(t, root, frame, defaultWorkspaceLimits); err != nil {
		t.Fatal(err)
	}
	if _, err := receiveFrame(t, root, late, defaultWorkspaceLimits); err == nil {
		t.Fatal("the receiver synced a 0100 directory")
	}
	if _, err := os.Lstat(materialization); !os.IsNotExist(err) {
		t.Fatalf("the failed receive left its materialization: %v", err)
	}
	if ack, err := receiveFrame(t, root, frame, defaultWorkspaceLimits); err != nil || !strings.HasPrefix(ack, "ok ") {
		t.Fatalf("receive after the failure = %q, %v", ack, err)
	}
}

// materializationsLocked reports whether a receive holds the lock on the
// materializations under root. A flock on another open of the directory
// conflicts with it in this process too.
func materializationsLocked(t *testing.T, root string) bool {
	t.Helper()
	dir, err := os.Open(filepath.Join(root, "materializations"))
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	err = syscall.Flock(int(dir.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return false
	}
	if !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatal(err)
	}
	return true
}

// TestReceiveWorkspaceKeepsTheCopyOfAConcurrentReceive starts a second receive
// of an ID while the first unpacks it, and the first then fails. The first
// must hold the lock while it unpacks, and its failure must not remove the
// copy that the second receive acks. Not parallel: it replaces
// beforeReceiverOpen.
func TestReceiveWorkspaceKeepsTheCopyOfAConcurrentReceive(t *testing.T) {
	root := t.TempDir()
	worktree := filepath.Join(root, "materializations", "m1", MaterializedWorktree)
	failing := frameArchive("m1", buildTestArchive(t, file("f", "first"), link("l", "missing")))
	frame := frameArchive("m1", buildTestArchive(t, file("f", "second")))
	var fired, locked atomic.Bool
	var ack string
	var secondErr error
	finished := make(chan struct{})
	beforeReceiverOpen = func(opened string) {
		if opened != worktree || !fired.CompareAndSwap(false, true) {
			return
		}
		locked.Store(materializationsLocked(t, root))
		go func() {
			defer close(finished)
			ack, secondErr = receiveFrame(t, root, frame, defaultWorkspaceLimits)
		}()
	}
	t.Cleanup(func() { beforeReceiverOpen = func(string) {} })
	if _, err := receiveFrame(t, root, failing, defaultWorkspaceLimits); err == nil || !fired.Load() || !locked.Load() {
		t.Fatalf("first receive = %v, second started = %v, lock held = %v; want a late failure under the lock", err, fired.Load(), locked.Load())
	}
	<-finished
	if content, err := os.ReadFile(filepath.Join(worktree, "f")); secondErr != nil || !strings.HasPrefix(ack, "ok ") || string(content) != "second" {
		t.Fatalf("second receive = %q, %v; its copy = %q, %v", ack, secondErr, content, err)
	}
}

// lockCheckingWriter records whether the materializations lock is held when
// the receiver writes its ack.
type lockCheckingWriter struct {
	t      *testing.T
	root   string
	ack    bytes.Buffer
	locked bool
}

func (w *lockCheckingWriter) Write(p []byte) (int, error) {
	w.locked = materializationsLocked(w.t, w.root)
	return w.ack.Write(p)
}

// TestReceiveWorkspaceAcksBeforeItEndsTheLock: a receive must write its ok ack
// while it still holds the lock, so a later receive of the same ID cannot
// replace the copy before the ack names it.
func TestReceiveWorkspaceAcksBeforeItEndsTheLock(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writer := &lockCheckingWriter{t: t, root: root}
	err := receiveWorkspace(struct {
		io.Reader
		io.Writer
	}{bytes.NewReader(frameArchive("m1", buildTestArchive(t, file("f", "x")))), writer}, root, defaultWorkspaceLimits)
	if err != nil || !strings.HasPrefix(writer.ack.String(), "ok ") || !writer.locked {
		t.Fatalf("receive = %q, %v; lock held at the ack = %v", writer.ack.String(), err, writer.locked)
	}
	if materializationsLocked(t, root) {
		t.Fatal("the lock outlived the receive")
	}
}
