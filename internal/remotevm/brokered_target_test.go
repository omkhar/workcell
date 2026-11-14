// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestFakeBrokerProcess is the fake broker CLI. A brokered test runs the test
// binary with "-- <mode> <guest root>"; a normal run has no arguments and
// skips. The receive mode serves the real receiver on stdio, as the IAP or
// SSM stdio tunnel would.
func TestFakeBrokerProcess(t *testing.T) {
	args := flag.Args()
	if len(args) != 2 {
		t.Skip("fake broker helper process")
	}
	stdio := struct {
		io.Reader
		io.Writer
	}{os.Stdin, os.Stdout}
	switch args[0] {
	case "receive", "umask077", "resolved":
		if args[0] == "umask077" {
			unix.Umask(0o077)
		}
		// The broker must run as the file that PATH resolved to, not as
		// its bare name.
		if want := filepath.Join(os.Getenv("PATH"), fakeBrokerName); args[0] == "resolved" && os.Args[0] != want {
			fmt.Fprintf(os.Stderr, "broker argv[0] = %q, want %q\n", os.Args[0], want)
			os.Exit(5)
		}
		if err := receiveWorkspace(stdio, args[1], defaultWorkspaceLimits); err != nil {
			os.Exit(3)
		}
	case "lostack":
		// The guest unpacks and syncs the workspace, but its ack never
		// reaches the host.
		if err := receiveWorkspace(struct {
			io.Reader
			io.Writer
		}{os.Stdin, io.Discard}, args[1], defaultWorkspaceLimits); err != nil {
			os.Exit(3)
		}
	case "refuse":
		fmt.Fprintf(os.Stdout, "error %q\n", "refused")
		io.Copy(io.Discard, os.Stdin)
	case "exit7":
		os.Stderr.WriteString(strings.Repeat("noise ", 400) + "ERROR: Failed to lookup instance\n")
		os.Exit(7)
	case "exit0":
		os.Stderr.WriteString("ERROR: Failed to lookup instance\n")
	case "orphan":
		// A grandchild keeps stdout open after the broker exits.
		orphan := exec.Command("sleep", "15")
		orphan.Stdout = os.Stdout
		if err := orphan.Start(); err != nil {
			os.Exit(4)
		}
	case "hang":
		signal.Ignore(syscall.SIGTERM)
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}

// fakeBrokerName is the bare name under which a test puts the fake broker on
// PATH.
const fakeBrokerName = "workcell-fake-broker"

func fakeBrokerTarget(t *testing.T, mode, guestRoot string, started *bool) BrokeredTarget {
	t.Helper()
	return BrokeredTarget{Contract: DefaultGCPVMContract(), Command: func(string) []string {
		*started = true
		return []string{os.Args[0], "-test.run=^TestFakeBrokerProcess$", "--", mode, guestRoot}
	}}
}

func brokeredRequest(t *testing.T, source string) MaterializeRequest {
	return MaterializeRequest{StateRoot: t.TempDir(), TargetID: "workcell-l4-test", MaterializationID: "m1", SourceWorkspace: source}
}

func TestBrokeredTargetMaterializesTheWorkspaceOnTheGuest(t *testing.T) {
	t.Parallel()

	guestRoot := t.TempDir()
	var started bool
	source := filepath.Join("testdata", "source-workspace")
	result, err := fakeBrokerTarget(t, "receive", guestRoot, &started).MaterializeWorkspace(context.Background(), brokeredRequest(t, source))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateMaterialization(DefaultGCPVMContract(), result, source); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(guestRoot, "materializations", "m1", MaterializedWorktree); result.MaterializedWorkspace != want || result.Manifest.MaterializedWorkspace != want {
		t.Fatalf("materialized workspace = %q, want the guest path %q", result.MaterializedWorkspace, want)
	}
	if len(result.Manifest.ArchiveSHA256) != 64 {
		t.Fatalf("archive_sha256 = %q", result.Manifest.ArchiveSHA256)
	}
	written, err := os.ReadFile(result.ManifestPath)
	if err != nil || !strings.Contains(string(written), result.Manifest.ArchiveSHA256) || !strings.Contains(string(written), guestRoot) {
		t.Fatalf("host manifest does not record the guest copy: %s (%v)", written, err)
	}
	guestEntries, err := copyWorkspaceTree(result.MaterializedWorkspace, filepath.Join(t.TempDir(), "mirror"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(guestEntries, result.Manifest.Entries) {
		t.Fatalf("guest entries differ from the manifest:\n%v\n%v", guestEntries, result.Manifest.Entries)
	}
}

// TestBrokeredTargetKeepsTheSourceModesOnTheGuest runs the guest under a
// strict umask, which must not change the modes that the manifest records.
func TestBrokeredTargetKeepsTheSourceModesOnTheGuest(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "open"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"shared", "open/tool"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, mode := range map[string]os.FileMode{"open": 0o777, "open/tool": 0o777, "shared": 0o666} {
		if err := os.Chmod(filepath.Join(source, name), mode); err != nil {
			t.Fatal(err)
		}
	}
	var started bool
	result, err := fakeBrokerTarget(t, "umask077", t.TempDir(), &started).MaterializeWorkspace(context.Background(), brokeredRequest(t, source))
	if err != nil {
		t.Fatal(err)
	}
	guestEntries, err := copyWorkspaceTree(result.MaterializedWorkspace, filepath.Join(t.TempDir(), "mirror"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(guestEntries, result.Manifest.Entries) {
		t.Fatalf("guest entries differ from the manifest:\n%v\n%v", guestEntries, result.Manifest.Entries)
	}
}

func TestRunConformanceWithBrokeredTargetStopsAtBootstrap(t *testing.T) {
	t.Parallel()

	var started bool
	caseSpec := DefaultConformanceCase(t.TempDir(), filepath.Join("testdata", "source-workspace"))
	_, err := RunConformance(context.Background(), fakeBrokerTarget(t, "receive", t.TempDir(), &started), DefaultGCPVMContract(), caseSpec)
	if !errors.Is(err, errBootstrapNotImplemented) {
		t.Fatalf("RunConformance() error = %v, want the bootstrap gap after a valid materialization", err)
	}
}

func TestBrokeredTargetRejectsHostileWorkspacesBeforeTheBrokerStarts(t *testing.T) {
	t.Parallel()

	for name, prepare := range map[string]func(string) error{
		"symlink escape": func(root string) error { return os.Symlink("../../outside", filepath.Join(root, "escape")) },
		"absolute link":  func(root string) error { return os.Symlink("/etc/passwd", filepath.Join(root, "escape")) },
		"fifo":           func(root string) error { return unix.Mkfifo(filepath.Join(root, "pipe"), 0o600) },
	} {
		source := t.TempDir()
		if err := prepare(source); err != nil {
			t.Fatal(err)
		}
		var started bool
		if _, err := fakeBrokerTarget(t, "receive", t.TempDir(), &started).MaterializeWorkspace(context.Background(), brokeredRequest(t, source)); err == nil {
			t.Fatalf("%s: MaterializeWorkspace() succeeded", name)
		}
		if started {
			t.Fatalf("%s: the broker started for a hostile workspace", name)
		}
	}
}

func TestBrokeredTargetRejectsAnOptionLikeTargetID(t *testing.T) {
	t.Parallel()

	var started bool
	req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
	req.TargetID = "--project=other"
	if _, err := fakeBrokerTarget(t, "receive", t.TempDir(), &started).MaterializeWorkspace(context.Background(), req); err == nil || started {
		t.Fatalf("MaterializeWorkspace() error = %v, started = %v; want rejection before the broker", err, started)
	}
}

func TestBrokeredTargetPropagatesTheBrokerExitStatus(t *testing.T) {
	t.Parallel()

	var started bool
	req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
	_, err := fakeBrokerTarget(t, "exit7", t.TempDir(), &started).MaterializeWorkspace(context.Background(), req)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 || !strings.Contains(err.Error(), "Failed to lookup instance") || strings.Count(err.Error(), "noise") > 200 {
		t.Fatalf("MaterializeWorkspace() error = %v, want broker exit status 7 and the stderr tail", err)
	}
	// The broker ran, so the host cannot rule out a guest copy.
	if manifest := readHostManifest(t, req); manifest.GuestTransfer != GuestTransferUnconfirmed {
		t.Fatalf("guest_transfer = %q after a broker failure, want %q", manifest.GuestTransfer, GuestTransferUnconfirmed)
	}
}

// TestBrokeredTargetRefusesAnEmptyBrokerCommandBeforeTheRecord: a zero or an
// empty broker command sends nothing, so it must fail without a panic before
// the unconfirmed manifest records a send.
func TestBrokeredTargetRefusesAnEmptyBrokerCommandBeforeTheRecord(t *testing.T) {
	t.Parallel()

	for _, command := range []func(string) []string{nil, func(string) []string { return []string{} }} {
		req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
		_, err := BrokeredTarget{Contract: DefaultGCPVMContract(), Command: command}.MaterializeWorkspace(context.Background(), req)
		if err == nil || !strings.Contains(err.Error(), "broker command is empty") {
			t.Fatalf("MaterializeWorkspace() = %v, want the empty broker command", err)
		}
		if _, err := os.Lstat(hostManifestPath(req)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("host manifest after an empty broker command: %v; want no record of a send", err)
		}
	}
}

// TestBrokeredTargetRefusesABrokerThatCannotStartBeforeTheRecord: a broker
// program that is missing or not executable, and a context that has ended,
// start no broker and send nothing, so the send must fail before the
// unconfirmed manifest records it.
func TestBrokeredTargetRefusesABrokerThatCannotStartBeforeTheRecord(t *testing.T) {
	t.Parallel()

	notExecutable := filepath.Join(t.TempDir(), "gcloud")
	if err := os.WriteFile(notExecutable, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, program := range []string{"/nonexistent/gcloud", notExecutable} {
		req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
		target := BrokeredTarget{Contract: DefaultGCPVMContract(), Command: func(string) []string { return []string{program, "compute"} }}
		if _, err := target.MaterializeWorkspace(t.Context(), req); err == nil || !strings.Contains(err.Error(), "broker command cannot run") {
			t.Fatalf("%s: MaterializeWorkspace() = %v, want a broker command that cannot run", program, err)
		}
		if _, err := os.Lstat(hostManifestPath(req)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s: host manifest: %v; want no record of a send", program, err)
		}
	}
	req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := fakeBrokerTarget(t, "receive", t.TempDir(), new(bool)).MaterializeWorkspace(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("MaterializeWorkspace() = %v, want the ended context", err)
	}
	if _, err := os.Lstat(hostManifestPath(req)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("host manifest after an ended context: %v; want no record of a send", err)
	}
}

// TestBrokeredTargetStartsTheResolvedBrokerProgram names the broker by a bare
// name that PATH resolves. runBroker must start the file that brokerArgv
// resolved, so the broker sees that path as its argv[0]. Not parallel: it
// sets PATH.
func TestBrokeredTargetStartsTheResolvedBrokerProgram(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(self, filepath.Join(bin, fakeBrokerName)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	guestRoot := t.TempDir()
	target := BrokeredTarget{Contract: DefaultGCPVMContract(), Command: func(string) []string {
		return []string{fakeBrokerName, "-test.run=^TestFakeBrokerProcess$", "--", "resolved", guestRoot}
	}}
	result, err := target.MaterializeWorkspace(t.Context(), brokeredRequest(t, filepath.Join("testdata", "source-workspace")))
	if err != nil {
		t.Fatalf("MaterializeWorkspace() = %v, want the broker started as the resolved file", err)
	}
	if want := filepath.Join(guestRoot, "materializations", "m1", MaterializedWorktree); result.MaterializedWorkspace != want {
		t.Fatalf("materialized workspace = %q, want %q", result.MaterializedWorkspace, want)
	}
}

func hostManifestPath(req MaterializeRequest) string {
	return filepath.Join(req.StateRoot, "targets", TargetKind, GCPVMProvider, req.TargetID, "materializations", req.MaterializationID, MaterializationFile)
}

func readHostManifest(t *testing.T, req MaterializeRequest) WorkspaceManifest {
	t.Helper()
	content, err := os.ReadFile(hostManifestPath(req))
	if err != nil {
		t.Fatal(err)
	}
	var manifest WorkspaceManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestBrokeredTargetKeepsAnUnconfirmedRecordWhenTheAckIsLost(t *testing.T) {
	t.Parallel()

	guestRoot := t.TempDir()
	var started bool
	req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
	if _, err := fakeBrokerTarget(t, "lostack", guestRoot, &started).MaterializeWorkspace(context.Background(), req); err == nil {
		t.Fatal("MaterializeWorkspace() succeeded without an ack")
	}
	guestWorkspace := filepath.Join(guestRoot, "materializations", "m1", MaterializedWorktree)
	if _, err := os.Stat(guestWorkspace); err != nil {
		t.Fatalf("the guest did not keep the workspace: %v", err)
	}
	manifest := readHostManifest(t, req)
	if manifest.GuestTransfer != GuestTransferUnconfirmed || len(manifest.ArchiveSHA256) != 64 {
		t.Fatalf("host manifest = guest_transfer %q, archive_sha256 %q; want an unconfirmed record with the digest", manifest.GuestTransfer, manifest.ArchiveSHA256)
	}
	// A second send of the same ID that fails before the broker starts
	// keeps the record of the guest copy.
	hostile := req
	hostile.SourceWorkspace = t.TempDir()
	if err := unix.Mkfifo(filepath.Join(hostile.SourceWorkspace, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fakeBrokerTarget(t, "receive", guestRoot, &started).MaterializeWorkspace(context.Background(), hostile); err == nil {
		t.Fatal("MaterializeWorkspace() accepted a hostile workspace")
	}
	if kept := readHostManifest(t, req); kept.GuestTransfer != GuestTransferUnconfirmed || kept.ArchiveSHA256 != manifest.ArchiveSHA256 {
		t.Fatalf("host manifest after a failed second send = guest_transfer %q, archive_sha256 %q; want the first record", kept.GuestTransfer, kept.ArchiveSHA256)
	}

	// A second send that the guest acknowledges confirms the record.
	result, err := fakeBrokerTarget(t, "receive", guestRoot, &started).MaterializeWorkspace(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if manifest := readHostManifest(t, req); manifest.GuestTransfer != "" || manifest.MaterializedWorkspace != guestWorkspace || result.Manifest.GuestTransfer != "" {
		t.Fatalf("confirmed manifest = guest_transfer %q, workspace %q", manifest.GuestTransfer, manifest.MaterializedWorkspace)
	}
}

// A refusal does not prove that the guest holds no workspace: the receiver
// keeps an earlier copy with the same ID when it refuses before it unpacks.
func TestBrokeredTargetKeepsAnUnconfirmedRecordWhenTheGuestRefuses(t *testing.T) {
	t.Parallel()

	var started bool
	req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
	_, err := fakeBrokerTarget(t, "refuse", t.TempDir(), &started).MaterializeWorkspace(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "guest refused the workspace archive") {
		t.Fatalf("MaterializeWorkspace() error = %v, want the guest refusal", err)
	}
	if manifest := readHostManifest(t, req); manifest.GuestTransfer != GuestTransferUnconfirmed {
		t.Fatalf("guest_transfer = %q after a refusal, want %q", manifest.GuestTransfer, GuestTransferUnconfirmed)
	}
}

func TestMaterializeRefusesASymlinkedStateDirectory(t *testing.T) {
	t.Parallel()

	req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
	outside := t.TempDir()
	// The path that the request names exists behind the link.
	victim := filepath.Join(outside, TargetKind, GCPVMProvider, req.TargetID, "materializations", req.MaterializationID, MaterializedWorktree)
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(req.StateRoot, "targets")); err != nil {
		t.Fatal(err)
	}
	if _, err := materializeInStateRoot(DefaultGCPVMContract(), req); err == nil {
		t.Fatal("materializeInStateRoot() followed a symlinked state directory")
	}
	if entries, err := os.ReadDir(victim); err != nil || len(entries) != 0 {
		t.Fatalf("directory behind the link = %v, %v; want it present and empty", entries, err)
	}
}

func TestMaterializeCreatesAStateRootBelowMissingParents(t *testing.T) {
	t.Parallel()

	req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
	req.StateRoot = filepath.Join(req.StateRoot, "missing", "state")
	result, err := materializeInStateRoot(DefaultGCPVMContract(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(result.ManifestPath); err != nil {
		t.Fatal(err)
	}
}

func TestMaterializeKeepsOtherAccountsOutOfTheHostCopy(t *testing.T) {
	t.Parallel()

	req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
	targets := filepath.Join(req.StateRoot, "targets")
	materializationRoot := filepath.Join(targets, TargetKind, GCPVMProvider, req.TargetID, "materializations", req.MaterializationID)
	for _, prepare := range []func() error{
		// A first run creates each directory.
		func() error { return nil },
		// An earlier run left the materialization directory open.
		func() error { return os.Chmod(materializationRoot, 0o755) },
	} {
		if err := prepare(); err != nil {
			t.Fatal(err)
		}
		if _, err := materializeInStateRoot(DefaultGCPVMContract(), req); err != nil {
			t.Fatal(err)
		}
		for _, dir := range []string{targets, materializationRoot} {
			if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
				t.Fatalf("%s = %v, %v; want mode 0700", dir, info.Mode().Perm(), err)
			}
		}
	}
}

// TestMaterializeKeepsTheHostCopyThatTheKeptManifestRecords stages a second
// send of the same ID beside the first host copy. A copy that fails part way
// leaves the first host copy, which the kept manifest still describes.
func TestMaterializeKeepsTheHostCopyThatTheKeptManifestRecords(t *testing.T) {
	t.Parallel()

	req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
	first, err := materializeInStateRoot(DefaultGCPVMContract(), req)
	if err != nil {
		t.Fatal(err)
	}
	hostile := req
	hostile.SourceWorkspace = t.TempDir()
	if err := os.WriteFile(filepath.Join(hostile.SourceWorkspace, "a"), []byte("copied first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(hostile.SourceWorkspace, "z"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := materializeInStateRoot(DefaultGCPVMContract(), hostile); err == nil {
		t.Fatal("materializeInStateRoot() accepted a hostile workspace")
	}
	kept := readHostManifest(t, req)
	if !slices.Equal(kept.Entries, first.Manifest.Entries) {
		t.Fatalf("kept manifest entries changed:\n%v\n%v", kept.Entries, first.Manifest.Entries)
	}
	hostEntries, err := copyWorkspaceTree(first.MaterializedWorkspace, filepath.Join(t.TempDir(), "mirror"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(hostEntries, kept.Entries) {
		t.Fatalf("host copy differs from the kept manifest:\n%v\n%v", hostEntries, kept.Entries)
	}
	names, err := os.ReadDir(first.MaterializationRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 {
		t.Fatalf("materialization root holds %v, want only the manifest and the workspace", names)
	}

	// A good second send replaces the host copy.
	second := req
	second.SourceWorkspace = t.TempDir()
	if err := os.WriteFile(filepath.Join(second.SourceWorkspace, "b"), []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := materializeInStateRoot(DefaultGCPVMContract(), second)
	if err != nil {
		t.Fatal(err)
	}
	hostEntries, err = copyWorkspaceTree(result.MaterializedWorkspace, filepath.Join(t.TempDir(), "mirror"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(hostEntries, result.Manifest.Entries) || len(hostEntries) != 1 || hostEntries[0].Path != "b" {
		t.Fatalf("host copy after a good second send = %v, want only b", hostEntries)
	}
	if names, err := os.ReadDir(result.MaterializationRoot); err != nil || len(names) != 2 {
		t.Fatalf("materialization root holds %v (%v), want only the manifest and the workspace", names, err)
	}
	if info, err := os.Stat(result.MaterializedWorkspace); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("host workspace = %v, %v; want mode 0700", info, err)
	}
}

// TestMaterializeReplacesANonDirectoryHostCopy removes a symlink or a file in
// the place of the host workspace without following it.
func TestMaterializeReplacesANonDirectoryHostCopy(t *testing.T) {
	t.Parallel()

	for name, plant := range map[string]func(t *testing.T, workspace, outside string){
		"symlink": func(t *testing.T, workspace, outside string) { mustSymlink(t, outside, workspace) },
		"file": func(t *testing.T, workspace, _ string) {
			if err := os.WriteFile(workspace, []byte("not a copy"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
			first, err := materializeInStateRoot(DefaultGCPVMContract(), req)
			if err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(first.MaterializedWorkspace); err != nil {
				t.Fatal(err)
			}
			plant(t, first.MaterializedWorkspace, outside)
			result, err := materializeInStateRoot(DefaultGCPVMContract(), req)
			if err != nil {
				t.Fatal(err)
			}
			if info, err := os.Lstat(result.MaterializedWorkspace); err != nil || !info.IsDir() {
				t.Fatalf("host workspace = %v, %v; want a directory", info, err)
			}
			if names, err := os.ReadDir(outside); err != nil || len(names) != 1 {
				t.Fatalf("symlink target holds %v (%v), want only keep", names, err)
			}
			if names, err := os.ReadDir(result.MaterializationRoot); err != nil || len(names) != 2 {
				t.Fatalf("materialization root holds %v (%v), want only the manifest and the workspace", names, err)
			}
		})
	}
}

// assertHostCopyMatchesKeptManifest fails unless the host workspace of req
// holds exactly the entries of want and the kept manifest records them.
func assertHostCopyMatchesKeptManifest(t *testing.T, req MaterializeRequest, want []WorkspaceEntry) {
	t.Helper()
	kept := readHostManifest(t, req)
	if !slices.Equal(kept.Entries, want) {
		t.Fatalf("kept manifest entries changed:\n%v\n%v", kept.Entries, want)
	}
	materializationRoot := filepath.Dir(hostManifestPath(req))
	hostEntries, err := copyWorkspaceTree(filepath.Join(materializationRoot, DefaultGCPVMContract().WorkspaceMaterialization.WorkspaceDir), filepath.Join(t.TempDir(), "mirror"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(hostEntries, kept.Entries) {
		t.Fatalf("host copy differs from the kept manifest:\n%v\n%v", hostEntries, kept.Entries)
	}
}

// secondSource has the paths of the test source workspace, and one file
// with other content.
func secondSource(t *testing.T) string {
	t.Helper()
	source := filepath.Join(t.TempDir(), "second")
	if _, err := copyWorkspaceTree(filepath.Join("testdata", "source-workspace"), source, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	return source
}

// TestBrokeredTargetKeepsTheHostCopyWhenTheArchiveFails fails the archive of
// a second send. The host copy and the kept manifest must still agree.
// Not parallel: it replaces createArchiveFile.
func TestBrokeredTargetKeepsTheHostCopyWhenTheArchiveFails(t *testing.T) {
	guestRoot := t.TempDir()
	var started bool
	req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
	first, err := fakeBrokerTarget(t, "receive", guestRoot, &started).MaterializeWorkspace(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	createArchiveFile = func(string, string) (*os.File, error) { return nil, unix.ENOSPC }
	t.Cleanup(func() { createArchiveFile = os.CreateTemp })
	second := req
	second.SourceWorkspace = secondSource(t)
	if _, err := fakeBrokerTarget(t, "receive", guestRoot, &started).MaterializeWorkspace(context.Background(), second); !errors.Is(err, unix.ENOSPC) {
		t.Fatalf("MaterializeWorkspace() = %v, want ENOSPC", err)
	}
	assertHostCopyMatchesKeptManifest(t, req, first.Manifest.Entries)
}

type killedRun struct{}

func materializeUntilKilled(t *testing.T, req MaterializeRequest) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != (killedRun{}) {
			t.Fatalf("the send was not killed: %v", recovered)
		}
	}()
	_, _ = materializeInStateRoot(DefaultGCPVMContract(), req)
}

func failHostileSend(t *testing.T, req MaterializeRequest) {
	t.Helper()
	hostile := req
	hostile.SourceWorkspace = t.TempDir()
	if err := unix.Mkfifo(filepath.Join(hostile.SourceWorkspace, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := materializeInStateRoot(DefaultGCPVMContract(), hostile); err == nil {
		t.Fatal("materializeInStateRoot() accepted a hostile workspace")
	}
}

// TestMaterializeRefusesToRestoreAPlantedPrevious plants a symlink under a
// previous name while the workspace is missing. A walk of the symlink finds
// no entries, as the empty kept manifest records, but recovery must not make
// it the host workspace, and must not touch its target.
func TestMaterializeRefusesToRestoreAPlantedPrevious(t *testing.T) {
	t.Parallel()

	req := brokeredRequest(t, t.TempDir())
	first, err := materializeInStateRoot(DefaultGCPVMContract(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(first.MaterializedWorkspace); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, outside, filepath.Join(first.MaterializationRoot, "."+DefaultGCPVMContract().WorkspaceMaterialization.WorkspaceDir+"-previous-0011"))
	failHostileSend(t, req)
	if info, err := os.Lstat(first.MaterializedWorkspace); err == nil {
		t.Fatalf("recovery made %v the host workspace", info.Mode())
	}
	if names, err := os.ReadDir(outside); err != nil || len(names) != 1 {
		t.Fatalf("symlink target holds %v (%v), want only keep", names, err)
	}
}

// TestMaterializeRecoversAnInterruptionAtEveryStepOfTheSwap stops a second
// send before each step of its swap, by a kill or by a failed step. A later
// send that fails must find the host copy that the kept manifest describes,
// and a later good send must leave only its own copy and manifest. A failure
// before the cleanup comes after the manifest write, as when the write puts
// the manifest in place and then fails.
// Not parallel: it replaces swapStep.
func TestMaterializeRecoversAnInterruptionAtEveryStepOfTheSwap(t *testing.T) {
	noStep := swapStep
	t.Cleanup(func() { swapStep = noStep })
	for _, earlier := range []string{"another", "the same", "no"} {
		for _, step := range []string{"first rename", "second rename", "manifest", "cleanup", "done"} {
			for _, kill := range []bool{true, false} {
				committed := step == "cleanup" || step == "done"
				if step == "done" && !kill {
					continue
				}
				t.Run(fmt.Sprintf("%s copy, kill=%v before %s", earlier, kill, step), func(t *testing.T) {
					req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
					first, err := materializeInStateRoot(DefaultGCPVMContract(), req)
					if err != nil {
						t.Fatal(err)
					}
					want := first.Manifest.Entries
					second := req
					switch earlier {
					case "another":
						second.SourceWorkspace = secondSource(t)
					case "no":
						for _, path := range []string{first.MaterializedWorkspace, first.ManifestPath} {
							if err := os.RemoveAll(path); err != nil {
								t.Fatal(err)
							}
						}
						want = nil
					}
					swapStep = func(at string) error {
						if at == step && kill {
							panic(killedRun{})
						} else if at == step {
							return unix.ENOSPC
						}
						return nil
					}
					if kill {
						materializeUntilKilled(t, second)
					} else if _, err := materializeInStateRoot(DefaultGCPVMContract(), second); !errors.Is(err, unix.ENOSPC) {
						t.Fatalf("materializeInStateRoot() = %v, want ENOSPC", err)
					}
					swapStep = noStep
					if !kill && !committed && want != nil {
						assertHostCopyMatchesKeptManifest(t, req, want)
					}
					failHostileSend(t, req)
					if committed {
						if want, err = copyWorkspaceTree(second.SourceWorkspace, filepath.Join(t.TempDir(), "mirror"), nil); err != nil {
							t.Fatal(err)
						}
					}
					if want != nil {
						assertHostCopyMatchesKeptManifest(t, req, want)
					} else if _, err := os.Lstat(first.ManifestPath); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("manifest after an uncommitted first copy: %v, want none", err)
					}
					result, err := materializeInStateRoot(DefaultGCPVMContract(), req)
					if err != nil {
						t.Fatal(err)
					}
					assertHostCopyMatchesKeptManifest(t, req, result.Manifest.Entries)
					if names, err := os.ReadDir(result.MaterializationRoot); err != nil || len(names) != 2 {
						t.Fatalf("materialization root holds %v (%v), want only the manifest and the workspace", names, err)
					}
				})
			}
		}
	}
}
