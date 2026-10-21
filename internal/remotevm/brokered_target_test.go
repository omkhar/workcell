// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

import (
	"context"
	"errors"
	"flag"
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
	manifest := filepath.Join(req.StateRoot, "targets", TargetKind, GCPVMProvider, req.TargetID, "materializations", "m1", MaterializationFile)
	if _, err := os.Stat(manifest); !os.IsNotExist(err) {
		t.Fatalf("a failed transfer left the host manifest: %v", err)
	}
}

func TestBrokeredTargetKillsABrokerThatIgnoresTheTimeout(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var started bool
	begin := time.Now()
	_, err := fakeBrokerTarget(t, "hang", t.TempDir(), &started).MaterializeWorkspace(ctx, brokeredRequest(t, filepath.Join("testdata", "source-workspace")))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("MaterializeWorkspace() error = %v, want a timeout", err)
	}
	if elapsed := time.Since(begin); elapsed > brokerStopGrace+10*time.Second {
		t.Fatalf("SIGKILL escalation took %v", elapsed)
	}
}

func TestBrokeredTargetConstructorsPinTheBrokerArgv(t *testing.T) {
	t.Parallel()

	gcp, err := NewGCPVMBrokeredTarget("/usr/bin/gcloud", "proj", "us-central1-a", 7001)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := gcp.Command("vm-1"), []string{"/usr/bin/gcloud", "compute", "start-iap-tunnel", "vm-1", "7001", "--listen-on-stdin", "--zone=us-central1-a", "--project=proj", "--quiet"}; !slices.Equal(got, want) {
		t.Fatalf("gcp argv = %q", got)
	}
	aws, err := NewAWSEC2SSMBrokeredTarget("/usr/local/bin/aws", "us-east-1", 7001)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := aws.Command("i-0123"), []string{"/usr/local/bin/aws", "ssm", "start-session", "--target", "i-0123", "--document-name", "AWS-StartSSHSession", "--parameters", "portNumber=7001", "--region", "us-east-1"}; !slices.Equal(got, want) {
		t.Fatalf("aws argv = %q", got)
	}
	for name, err := range map[string]error{
		"relative cli": func() error { _, err := NewGCPVMBrokeredTarget("gcloud", "p", "z", 1); return err }(),
		"port zero":    func() error { _, err := NewGCPVMBrokeredTarget("/g", "p", "z", 0); return err }(),
		"port high":    func() error { _, err := NewAWSEC2SSMBrokeredTarget("/a", "r", 65536); return err }(),
		"empty zone":   func() error { _, err := NewGCPVMBrokeredTarget("/g", "p", " ", 1); return err }(),
		"option value": func() error { _, err := NewAWSEC2SSMBrokeredTarget("/a", "--profile=x", 1); return err }(),
	} {
		if err == nil {
			t.Fatalf("%s: constructor accepted invalid input", name)
		}
	}
}

func TestBrokeredTargetLeavesBootstrapAndSessionsToLaterItems(t *testing.T) {
	t.Parallel()

	var target BrokeredTarget
	if _, err := target.BootstrapTarget(context.Background(), BootstrapRequest{}); !errors.Is(err, errBootstrapNotImplemented) {
		t.Fatalf("BootstrapTarget() error = %v", err)
	}
	if _, err := target.StartSession(context.Background(), StartSessionRequest{}); !errors.Is(err, errSessionNotImplemented) {
		t.Fatalf("StartSession() error = %v", err)
	}
	if _, err := target.FinishSession(context.Background(), FinishSessionRequest{}); !errors.Is(err, errSessionNotImplemented) {
		t.Fatalf("FinishSession() error = %v", err)
	}
}

func TestBrokeredTargetTimesOutWhenAGrandchildHoldsThePipe(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var started bool
	begin := time.Now()
	_, err := fakeBrokerTarget(t, "orphan", t.TempDir(), &started).MaterializeWorkspace(ctx, brokeredRequest(t, filepath.Join("testdata", "source-workspace")))
	if err == nil {
		t.Fatal("MaterializeWorkspace() succeeded without a receiver")
	}
	if elapsed := time.Since(begin); elapsed > brokerStopGrace+5*time.Second {
		t.Fatalf("a held pipe blocked the transfer for %v", elapsed)
	}
}

func TestBrokeredTargetReportsTheStderrOfABrokerThatExitsZero(t *testing.T) {
	t.Parallel()

	var started bool
	_, err := fakeBrokerTarget(t, "exit0", t.TempDir(), &started).MaterializeWorkspace(context.Background(), brokeredRequest(t, filepath.Join("testdata", "source-workspace")))
	if err == nil || !strings.Contains(err.Error(), "Failed to lookup instance") {
		t.Fatalf("MaterializeWorkspace() error = %v, want the broker stderr", err)
	}
}
