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

var errSessionNotImplemented = errors.New("remote_vm session control over the broker is not implemented yet (roadmap L4-3)")

// BrokeredTarget materializes the workspace on a live guest and prepares the
// guest through the same broker (see BootstrapTarget). It copies the
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
	result, err := materializeInStateRoot(b.Contract, req)
	if err != nil {
		return MaterializeResult{}, err
	}
	guestWorkspace, digest, err := b.sendStagedWorkspace(ctx, req.TargetID, result)
	if err != nil {
		// The host manifest names the host copy; drop it so no record claims
		// a guest materialization that did not happen.
		return MaterializeResult{}, errors.Join(err, os.Remove(result.ManifestPath))
	}
	result.MaterializedWorkspace = guestWorkspace
	result.Manifest.MaterializedWorkspace = guestWorkspace
	result.Manifest.ArchiveSHA256 = digest
	if err := writeJSON(result.ManifestPath, result.Manifest); err != nil {
		return MaterializeResult{}, err
	}
	return result, nil
}

func (b BrokeredTarget) sendStagedWorkspace(ctx context.Context, targetID string, staged MaterializeResult) (string, string, error) {
	archive, err := os.CreateTemp(staged.MaterializationRoot, ".archive-")
	if err != nil {
		return "", "", err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	hash := sha256.New()
	if err := writeWorkspaceArchive(io.MultiWriter(archive, hash), staged.MaterializedWorkspace, staged.Manifest.Entries); err != nil {
		return "", "", err
	}
	size, err := archive.Seek(0, io.SeekCurrent)
	if err != nil {
		return "", "", err
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return "", "", err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	guestWorkspace, err := runPinned(ctx, "broker", b.Command(targetID), false, func(conn io.ReadWriteCloser) (string, error) {
		return sendWorkspaceArchive(conn, staged.Manifest.MaterializationID, archive, size, digest)
	})
	return guestWorkspace, digest, err
}

// runPinned runs argv without a shell and gives its stdio to transfer. Close
// on that stdio closes the child's stdin. A child that fails reports its exit
// status and the stderr tail under name. A timeout sends SIGTERM, and SIGKILL
// follows after brokerStopGrace. After a good transfer, a broker (untilExit
// false) is stopped, because its tunnel has no further use; a command
// (untilExit true) must exit 0 on its own.
func runPinned(ctx context.Context, name string, argv []string, untilExit bool, transfer func(io.ReadWriteCloser) (string, error)) (string, error) {
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
		return "", fmt.Errorf("start %s: %w", name, err)
	}
	// A broker child that keeps the pipes open must not hold a blocked read
	// past the deadline.
	defer context.AfterFunc(ctx, func() { stdin.Close(); stdout.Close() })()
	result, transferErr := transfer(struct {
		io.Reader
		io.WriteCloser
	}{stdout, stdin})
	stdin.Close()
	switch {
	case transferErr != nil:
		// Give a failed child a moment to exit with its own status.
		time.AfterFunc(brokerStopGrace, cancel)
	case !untilExit:
		cancel()
	}
	waitErr := cmd.Wait()
	switch {
	case transferErr == nil && (!untilExit || waitErr == nil):
		return result, nil
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "", fmt.Errorf("%s transfer timed out: %w", name, errors.Join(transferErr, waitErr))
	case transferErr == nil:
		return "", fmt.Errorf("%s failed (%w); %s stderr: %q", name, waitErr, name, stderr.b)
	case waitErr != nil:
		return "", fmt.Errorf("%s transfer failed (%w): %w; %s stderr: %q", name, waitErr, transferErr, name, stderr.b)
	default:
		return "", fmt.Errorf("%s transfer failed: %w; %s stderr: %q", name, transferErr, name, stderr.b)
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

// BootstrapTarget prepares the guest through the broker: the guest receiver
// applies GuestFloorScript (the DOCKER-USER floor), pulls ImageRef by digest
// and checks that the pulled image carries that digest. The host writes
// bootstrap.json with the floor digest only after a matching ack.
func (b BrokeredTarget) BootstrapTarget(ctx context.Context, req BootstrapRequest) (BootstrapResult, error) {
	if strings.HasPrefix(req.TargetID, "-") {
		return BootstrapResult{}, fmt.Errorf("target id must not start with '-': %q", req.TargetID)
	}
	if err := checkGuestImageRef(req.ImageRef); err != nil {
		return BootstrapResult{}, err
	}
	if len(req.GuestFloorScript) == 0 || len(req.GuestFloorScript) > maxFloorScriptBytes {
		return BootstrapResult{}, fmt.Errorf("guest floor script must be 1 to %d bytes", maxFloorScriptBytes)
	}
	result, err := bootstrapInStateRoot(b.Contract, req)
	if err != nil {
		return BootstrapResult{}, err
	}
	sum := sha256.Sum256(req.GuestFloorScript)
	floorDigest := hex.EncodeToString(sum[:])
	if _, err := runPinned(ctx, "broker", b.Command(req.TargetID), false, func(conn io.ReadWriteCloser) (string, error) {
		return "", sendBootstrap(conn, req.ImageRef, req.GuestFloorScript, floorDigest)
	}); err != nil {
		// No record may claim a guest that did not finish its bootstrap.
		return BootstrapResult{}, errors.Join(err, os.Remove(result.ManifestPath))
	}
	result.Manifest.EgressFloorSHA256 = floorDigest
	if err := writeJSON(result.ManifestPath, result.Manifest); err != nil {
		return BootstrapResult{}, err
	}
	return result, nil
}

func (b BrokeredTarget) StartSession(context.Context, StartSessionRequest) (SessionResult, error) {
	return SessionResult{}, errSessionNotImplemented
}

func (b BrokeredTarget) FinishSession(context.Context, FinishSessionRequest) (SessionResult, error) {
	return SessionResult{}, errSessionNotImplemented
}
