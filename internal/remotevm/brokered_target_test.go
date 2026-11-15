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
	"runtime/debug"
	"slices"
	"strings"
	"sync/atomic"
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
	case "longack": // the longest path that one ack line can carry
		digest, size := "", int64(0)
		fmt.Fscanln(os.Stdin, new(string), new(string), &digest, &size)
		io.CopyN(io.Discard, os.Stdin, size)
		fmt.Printf("ok %s /%s\n", digest, strings.Repeat("\x01", archiveLineBytes-len("ok  /\n")-len(digest)))
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
// unconfirmed manifest records it and before the host copy is replaced.
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
	// A second send of a confirmed ID must also keep the host copy and its
	// confirmed manifest.
	first, err := fakeBrokerTarget(t, "receive", t.TempDir(), new(bool)).MaterializeWorkspace(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	second := req
	second.SourceWorkspace = secondSource(t)
	for _, program := range []string{"/nonexistent/gcloud", notExecutable, "ended context"} {
		target := BrokeredTarget{Contract: DefaultGCPVMContract(), Command: func(string) []string { return []string{program, "compute"} }}
		sendCtx := t.Context()
		if program == "ended context" {
			target, sendCtx = fakeBrokerTarget(t, "receive", t.TempDir(), new(bool)), ctx
		}
		if _, err := target.MaterializeWorkspace(sendCtx, second); err == nil {
			t.Fatalf("%q: a second send that cannot start succeeded", program)
		}
		assertHostCopyMatchesKeptManifest(t, req, first.Manifest.Entries)
		if kept := readHostManifest(t, req); kept.GuestTransfer != "" || kept.ArchiveSHA256 != first.Manifest.ArchiveSHA256 {
			t.Fatalf("%q: kept manifest = guest_transfer %q, archive_sha256 %q; want the confirmed first send", program, kept.GuestTransfer, kept.ArchiveSHA256)
		}
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
	if _, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req); err == nil {
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
	result, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req)
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
		if _, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req); err != nil {
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
	first, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req)
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
	if _, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), hostile); err == nil {
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
	result, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), second)
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
			first, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req)
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
			result, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req)
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

// testSource copies the test source workspace and adds a symlink. A change
// of "content", "mode" or "link target" changes one entry, and no other part
// of it.
func testSource(t *testing.T, change string) string {
	t.Helper()
	source := filepath.Join(t.TempDir(), "source")
	if _, err := copyWorkspaceTree(filepath.Join("testdata", "source-workspace"), source, nil); err != nil {
		t.Fatal(err)
	}
	target, readme := "README.md", filepath.Join(source, "README.md")
	// Modes that a usual umask changes.
	err := errors.Join(os.Chmod(filepath.Join(source, "subdir"), 0o777), os.Chmod(filepath.Join(source, "subdir", "config.txt"), 0o666))
	switch change {
	case "content":
		err = errors.Join(err, os.WriteFile(readme, []byte("second"), 0o644))
	case "mode":
		err = errors.Join(err, os.Chmod(readme, 0o600))
	case "link target":
		target = "subdir/config.txt"
	}
	if err == nil {
		err = os.Symlink(target, filepath.Join(source, "link"))
	}
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func secondSource(t *testing.T) string { return testSource(t, "content") }

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
	_, _ = materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req)
}

func failHostileSend(t *testing.T, req MaterializeRequest) {
	t.Helper()
	hostile := req
	hostile.SourceWorkspace = t.TempDir()
	if err := unix.Mkfifo(filepath.Join(hostile.SourceWorkspace, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), hostile); err == nil {
		t.Fatal("materializeInStateRoot() accepted a hostile workspace")
	}
}

// TestMaterializeRefusesToRestoreAPlantedPrevious plants a symlink under a
// previous name while the workspace is missing. A walk of the symlink finds
// no entries, as the empty kept manifest records, but recovery must not make
// it the host workspace, and must not touch its target.
// The same holds for an empty directory that another account owns.
func TestMaterializeRefusesToRestoreAPlantedPrevious(t *testing.T) {
	t.Parallel()

	for _, foreign := range []bool{false, true} {
		t.Run(fmt.Sprintf("foreign directory=%v", foreign), func(t *testing.T) {
			t.Parallel()
			if foreign && os.Geteuid() != 0 {
				t.Skip("needs root to give a directory to another account")
			}
			req := brokeredRequest(t, t.TempDir())
			first, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req)
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
			planted := filepath.Join(first.MaterializationRoot, "."+DefaultGCPVMContract().WorkspaceMaterialization.WorkspaceDir+"-previous-0011")
			if foreign {
				mustMkdir(t, planted)
				err = os.Lchown(planted, 1, 1)
			} else {
				err = os.Symlink(outside, planted)
			}
			if err != nil {
				t.Fatal(err)
			}
			failHostileSend(t, req)
			if info, err := os.Lstat(first.MaterializedWorkspace); err == nil {
				t.Fatalf("recovery made %v the host workspace", info.Mode())
			}
			if names, err := os.ReadDir(outside); err != nil || len(names) != 1 {
				t.Fatalf("symlink target holds %v (%v), want only keep", names, err)
			}
		})
	}
}

// TestMaterializeRefusesAKeptManifestThatItCannotRead plants an empty
// leftover copy and a kept manifest that recovery cannot use. The send must
// fail and move nothing.
func TestMaterializeRefusesAKeptManifestThatItCannotRead(t *testing.T) {
	t.Parallel()

	for name, content := range map[string]string{"not json": "{", "no entries": "{}\n", "version 1, no entries": `{"version":1}`, "version 2": `{"version":2,"entries":[]}`} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
			first, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req)
			if err != nil {
				t.Fatal(err)
			}
			leftover := filepath.Join(first.MaterializationRoot, "."+DefaultGCPVMContract().WorkspaceMaterialization.WorkspaceDir+"-staging-0011")
			mustMkdir(t, leftover)
			if err := os.WriteFile(first.ManifestPath, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req); err == nil {
				t.Fatal("materializeInStateRoot() accepted a kept manifest that it cannot read")
			}
			if entries, err := copyWorkspaceTree(first.MaterializedWorkspace, "", nil); err != nil || !slices.Equal(entries, first.Manifest.Entries) {
				t.Fatalf("host workspace = %v (%v), want the first copy", entries, err)
			}
			if _, err := os.Lstat(leftover); err != nil {
				t.Fatalf("leftover copy: %v", err)
			}
		})
	}
}

// TestMaterializeWaitsForASendOfTheSameID pauses a send while it holds the
// lock of its ID. A second send to the fake or the brokered target must wait
// for that lock until its context ends, and a send that waits with no
// deadline must go on once the first send ends.
// Not parallel: it replaces swapStep.
func TestMaterializeWaitsForASendOfTheSameID(t *testing.T) {
	noStep := swapStep
	t.Cleanup(func() { swapStep = noStep })
	req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
	paused, resume, steps := make(chan struct{}), make(chan struct{}), atomic.Int32{}
	swapStep = func(string, *os.File) error {
		if steps.Add(1) == 1 {
			close(paused)
			<-resume
		}
		return nil
	}
	sends := make(chan error, 4)
	go func() {
		_, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req)
		sends <- err
	}()
	<-paused
	defer close(resume)
	var started bool
	for _, target := range []ConformanceTarget{FakeTarget{Contract: DefaultGCPVMContract()}, fakeBrokerTarget(t, "receive", t.TempDir(), &started)} {
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()
		go func() {
			_, err := target.MaterializeWorkspace(ctx, req)
			sends <- err
		}()
		select {
		case err := <-sends:
			if !errors.Is(err, context.DeadlineExceeded) || started {
				t.Fatalf("MaterializeWorkspace() = %v, broker started = %v while another send held the lock; want the deadline", err, started)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("MaterializeWorkspace() still waits 5s after its 200ms deadline")
		}
	}
	go func() {
		_, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req)
		sends <- err
	}()
	resume <- struct{}{}
	for range 2 {
		if err := <-sends; err != nil {
			t.Fatal(err)
		}
	}
	assertHostCopyMatchesKeptManifest(t, req, readHostManifest(t, req).Entries)
}

// TestMaterializeRefusesAManifestOverTheReadLimit lowers the manifest limit
// to the size of the kept manifest, which recovery must still read. A send
// whose manifest is larger must fail before it moves a copy.
// Not parallel: it replaces maxManifestBytes.
func TestMaterializeRefusesAManifestOverTheReadLimit(t *testing.T) {
	limit := maxManifestBytes
	t.Cleanup(func() { maxManifestBytes = limit })
	req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
	first, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(first.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	maxManifestBytes = info.Size()
	leftover := filepath.Join(first.MaterializationRoot, "."+DefaultGCPVMContract().WorkspaceMaterialization.WorkspaceDir+"-staging-0011")
	mustMkdir(t, leftover)
	second := req
	second.SourceWorkspace = secondSource(t)
	if _, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), second); err == nil || !strings.Contains(err.Error(), "exceeds the limit") {
		t.Fatalf("materializeInStateRoot() = %v, want the manifest limit", err)
	}
	assertHostCopyMatchesKeptManifest(t, req, first.Manifest.Entries)
	if _, err := os.Lstat(leftover); err != nil {
		t.Fatalf("leftover copy: %v", err)
	}
	// A manifest of exactly the limit is written.
	if _, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req); err != nil {
		t.Fatal(err)
	}
}

// TestMaterializeRecoversAnInterruptionAtEveryStepOfTheSwap stops a second
// send by a kill or by a failure before each step of its swap, and before
// and after the manifest write, for each kind of change. A failed send must
// leave a host copy that the kept manifest describes. A kill may leave them
// apart, but a later send that fails must put them together, and must keep
// every copy that the kill left. A later good send must leave only its own
// copy and manifest.
// Not parallel: it replaces swapStep and writeManifestAt.
func TestMaterializeRecoversAnInterruptionAtEveryStepOfTheSwap(t *testing.T) {
	noStep, write := swapStep, writeManifestAt
	t.Cleanup(func() { swapStep, writeManifestAt = noStep, write })
	for _, change := range []string{"content", "mode", "link target", "no", "first copy"} {
		for _, step := range []string{"first rename", "second rename", "manifest", "written", "done"} {
			for _, kill := range []bool{true, false} {
				if step == "done" && !kill {
					continue
				}
				t.Run(fmt.Sprintf("%s change, kill=%v before %s", change, kill, step), func(t *testing.T) {
					req := brokeredRequest(t, testSource(t, ""))
					first, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req)
					if err != nil {
						t.Fatal(err)
					}
					want, second := first.Manifest.Entries, req
					switch change {
					case "first copy":
						for _, path := range []string{first.MaterializedWorkspace, first.ManifestPath} {
							if err := os.RemoveAll(path); err != nil {
								t.Fatal(err)
							}
						}
						want = nil
					case "no":
					default:
						second.SourceWorkspace = testSource(t, change)
					}
					if step == "written" || step == "done" {
						if want, err = copyWorkspaceTree(second.SourceWorkspace, "", nil); err != nil {
							t.Fatal(err)
						}
					}
					inject := func(at string, parent *os.File) error {
						if at == step && kill {
							parent.Close() // a kill ends the lock
							panic(killedRun{})
						} else if at == step {
							return unix.ENOSPC
						}
						return nil
					}
					swapStep = inject
					writeManifestAt = func(parent *os.File, name string, data []byte, mode os.FileMode, prefix string) error {
						err := inject("manifest", parent)
						if err == nil {
							err = write(parent, name, data, mode, prefix)
						}
						return errors.Join(err, inject("written", parent))
					}
					assertKept := func() {
						t.Helper()
						if want != nil {
							assertHostCopyMatchesKeptManifest(t, req, want)
						} else if _, err := os.Lstat(first.ManifestPath); !errors.Is(err, os.ErrNotExist) {
							t.Fatalf("manifest after an uncommitted first copy: %v, want none", err)
						}
					}
					if kill {
						materializeUntilKilled(t, second)
					} else if _, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), second); !errors.Is(err, unix.ENOSPC) {
						t.Fatalf("materializeInStateRoot() = %v, want ENOSPC", err)
					} else if assertKept(); want == nil {
						// No manifest describes the new copy, so it is not left in place.
						if _, err := os.Lstat(first.MaterializedWorkspace); !errors.Is(err, os.ErrNotExist) {
							t.Fatalf("host workspace after an uncommitted first copy: %v, want none", err)
						}
					}
					swapStep, writeManifestAt = noStep, write
					left := hostCopies(t, first.MaterializationRoot)
					failHostileSend(t, req)
					assertKept()
					assertCopiesKept(t, first.MaterializationRoot, left)
					// A send that fails part way after recovery keeps them too.
					left = hostCopies(t, first.MaterializationRoot)
					swapStep = func(at string, _ *os.File) error {
						if at == "second rename" {
							return unix.ENOSPC
						}
						return nil
					}
					if _, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), second); !errors.Is(err, unix.ENOSPC) {
						t.Fatalf("materializeInStateRoot() = %v, want ENOSPC", err)
					}
					swapStep = noStep
					assertKept()
					assertCopiesKept(t, first.MaterializationRoot, left)
					result, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req)
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

func assertCopiesKept(t *testing.T, root string, before map[string]bool) {
	t.Helper()
	after := hostCopies(t, root)
	for copied := range before {
		if !after[copied] {
			t.Fatalf("lost the copy %s", copied)
		}
	}
}

// hostCopies returns the entries of each directory in root, as text.
func hostCopies(t *testing.T, root string) map[string]bool {
	t.Helper()
	names, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	copies := map[string]bool{}
	for _, name := range names {
		if name.IsDir() {
			entries, err := copyWorkspaceTree(filepath.Join(root, name.Name()), "", nil)
			if err != nil {
				t.Fatal(err)
			}
			copies[fmt.Sprint(entries)] = true
		}
	}
	return copies
}

// TestMaterializeKeepsTheNewCopyWhenItCannotReadBackAFailedManifest fails a
// manifest write after its rename and leaves a manifest that the read back
// refuses. The commit cannot tell which manifest is kept, so it must keep the
// new copy in place, which the landed manifest describes.
// Not parallel: it replaces writeManifestAt.
func TestMaterializeKeepsTheNewCopyWhenItCannotReadBackAFailedManifest(t *testing.T) {
	write := writeManifestAt
	t.Cleanup(func() { writeManifestAt = write })
	req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
	first, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req)
	if err != nil {
		t.Fatal(err)
	}
	moved := first.ManifestPath + ".moved"
	writeManifestAt = func(parent *os.File, name string, data []byte, mode os.FileMode, prefix string) error {
		err := write(parent, name, data, mode, prefix)
		if err == nil {
			err = errors.Join(os.Rename(first.ManifestPath, moved), os.Symlink(filepath.Base(moved), first.ManifestPath), unix.EIO)
		}
		return err
	}
	second := req
	second.SourceWorkspace = secondSource(t)
	if _, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), second); !errors.Is(err, unix.EIO) {
		t.Fatalf("materializeInStateRoot() = %v, want EIO", err)
	}
	if err := os.Rename(moved, first.ManifestPath); err != nil {
		t.Fatal(err)
	}
	want, err := copyWorkspaceTree(second.SourceWorkspace, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	assertHostCopyMatchesKeptManifest(t, req, want)
}

// TestMaterializeRemovesWhatRecoveryMovesAside kills a send after it put its
// copy in place and before its manifest write, and plants the temporary
// files of a killed archive and manifest write. The next good send puts back
// the copy that the kept manifest describes and moves the killed copy aside.
// It must leave only its own copy and manifest.
// Not parallel: it replaces writeManifestAt.
func TestMaterializeRemovesWhatRecoveryMovesAside(t *testing.T) {
	write := writeManifestAt
	t.Cleanup(func() { writeManifestAt = write })
	req := brokeredRequest(t, testSource(t, ""))
	first, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req)
	if err != nil {
		t.Fatal(err)
	}
	writeManifestAt = func(parent *os.File, _ string, _ []byte, _ os.FileMode, _ string) error {
		parent.Close()
		panic(killedRun{})
	}
	second := req
	second.SourceWorkspace = secondSource(t)
	materializeUntilKilled(t, second)
	writeManifestAt = write
	for _, name := range []string{archivePrefix + "0011", ".manifest-0011"} {
		if err := os.WriteFile(filepath.Join(first.MaterializationRoot, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req); err != nil {
		t.Fatal(err)
	}
	if names, err := os.ReadDir(first.MaterializationRoot); err != nil || len(names) != 2 {
		t.Fatalf("materialization root holds %v (%v), want only the manifest and the workspace", names, err)
	}
}

// TestBrokeredTargetRefusesASendWhoseConfirmedManifestCannotFit lowers the
// manifest limit to the size of the unconfirmed manifest. The guest acks a
// longer path, so the send must fail before the broker starts, and not after
// the guest holds a copy that no manifest can confirm.
// Not parallel: it replaces maxManifestBytes.
func TestBrokeredTargetRefusesASendWhoseConfirmedManifestCannotFit(t *testing.T) {
	limit := maxManifestBytes
	t.Cleanup(func() { maxManifestBytes = limit })
	req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
	result, staged, err := stageInStateRoot(t.Context(), DefaultGCPVMContract(), req)
	if err != nil {
		t.Fatal(err)
	}
	result.Manifest.ArchiveSHA256, result.Manifest.GuestTransfer = strings.Repeat("0", 64), GuestTransferUnconfirmed
	content, err := manifestContent(result.Manifest)
	if err = errors.Join(err, staged.close()); err != nil {
		t.Fatal(err)
	}
	maxManifestBytes = int64(len(content))
	guestRoot := filepath.Join(t.TempDir(), strings.Repeat("g", 200))
	mustMkdir(t, guestRoot)
	var started bool
	if _, err := fakeBrokerTarget(t, "receive", guestRoot, &started).MaterializeWorkspace(t.Context(), req); err == nil || started {
		t.Fatalf("MaterializeWorkspace() = %v, broker started = %v; want the manifest limit before the broker", err, started)
	}
}

// TestBrokeredTargetEndsTheLockWhenTheSendPanics panics in the broker
// command while the lock is held. A later send of the same ID in this process
// must not wait for the lock of the panicked send. A collection could close
// a leaked descriptor and end its lock, so the test turns collection off.
// Not parallel: it changes the collector setting.
func TestBrokeredTargetEndsTheLockWhenTheSendPanics(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("the send did not panic")
			}
		}()
		target := BrokeredTarget{Contract: DefaultGCPVMContract(), Command: func(string) []string { panic("broker lookup") }}
		_, _ = target.MaterializeWorkspace(t.Context(), req)
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	var started bool
	if _, err := fakeBrokerTarget(t, "receive", t.TempDir(), &started).MaterializeWorkspace(ctx, req); err != nil {
		t.Fatal(err)
	}
}

// TestMaterializeRefusesAnEndedContextBeforeTheSwap ends the context before a
// free lock is taken, or after the staging. The host copy and the kept
// manifest must stay, and the broker must not start.
// Not parallel: it replaces createArchiveFile.
func TestMaterializeRefusesAnEndedContextBeforeTheSwap(t *testing.T) {
	t.Cleanup(func() { createArchiveFile = os.CreateTemp })
	req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
	first, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req)
	if err != nil {
		t.Fatal(err)
	}
	second := req
	second.SourceWorkspace = secondSource(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := materializeInStateRoot(ctx, DefaultGCPVMContract(), second); !errors.Is(err, context.Canceled) {
		t.Fatalf("materializeInStateRoot() = %v, want the ended context", err)
	}
	assertHostCopyMatchesKeptManifest(t, req, first.Manifest.Entries)
	for _, late := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		if !late {
			cancel()
		}
		createArchiveFile = func(dir, pattern string) (*os.File, error) { cancel(); return os.CreateTemp(dir, pattern) }
		// A late end comes after the broker argv is resolved, which precedes
		// the context check.
		var started bool
		if _, err := fakeBrokerTarget(t, "receive", t.TempDir(), &started).MaterializeWorkspace(ctx, second); !errors.Is(err, context.Canceled) || started != late {
			t.Fatalf("late %v: MaterializeWorkspace() = %v, broker argv resolved = %v", late, err, started)
		}
		assertHostCopyMatchesKeptManifest(t, req, first.Manifest.Entries)
	}
}

// TestMaterializeKeepsAKeptCopyThatRecoveryCannotScan: a killed send leaves the
// kept copy aside; a scan error then a failed write must keep it. Not parallel.
func TestMaterializeKeepsAKeptCopyThatRecoveryCannotScan(t *testing.T) {
	write, limits := writeManifestAt, defaultWorkspaceLimits
	t.Cleanup(func() { writeManifestAt, defaultWorkspaceLimits = write, limits })
	req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
	first, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req)
	if err != nil {
		t.Fatal(err)
	}
	writeManifestAt = func(parent *os.File, _ string, _ []byte, _ os.FileMode, _ string) error {
		parent.Close()
		panic(killedRun{})
	}
	req.SourceWorkspace = t.TempDir()
	materializeUntilKilled(t, req)
	writeManifestAt, defaultWorkspaceLimits.maxEntries = func(*os.File, string, []byte, os.FileMode, string) error { return unix.ENOSPC }, len(first.Manifest.Entries)-1
	if _, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req); !errors.Is(err, unix.ENOSPC) {
		t.Fatalf("materializeInStateRoot() = %v, want ENOSPC", err)
	}
	writeManifestAt, defaultWorkspaceLimits = write, limits
	kept := hostCopies(t, first.MaterializationRoot)[fmt.Sprint(readHostManifest(t, req).Entries)]
	if _, err := materializeInStateRoot(t.Context(), DefaultGCPVMContract(), req); err != nil || !kept {
		t.Fatalf("a host copy matches the kept manifest: %v; the next send: %v", kept, err)
	}
}

// TestBrokeredTargetConfirmsTheLongestAckAtTheManifestLimit: an empty broker
// command of a second send must fail, keep the host copy and its manifest, and
// end the lock; at the limit of the size check the longest ack is confirmed,
// one byte less stops the send before the broker. Not parallel.
func TestBrokeredTargetConfirmsTheLongestAckAtTheManifestLimit(t *testing.T) {
	defer func(limit int64) { maxManifestBytes = limit }(maxManifestBytes)
	req := brokeredRequest(t, filepath.Join("testdata", "source-workspace"))
	first, err := fakeBrokerTarget(t, "receive", t.TempDir(), new(bool)).MaterializeWorkspace(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	second := req
	second.SourceWorkspace = secondSource(t)
	empty := BrokeredTarget{Contract: DefaultGCPVMContract(), Command: func(string) []string { return nil }}
	if _, err := empty.MaterializeWorkspace(t.Context(), second); err == nil || !strings.Contains(err.Error(), "broker command is empty") {
		t.Fatalf("MaterializeWorkspace() = %v, want the empty broker command", err)
	}
	assertHostCopyMatchesKeptManifest(t, req, first.Manifest.Entries)
	if kept := readHostManifest(t, req); kept.GuestTransfer != "" || kept.ArchiveSHA256 != first.Manifest.ArchiveSHA256 {
		t.Fatalf("kept manifest = guest_transfer %q, archive_sha256 %q; want the confirmed first send", kept.GuestTransfer, kept.ArchiveSHA256)
	}
	// The sends below wait for the lock, so they also show that it ended.
	manifest := first.Manifest
	manifest.MaterializedWorkspace = strings.Repeat("\x00", archiveLineBytes)
	content, _ := manifestContent(manifest)
	for _, size := range []int{len(content) - 1, len(content)} {
		maxManifestBytes = int64(size)
		var started bool
		_, err := fakeBrokerTarget(t, "longack", t.TempDir(), &started).MaterializeWorkspace(t.Context(), req)
		if (err == nil) != (size == len(content)) || started != (err == nil) {
			t.Fatalf("limit %d: MaterializeWorkspace() = %v, broker started = %v", size, err, started)
		}
	}
}
