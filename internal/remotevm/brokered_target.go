// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// ponytail: one fixed bound for a 2 GiB workspace over a slow tunnel; make
	// it a target field when an operator needs a different one.
	brokerTransferTimeout = 30 * time.Minute
	// brokerStopGrace is how long a broker may take to exit on its own, and
	// then how long it may take after SIGTERM before SIGKILL.
	brokerStopGrace = 5 * time.Second
)

var (
	errBootstrapNotImplemented = errors.New("remote_vm bootstrap over the broker is not implemented yet (roadmap L4-2)")
	errSessionNotImplemented   = errors.New("remote_vm session control over the broker is not implemented yet (roadmap L4-3)")
)

// BrokeredTarget materializes the workspace on a live guest. It copies the
// workspace into the host state root as FakeTarget does, then streams a
// deterministic tar of that reviewed copy through the provider broker to the
// guest receiver port. The broker CLI connects its stdin and stdout to that
// port, so no local listener and no inbound public SSH are needed.
type BrokeredTarget struct {
	Contract Contract
	// Command returns the pinned broker argv for targetID. It runs without a
	// shell.
	Command func(targetID string) []string
}

// NewGCPVMBrokeredTarget streams through an IAP TCP tunnel that
// `gcloud compute start-iap-tunnel --listen-on-stdin` opens.
func NewGCPVMBrokeredTarget(gcloudPath, project, zone string, receiverPort int) (BrokeredTarget, error) {
	if err := validateBrokerArgs(gcloudPath, receiverPort, project, zone); err != nil {
		return BrokeredTarget{}, err
	}
	return BrokeredTarget{Contract: DefaultGCPVMContract(), Command: func(targetID string) []string {
		return []string{gcloudPath, "compute", "start-iap-tunnel", targetID, strconv.Itoa(receiverPort), "--listen-on-stdin", "--zone=" + zone, "--project=" + project, "--quiet"}
	}}, nil
}

// NewAWSEC2SSMBrokeredTarget streams through an SSM session that the
// AWS-StartSSHSession document connects to the receiver port on stdio.
func NewAWSEC2SSMBrokeredTarget(awsPath, region string, receiverPort int) (BrokeredTarget, error) {
	if err := validateBrokerArgs(awsPath, receiverPort, region); err != nil {
		return BrokeredTarget{}, err
	}
	return BrokeredTarget{Contract: DefaultAWSEC2SSMContract(), Command: func(targetID string) []string {
		return []string{awsPath, "ssm", "start-session", "--target", targetID, "--document-name", "AWS-StartSSHSession", "--parameters", "portNumber=" + strconv.Itoa(receiverPort), "--region", region}
	}}, nil
}

func validateBrokerArgs(cliPath string, receiverPort int, values ...string) error {
	if !filepath.IsAbs(cliPath) {
		return fmt.Errorf("broker CLI path must be absolute: %q", cliPath)
	}
	if receiverPort < 1 || receiverPort > 65535 {
		return fmt.Errorf("receiver port must be 1 to 65535, got %d", receiverPort)
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" || strings.HasPrefix(value, "-") {
			return fmt.Errorf("broker argument must be non-empty and must not start with '-': %q", value)
		}
	}
	return nil
}

func (b BrokeredTarget) MaterializeWorkspace(ctx context.Context, req MaterializeRequest) (MaterializeResult, error) {
	// The target id becomes a positional broker argument.
	if strings.HasPrefix(req.TargetID, "-") {
		return MaterializeResult{}, fmt.Errorf("target id must not start with '-': %q", req.TargetID)
	}
	result, err := stageInStateRoot(b.Contract, req)
	if err != nil {
		return MaterializeResult{}, err
	}
	// No error path removes the host manifest. Before the broker starts, the
	// manifest of an earlier run with this ID is still true. After that, the
	// unconfirmed manifest is the record that the guest can hold a workspace:
	// a lost acknowledgment, a refusal that kept an earlier guest copy, and a
	// host crash all leave it for an operator to reconcile.
	guestWorkspace, err := b.sendStagedWorkspace(ctx, req.TargetID, &result)
	if err != nil {
		return MaterializeResult{}, err
	}
	result.MaterializedWorkspace = guestWorkspace
	result.Manifest.MaterializedWorkspace = guestWorkspace
	result.Manifest.GuestTransfer = ""
	if err := writeJSON(result.ManifestPath, result.Manifest); err != nil {
		return MaterializeResult{}, err
	}
	return result, nil
}

// sendStagedWorkspace archives the staged host copy and sends it through the
// broker. Before the broker starts, it writes the manifest with the archive
// digest and the unconfirmed state.
func (b BrokeredTarget) sendStagedWorkspace(ctx context.Context, targetID string, staged *MaterializeResult) (string, error) {
	archive, err := os.CreateTemp(staged.MaterializationRoot, ".archive-")
	if err != nil {
		return "", err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	hash := sha256.New()
	if err := writeWorkspaceArchive(io.MultiWriter(archive, hash), staged.MaterializedWorkspace, staged.Manifest.Entries); err != nil {
		return "", err
	}
	size, err := archive.Seek(0, io.SeekCurrent)
	if err != nil {
		return "", err
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	staged.Manifest.ArchiveSHA256 = digest
	staged.Manifest.GuestTransfer = GuestTransferUnconfirmed
	if err := writeJSON(staged.ManifestPath, staged.Manifest); err != nil {
		return "", err
	}
	return runBroker(ctx, b.Command(targetID), func(conn io.ReadWriter) (string, error) {
		return sendWorkspaceArchive(conn, staged.Manifest.MaterializationID, archive, size, digest)
	})
}

// runBroker runs the broker argv without a shell and gives its stdio to
// transfer. A broker that fails reports its exit status. A timeout sends
// SIGTERM, and SIGKILL follows after brokerStopGrace.
func runBroker(ctx context.Context, argv []string, transfer func(io.ReadWriter) (string, error)) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, brokerTransferTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = brokerStopGrace
	// gcloud exits 0 after a failed IAP connection, so its stderr tail is the
	// only cause an operator gets.
	stderr := &tailBuffer{max: 1024}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start broker: %w", err)
	}
	// A broker child that keeps the pipes open must not hold a blocked read
	// past the deadline.
	defer context.AfterFunc(ctx, func() { stdin.Close(); stdout.Close() })()
	result, transferErr := transfer(struct {
		io.Reader
		io.Writer
	}{stdout, stdin})
	stdin.Close()
	if transferErr == nil {
		// The guest acked the archive; the tunnel has no further use.
		cancel()
	} else {
		// Give a failed broker a moment to exit with its own status.
		time.AfterFunc(brokerStopGrace, cancel)
	}
	waitErr := cmd.Wait()
	switch {
	case transferErr == nil:
		return result, nil
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "", fmt.Errorf("broker transfer timed out: %w", errors.Join(transferErr, waitErr))
	case waitErr != nil:
		return "", fmt.Errorf("broker transfer failed (%w): %w; broker stderr: %q", waitErr, transferErr, stderr.b)
	default:
		return "", fmt.Errorf("broker transfer failed: %w; broker stderr: %q", transferErr, stderr.b)
	}
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	b   []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

func (b BrokeredTarget) BootstrapTarget(context.Context, BootstrapRequest) (BootstrapResult, error) {
	return BootstrapResult{}, errBootstrapNotImplemented
}

func (b BrokeredTarget) StartSession(context.Context, StartSessionRequest) (SessionResult, error) {
	return SessionResult{}, errSessionNotImplemented
}

func (b BrokeredTarget) FinishSession(context.Context, FinishSessionRequest) (SessionResult, error) {
	return SessionResult{}, errSessionNotImplemented
}
