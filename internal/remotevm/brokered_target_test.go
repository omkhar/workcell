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
	case "receive":
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
