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
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/omkhar/workcell/internal/host/hoststate"
	"github.com/omkhar/workcell/internal/host/sessions"
)

const (
	// ponytail: one fixed bound for a 2 GiB workspace over a slow tunnel; make
	// it a target field when an operator needs a different one.
	brokerTransferTimeout = 30 * time.Minute
	// brokerStopGrace is how long a broker may take to exit on its own, and
	// then how long it may take after SIGTERM before SIGKILL.
	brokerStopGrace = 5 * time.Second
)

var errRunArgvPending = errors.New("remote_vm session start needs the docker run argv that the launcher renders; the launcher renderer is pending (roadmap L4-3 follow-up)")

// BrokeredTarget materializes the workspace on a live guest, prepares the
// guest, and starts and removes the session container through the same
// broker (see BootstrapTarget, StartSession and FinishSession). It copies the
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

// checkBrokerTargetID refuses a target id that the broker CLI would read as
// an option, because it becomes a positional broker argument.
func checkBrokerTargetID(targetID string) error {
	if strings.HasPrefix(targetID, "-") {
		return fmt.Errorf("target id must not start with '-': %q", targetID)
	}
	return nil
}

func (b BrokeredTarget) MaterializeWorkspace(ctx context.Context, req MaterializeRequest) (MaterializeResult, error) {
	if err := checkBrokerTargetID(req.TargetID); err != nil {
		return MaterializeResult{}, err
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
	if err := checkBrokerTargetID(req.TargetID); err != nil {
		return BootstrapResult{}, err
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

// StartSession runs the launcher-rendered detached docker run argv on the
// guest through the broker. Only after the guest acks the container does the
// host write the running session record and the chained start audit records.
// If the start or that write fails, the target removes the container again.
func (b BrokeredTarget) StartSession(ctx context.Context, req StartSessionRequest) (SessionResult, error) {
	if len(req.RunArgv) == 0 {
		return SessionResult{}, errRunArgvPending
	}
	sessionID, err := checkStartSession(req)
	if err != nil {
		return SessionResult{}, err
	}
	if err := checkWireToken("session id", sessionID); err != nil {
		return SessionResult{}, err
	}
	targetID, imageRef := req.Bootstrap.Manifest.TargetID, req.Bootstrap.Manifest.ImageRef
	if err := checkBrokerTargetID(targetID); err != nil {
		return SessionResult{}, err
	}
	if req.Bootstrap.Manifest.EgressFloorSHA256 == "" {
		return SessionResult{}, fmt.Errorf("bootstrap.json records no guest egress floor; bootstrap the target through the broker first")
	}
	if err := checkGuestImageRef(imageRef); err != nil {
		return SessionResult{}, err
	}
	name, err := checkRunArgv(req.RunArgv, imageRef)
	if err != nil {
		return SessionResult{}, err
	}
	if _, err := os.Lstat(sessionRecordPath(req.Bootstrap, sessionID)); !errors.Is(err, fs.ErrNotExist) {
		return SessionResult{}, errors.Join(fmt.Errorf("session %s already has a record", sessionID), err)
	}
	if _, err := runPinned(ctx, "broker", b.Command(targetID), false, func(conn io.ReadWriteCloser) (string, error) {
		return "", sendSessionStart(conn, sessionID, imageRef, name, req.RunArgv)
	}); err != nil {
		// A lost ack can hide a container that the guest started.
		_, stopErr := b.stopGuestContainer(ctx, targetID, sessionID, name)
		return SessionResult{}, errors.Join(err, stopErr)
	}
	result, err := startSessionInStateRoot(b.Contract, req, name, hoststate.AppendAuditRecord)
	if err != nil {
		// No container may run without its record and audit, and no record
		// may claim a container that the target removed.
		_, stopErr := b.stopGuestContainer(ctx, targetID, sessionID, name)
		removeErr := os.Remove(sessionRecordPath(req.Bootstrap, sessionID))
		if errors.Is(removeErr, fs.ErrNotExist) {
			removeErr = nil
		}
		return SessionResult{}, errors.Join(err, stopErr, removeErr)
	}
	return result, nil
}

// FinishSession removes the session container on the guest through the
// broker. Only after the guest acks that docker no longer lists it does the
// host write the final record and the chained session_finished record. A
// session whose record is already final returns that record and calls no
// broker, and a container that an earlier attempt removed acks as absent, so
// a repeated teardown succeeds. Any other failure leaves the record running.
func (b BrokeredTarget) FinishSession(ctx context.Context, req FinishSessionRequest) (SessionResult, error) {
	if err := checkFinishSession(req); err != nil {
		return SessionResult{}, err
	}
	record, err := sessions.ReadSessionRecord(req.Started.RecordPath)
	if err != nil {
		return SessionResult{}, err
	}
	if record.Status == b.Contract.Session.FinalStatus {
		return SessionResult{Record: record, RecordPath: req.Started.RecordPath, AuditLogPath: req.Started.AuditLogPath}, nil
	}
	if err := checkBrokerTargetID(record.TargetID); err != nil {
		return SessionResult{}, err
	}
	if _, err := b.stopGuestContainer(ctx, record.TargetID, record.SessionID, record.ContainerName); err != nil {
		return SessionResult{}, err
	}
	return finishSessionInStateRoot(b.Contract, req, hoststate.AppendAuditRecord)
}

func (b BrokeredTarget) stopGuestContainer(ctx context.Context, targetID, sessionID, name string) (string, error) {
	if err := checkWireToken("session id", sessionID); err != nil {
		return "", err
	}
	if err := checkWireToken("container name", name); err != nil {
		return "", err
	}
	return runPinned(ctx, "broker", b.Command(targetID), false, func(conn io.ReadWriteCloser) (string, error) {
		return sendSessionStop(conn, sessionID, name)
	})
}
