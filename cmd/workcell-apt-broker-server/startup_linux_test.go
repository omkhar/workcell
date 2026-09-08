// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build linux

package main

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type fixtureStartupProcess struct {
	log *[]string
}

func (p fixtureStartupProcess) Kill() error {
	*p.log = append(*p.log, "kill")
	return nil
}

func (p fixtureStartupProcess) Wait() (*os.ProcessState, error) {
	*p.log = append(*p.log, "wait")
	return nil, nil
}

func (p fixtureStartupProcess) Release() error {
	*p.log = append(*p.log, "release")
	return nil
}

type fixtureStartupReader struct {
	io.Reader
	deadlineErr error
}

type fixtureErrorReader struct {
	err error
}

func (r fixtureErrorReader) Read([]byte) (int, error) {
	return 0, r.err
}

func (r fixtureStartupReader) SetReadDeadline(time.Time) error {
	return r.deadlineErr
}

type fixtureStartupWriter struct {
	log *[]string
	err error
}

func (w fixtureStartupWriter) Write(body []byte) (int, error) {
	*w.log = append(*w.log, "ack")
	if w.err != nil {
		return 0, w.err
	}
	return len(body), nil
}

func TestStartPeerUIDRequiresExactArgument(t *testing.T) {
	for _, arguments := range [][]string{nil, {"--peer-uid"}, {"--peer-uid", "0"}, {"--socket", "1000"}, {"--peer-uid", "1000", "extra"}} {
		if _, err := startPeerUID(arguments); err == nil {
			t.Fatalf("startPeerUID(%q) succeeded", arguments)
		}
	}
	if uid, err := startPeerUID([]string{"--peer-uid", "1000"}); err != nil || uid != 1000 {
		t.Fatalf("startPeerUID() = (%d, %v)", uid, err)
	}
}

func TestStartupHandshakeRequiresAcknowledgement(t *testing.T) {
	readyReader, readyWriter := testPipe(t)
	defer readyReader.Close()
	ackReader, ackWriter := testPipe(t)
	defer ackWriter.Close()
	handshake, err := startupHandshake(readyWriter.Fd(), ackReader.Fd())
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- handshake() }()
	ready, err := io.ReadAll(readyReader)
	if err != nil || !reflect.DeepEqual(ready, []byte{startupReadyByte}) {
		t.Fatalf("readiness = (%q, %v)", ready, err)
	}
	select {
	case err := <-finished:
		t.Fatalf("handshake completed before acknowledgement: %v", err)
	default:
	}
	if _, err := ackWriter.Write([]byte{startupAcknowledge}); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func testPipe(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	return reader, writer
}

// The leaf here is an ordinary file the leaf checks alone would accept. Only
// the ancestor walk refuses it, because a directory above it is writable by any
// uid, and that is the uid that would swap the leaf.
func TestOpenServerBinaryRejectsUntrustedAncestry(t *testing.T) {
	ancestor := filepath.Join(t.TempDir(), "ancestor")
	path := filepath.Join(ancestor, "server")
	if err := errors.Join(os.Mkdir(ancestor, 0o777), os.Chmod(ancestor, 0o777), os.WriteFile(path, nil, 0o755)); err != nil {
		t.Fatal(err)
	}
	binary, err := openServerBinary(path)
	if err == nil {
		binary.Close()
		t.Fatal("untrusted server binary path was accepted")
	}
}

// The type check runs before the ownership check, so a directory never
// satisfies the leaf and a file never satisfies an ancestor, at any uid.
func TestValidateStartupDescriptorRejectsWrongType(t *testing.T) {
	for _, test := range []struct {
		target string
		flags  int
		kind   uint32
	}{
		{target: t.TempDir(), flags: startupDirectoryFlags, kind: unix.S_IFREG},
		{target: os.Args[0], flags: unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC, kind: unix.S_IFDIR},
	} {
		descriptor, err := unix.Open(test.target, test.flags, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateStartupDescriptor(descriptor, test.kind); err == nil {
			t.Errorf("%s was accepted as type %o", test.target, test.kind)
		}
		unix.Close(descriptor)
	}
}

// The descriptor the child executes must be the one openServerBinary validated.
// Both the exec path and the handshake numbers come from the ExtraFiles order,
// so the order and the path are asserted together.
func TestServerCommandExecutesTheValidatedDescriptor(t *testing.T) {
	binary, ready, acknowledge := os.Stdout, os.Stdin, os.Stderr
	command := serverCommand(binary, ready, acknowledge, 1000)
	if got, want := command.Path, "/proc/self/fd/"+strconv.Itoa(startupBinaryFD); got != want {
		t.Fatalf("command path = %q, want %q", got, want)
	}
	for descriptor, want := range map[int]*os.File{startupReadyFD: ready, startupAckFD: acknowledge, startupBinaryFD: binary} {
		if got := command.ExtraFiles[descriptor-3]; got != want {
			t.Fatalf("descriptor %d does not carry the expected file", descriptor)
		}
	}
}

func TestFixedServerEnvironmentIsMinimal(t *testing.T) {
	t.Setenv("WORKCELL_APT_BROKER_HELPER_TIMEOUT_SECONDS", "7")
	want := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"LC_ALL=C",
		"LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so",
		"WORKCELL_APT_BROKER_HELPER_TIMEOUT_SECONDS=7",
	}
	if got := fixedServerEnvironment(); !reflect.DeepEqual(got, want) {
		t.Fatalf("server environment = %q", got)
	}
}

func TestFinishStartupTransfersOnlyValidatedServer(t *testing.T) {
	fixtureErr := errors.New("fixture failure")
	for _, test := range []struct {
		name        string
		ready       startupReader
		validateErr error
		ackErr      error
		wantLog     []string
		wantError   bool
	}{
		{name: "success", ready: startupBody("R"), wantLog: []string{"validate", "ack", "release"}},
		{name: "empty readiness", ready: startupBody(""), wantLog: []string{"kill", "wait"}, wantError: true},
		{name: "duplicate readiness", ready: startupBody("RR"), wantLog: []string{"kill", "wait"}, wantError: true},
		{name: "deadline failure", ready: fixtureStartupReader{Reader: bytes.NewReader(nil), deadlineErr: fixtureErr}, wantLog: []string{"kill", "wait"}, wantError: true},
		{name: "read timeout", ready: fixtureStartupReader{Reader: fixtureErrorReader{err: fixtureErr}}, wantLog: []string{"kill", "wait"}, wantError: true},
		{name: "socket metadata", ready: startupBody("R"), validateErr: fixtureErr, wantLog: []string{"validate", "kill", "wait"}, wantError: true},
		{name: "acknowledgement", ready: startupBody("R"), ackErr: fixtureErr, wantLog: []string{"validate", "ack", "kill", "wait"}, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var log []string
			validate := func() error {
				log = append(log, "validate")
				return test.validateErr
			}
			writer := fixtureStartupWriter{log: &log, err: test.ackErr}
			socketPath := boundStartupSocket(t)
			err := finishStartup(fixtureStartupProcess{log: &log}, socketPath, test.ready, writer, validate)
			if !reflect.DeepEqual(log, test.wantLog) || (err != nil) != test.wantError {
				t.Fatalf("finishStartup() = (%v, %v), want (%v, error=%v)", log, err, test.wantLog, test.wantError)
			}
			// A killed child cannot unlink the socket it bound, so the starter
			// has to. Anything left behind fails every later --start.
			_, statErr := os.Lstat(socketPath)
			if removed := errors.Is(statErr, os.ErrNotExist); removed != test.wantError {
				t.Fatalf("socket removed = %v, want %v (stat %v)", removed, test.wantError, statErr)
			}
		})
	}
}

func startupBody(body string) startupReader {
	return fixtureStartupReader{Reader: bytes.NewBufferString(body)}
}

// boundStartupSocket stands in for the socket a launched server binds. Close
// must not unlink it, or the test could not tell the starter's cleanup apart
// from the listener's own.
//
// The directory is not tb.TempDir(): that path is TMPDIR plus the full subtest
// name, which overruns the AF_UNIX sun_path limit under the TMPDIR CI exports
// and makes bind() fail with a bare EINVAL. internal/aptbroker keeps its own
// short-directory helpers for the same reason.
func boundStartupSocket(tb testing.TB) string {
	tb.Helper()

	directory, err := os.MkdirTemp("/tmp", "wcbs-")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := filepath.Join(directory, "socket")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		tb.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	// A SIGKILLed child leaves the pathname with nothing behind it, and that is
	// what removeAbandonedSocket has to recognise as its own remains. Closing
	// the listener here rather than at cleanup is what makes this fixture that
	// rather than a live server the starter must not touch.
	if err := listener.Close(); err != nil {
		tb.Fatal(err)
	}
	return path
}

func TestPrepareSocketParentRejectsSymlinkWithoutChangingTarget(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("root-owned directory fixture requires root")
	}
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "socket-root")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := prepareSocketParent(path); err == nil {
		t.Fatal("symlink socket parent was accepted")
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("symlink target changed: (%v, %v)", info, err)
	}
}

func TestCreateSocketParentNormalizesRestrictiveUmask(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("root ownership check requires root")
	}
	path := filepath.Join(t.TempDir(), "socket-root")
	oldMask := syscall.Umask(0o077)
	defer syscall.Umask(oldMask)
	if err := createSocketParent(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("created socket parent = (%v, %v)", info, err)
	}
}

func TestCreateSocketParentDoesNotModifyExistingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "socket-root")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := createSocketParent(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("existing socket parent changed: (%v, %v)", info, err)
	}
}

func TestClaimSocketPathRejectsAPathThatIsNotASocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "socket")
	serving, err := claimSocketPath(path)
	if serving || err != nil {
		t.Fatalf("missing socket was rejected: (%v, %v)", serving, err)
	}
	if err := os.WriteFile(path, []byte("stale fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := claimSocketPath(path); err == nil {
		t.Fatal("a regular file at the socket path was accepted")
	}
}

func TestClaimSocketPathKeepsALiveSocketAndClearsAStaleOne(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("a live socket is accepted only after root-owner validation")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "socket")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	// The entrypoint can run twice in one container. The second --start has to
	// accept the socket the first one left serving rather than fail on it.
	serving, err := claimSocketPath(path)
	if !serving || err != nil {
		t.Fatalf("a live socket was not reported as serving: (%v, %v)", serving, err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("a live socket was removed: %v", err)
	}
	// An unclean stop leaves the pathname without a listener behind it.
	// Recovering it is the difference between the next --start working and
	// every --start failing until someone removes the socket by hand.
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	serving, err = claimSocketPath(path)
	if serving || err != nil {
		t.Fatalf("stale socket was not recovered: (%v, %v)", serving, err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale socket survived recovery: %v", err)
	}
}

func TestRemoveAbandonedSocketKeepsAConcurrentStartersLiveSocket(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("a started socket is recognised only after root-owner validation")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "socket")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}

	// Two overlapping --start calls both found the pathname free. This is the
	// loser cleaning up after its own failed child, and the socket it finds
	// belongs to the winner.
	if err := removeAbandonedSocket(path); err == nil {
		t.Fatal("a live socket was accepted as this attempt's own remains")
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("a concurrent starter's live socket was removed: %v", err)
	}

	// Once the winner stops, the pathname really is abandoned.
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := removeAbandonedSocket(path); err != nil {
		t.Fatalf("a stale socket was not cleared: %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale socket survived cleanup: %v", err)
	}
}

func TestClaimSocketPathFailsClosedOnAnInconclusiveProbe(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("a live socket is accepted only after root-owner validation")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "socket")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	defer func() { _ = listener.Close() }()
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}

	// The same socket named through a path too long for sun_path. Lstat still
	// answers, so the metadata checks pass and only the connect fails -- with
	// something other than a refusal, which is what "inconclusive" means here.
	deep := filepath.Join(root, strings.Repeat("d", 90))
	if err := os.Mkdir(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(deep, "alias")); err != nil {
		t.Fatal(err)
	}
	long := filepath.Join(deep, "alias", "socket")
	if len(long) <= 108 {
		t.Fatalf("the fixture path is not long enough to be inconclusive: %d bytes", len(long))
	}
	if info, err := os.Lstat(long); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("the fixture must still lstat as a socket: (%v, %v)", info, err)
	}
	if probe := socketProbe(long); probe == nil || errors.Is(probe, syscall.ECONNREFUSED) {
		t.Fatalf("the fixture probe is not inconclusive: %v", probe)
	}

	// An open question must not be reported as a serving broker: the entrypoint
	// would drop to the mapped user with nothing behind the socket.
	serving, err := claimSocketPath(long)
	if serving || err == nil {
		t.Fatalf("an inconclusive probe was reported as serving: (%v, %v)", serving, err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("an inconclusive probe removed the socket: %v", err)
	}
}

// The probe that proves a socket dead and the unlink that acts on it are two
// pathname lookups. The startup turn is what makes them one decision, so a
// second starter cannot bind between them and have its socket removed.
func TestHoldStartupTurnExcludesASecondStarter(t *testing.T) {
	parent := t.TempDir()
	release, err := holdStartupTurn(parent)
	if err != nil {
		t.Fatal(err)
	}

	// flock is per open file description, so the exclusion is asserted against a
	// second one rather than by calling holdStartupTurn again.
	file, err := os.OpenFile(filepath.Join(parent, startupLockName),
		os.O_CREATE|os.O_RDWR|unix.O_CLOEXEC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); !errors.Is(err, unix.EWOULDBLOCK) {
		t.Fatalf("a second startup turn was granted while the first was held: %v", err)
	}

	release()
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatalf("the startup turn was not released: %v", err)
	}
	_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
}

func TestCreateSocketParentRemovesTheDirectoryItCannotNormalize(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("normalization only fails for a non-root owner")
	}
	path := filepath.Join(t.TempDir(), "socket-root")
	if err := createSocketParent(path); err == nil {
		t.Fatal("a socket parent this uid cannot own was accepted")
	}
	// Leaving the directory behind would make every retry take the ErrExist
	// path, skip normalization, and fail the exact-0755 validation forever.
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unpublished socket parent survived: %v", err)
	}
}
