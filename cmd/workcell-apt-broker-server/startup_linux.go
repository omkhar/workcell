// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build linux

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/omkhar/workcell/internal/aptbroker"
	"golang.org/x/sys/unix"
)

const (
	serverBinary          = "/usr/local/libexec/workcell/workcell-apt-broker-server"
	startupLimit          = 5 * time.Second
	staleSocketProbeLimit = time.Second
	startupReadyByte      = byte('R')
	startupAcknowledge    = byte('A')
)

type startupProcess interface {
	Kill() error
	Wait() (*os.ProcessState, error)
	Release() error
}

type startupReader interface {
	io.Reader
	SetReadDeadline(time.Time) error
}

func startServer(arguments []string) error {
	peerUID, err := startPeerUID(arguments)
	if err != nil {
		return err
	}
	if err := prepareSocketParent(filepath.Dir(aptbroker.DefaultSocketPath)); err != nil {
		return err
	}
	serving, err := claimSocketPath(aptbroker.DefaultSocketPath)
	if err != nil || serving {
		return err
	}
	binary, err := openServerBinary(serverBinary)
	if err != nil {
		return err
	}
	defer binary.Close()
	return launchServer(binary, peerUID)
}

func startPeerUID(arguments []string) (uint32, error) {
	if len(arguments) != 2 || arguments[0] != "--peer-uid" {
		return 0, errors.New("start requires --peer-uid")
	}
	value, err := strconv.ParseUint(arguments[1], 10, 32)
	if err != nil {
		return 0, errors.New("invalid peer uid")
	}
	return peerUIDValue(value)
}

func prepareSocketParent(path string) error {
	ancestor := filepath.Dir(path)
	if err := validateStartupDirectory(filepath.Dir(ancestor), false); err != nil {
		return fmt.Errorf("validate socket root directory: %w", err)
	}
	if err := validateStartupDirectory(ancestor, false); err != nil {
		return fmt.Errorf("validate socket parent directory: %w", err)
	}
	if err := createSocketParent(path); err != nil {
		return err
	}
	if err := validateStartupDirectory(path, true); err != nil {
		return fmt.Errorf("validate socket parent: %w", err)
	}
	return nil
}

func createSocketParent(path string) error {
	err := os.Mkdir(path, 0o755)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create socket parent: %w", err)
	}
	if err := normalizeCreatedSocketParent(path); err != nil {
		return errors.Join(err, removeUnpublishedSocketParent(path))
	}
	return nil
}

// removeUnpublishedSocketParent unmakes a directory this call created but never
// finished normalizing. Nothing has been placed in it yet, so removing it is
// free. Leaving it behind publishes whatever mode the umask chose, and every
// later attempt then takes the ErrExist branch above, skips normalization, and
// fails the exact-0755 validation, wedging startup until someone repairs it by
// hand.
func removeUnpublishedSocketParent(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove unpublished socket parent: %w", err)
	}
	return nil
}

func normalizeCreatedSocketParent(path string) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open new socket parent: %w", err)
	}
	defer unix.Close(fd)
	if err := unix.Fchmod(fd, 0o755); err != nil {
		return fmt.Errorf("set new socket parent mode: %w", err)
	}
	var info unix.Stat_t
	if err := unix.Fstat(fd, &info); err != nil {
		return fmt.Errorf("inspect new socket parent: %w", err)
	}
	return validateCreatedSocketParent(info)
}

func validateCreatedSocketParent(info unix.Stat_t) error {
	if info.Uid != 0 {
		return errors.New("new socket parent is not root-owned")
	}
	if info.Mode&unix.S_IFMT != unix.S_IFDIR {
		return errors.New("new socket parent is not a directory")
	}
	if info.Mode&0o777 != 0o755 {
		return errors.New("new socket parent metadata is not trusted")
	}
	return nil
}

func validateStartupDirectory(path string, exactMode bool) error {
	info, err := os.Lstat(path)
	if !isStartupDirectory(info, err) {
		return errors.New("directory is not trusted")
	}
	if !hasTrustedStartupMode(info) {
		return errors.New("directory ownership or mode is not trusted")
	}
	if exactMode && info.Mode().Perm() != 0o755 {
		return errors.New("directory mode is not 0755")
	}
	return nil
}

func isStartupDirectory(info os.FileInfo, err error) bool {
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

func hasTrustedStartupMode(info os.FileInfo) bool {
	return fileUID(info) == 0 && info.Mode().Perm()&0o022 == 0
}

const startupDirectoryFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC

// Checking a pathname and then letting exec resolve it again are two lookups,
// and the starter runs as root. O_NOFOLLOW covers only one component, so every
// directory is opened from its parent and must be root-owned and root-writable
// only, and the leaf descriptor fstat accepts is the one exec receives.
func openServerBinary(path string) (*os.File, error) {
	directory, err := unix.Open("/", startupDirectoryFlags, 0)
	if err != nil {
		return nil, fmt.Errorf("open server binary root: %w", err)
	}
	defer func() { _ = unix.Close(directory) }()
	names := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for _, name := range names[:len(names)-1] {
		if err := validateStartupDescriptor(directory, unix.S_IFDIR); err != nil {
			return nil, err
		}
		next, err := unix.Openat(directory, name, startupDirectoryFlags, 0)
		if err != nil {
			return nil, fmt.Errorf("open server binary ancestor %s: %w", name, err)
		}
		_ = unix.Close(directory)
		directory = next
	}
	if err := validateStartupDescriptor(directory, unix.S_IFDIR); err != nil {
		return nil, err
	}
	leaf, err := unix.Openat(directory, names[len(names)-1], unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open server binary: %w", err)
	}
	binary := os.NewFile(uintptr(leaf), path)
	if err := validateStartupDescriptor(leaf, unix.S_IFREG); err != nil {
		binary.Close()
		return nil, err
	}
	return binary, nil
}

func validateStartupDescriptor(descriptor int, kind uint32) error {
	var info unix.Stat_t
	if err := unix.Fstat(descriptor, &info); err != nil {
		return fmt.Errorf("inspect server binary path: %w", err)
	}
	if info.Mode&unix.S_IFMT != kind {
		return errors.New("server binary path component has the wrong type")
	}
	if info.Uid != 0 || info.Mode&0o022 != 0 {
		return errors.New("server binary path is writable outside root")
	}
	return nil
}

// claimSocketPath makes the pathname ready to bind, and reports whether a
// server is already serving it.
//
// Two things reach this. The entrypoint can run more than once in one
// container, and the second --start must not fail on the socket the first one
// left serving: that is the idempotence the pid-file check used to provide.
// And an unclean stop leaves the pathname behind with no listener, because
// SIGKILL denies the server its own cleanup and bindSocket disables
// unlink-on-close; without recovery every later --start fails on it until
// someone removes it by hand.
//
// prepareSocketParent has already proved this directory is root-owned and
// writable by root alone, so no unprivileged uid can have placed this pathname
// here or swap it while we look. A socket that is still serving is accepted
// only after it passes the same validation a socket this call started would.
func claimSocketPath(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect existing server socket: %w", err)
	}
	if !isStartedSocket(info, nil) {
		return false, errors.New("apt broker socket path is not a socket")
	}
	switch err := socketProbe(path); {
	case err == nil:
		return true, validateStartedSocket(path)
	case errors.Is(err, syscall.ECONNREFUSED):
		// Nothing is listening, so this attempt claims the pathname.
	default:
		return false, fmt.Errorf("probe existing server socket: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return false, fmt.Errorf("remove stale server socket: %w", err)
	}
	return false, nil
}

// socketProbe reports what a connect to the pathname proves. A nil error means
// a server is listening. syscall.ECONNREFUSED proves nothing is, so the
// pathname is stale. Any other error leaves the question open, and both
// decisions that read this probe fail closed on it: a live server never has its
// socket unlinked, and a pathname the starter cannot reach is never reported as
// serving.
//
// ponytail: connect probe. The kernel accepts this connection before the server
// checks peer credentials, so a live server refuses it as a root peer and says
// so on stderr. That is the credential control working, but on a repeated
// --start it reads like an attack rather than a liveness check. Read the
// SO_ACCEPTCON flag for the socket's inode in /proc/net/unix instead if that
// noise ever matters.
func socketProbe(path string) error {
	connection, err := net.DialTimeout("unix", path, staleSocketProbeLimit)
	if err != nil {
		return err
	}
	_ = connection.Close()
	return nil
}

// removeAbandonedSocket clears the socket a killed startup left behind. The
// child is stopped with SIGKILL, so its own deferred cleanup never runs.
//
// claimSocketPath proved the pathname was free just before the launch, but that
// does not make a socket here this attempt's own: two overlapping --start calls
// can both find the pathname free, and then one child binds while the other
// fails. The loser must not unlink the winner's live socket, which would leave
// the winner serving an unlinked inode and every later client unable to
// connect. Only a refusal proves the socket here is the dead one this attempt
// left behind.
func removeAbandonedSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect abandoned server socket: %w", err)
	}
	if !isStartedSocket(info, nil) {
		return errors.New("failed startup left a path that is not its socket")
	}
	switch err := socketProbe(path); {
	case err == nil:
		return errors.New("failed startup left a socket another server is serving")
	case errors.Is(err, syscall.ECONNREFUSED):
		// The socket has no listener, so it is this attempt's own remains.
	default:
		return fmt.Errorf("probe abandoned server socket: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove abandoned server socket: %w", err)
	}
	return nil
}

func launchServer(binary *os.File, peerUID uint32) error {
	readyReader, readyWriter, err := os.Pipe()
	if err != nil {
		return err
	}
	defer readyReader.Close()
	ackReader, ackWriter, err := os.Pipe()
	if err != nil {
		readyWriter.Close()
		return err
	}
	defer ackWriter.Close()
	command := serverCommand(binary, readyWriter, ackReader, peerUID)
	if err := command.Start(); err != nil {
		readyWriter.Close()
		ackReader.Close()
		return err
	}
	readyWriter.Close()
	ackReader.Close()
	return finishStartup(command.Process, aptbroker.DefaultSocketPath, readyReader, ackWriter, func() error {
		return validateStartedSocket(aptbroker.DefaultSocketPath)
	})
}

// exec.Cmd hands ExtraFiles to the child from descriptor 3 upward, so the order
// below fixes these numbers and the child execs the validated descriptor rather
// than a pathname. That descriptor stays open in the server: it is read-only on
// the server's own binary, and exec.Cmd passes it to no helper process.
const (
	startupReadyFD  = 3
	startupAckFD    = 4
	startupBinaryFD = 5
)

func serverCommand(binary, ready, acknowledge *os.File, peerUID uint32) *exec.Cmd {
	command := exec.Command(
		"/proc/self/fd/"+strconv.Itoa(startupBinaryFD),
		"--socket", aptbroker.DefaultSocketPath,
		"--peer-uid", strconv.FormatUint(uint64(peerUID), 10),
		"--ready-fd", strconv.Itoa(startupReadyFD),
		"--ack-fd", strconv.Itoa(startupAckFD),
	)
	command.Env = fixedServerEnvironment()
	command.Stderr = os.Stderr
	command.ExtraFiles = []*os.File{ready, acknowledge, binary}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return command
}

func fixedServerEnvironment() []string {
	return []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"LC_ALL=C",
		"LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so",
		"WORKCELL_APT_BROKER_HELPER_TIMEOUT_SECONDS=" + os.Getenv("WORKCELL_APT_BROKER_HELPER_TIMEOUT_SECONDS"),
	}
}

func finishStartup(process startupProcess, socketPath string, ready startupReader, acknowledge io.Writer, validateSocket func() error) error {
	if err := completeStartup(ready, acknowledge, validateSocket); err != nil {
		return stopStartupProcess(process, socketPath, err)
	}
	// os.Process.Release cannot fail on Linux. Keep the return value so a
	// future platform implementation cannot silently change this contract.
	return process.Release()
}

func completeStartup(ready startupReader, acknowledge io.Writer, validateSocket func() error) error {
	body, err := readStartupReady(ready)
	if err != nil || !bytes.Equal(body, []byte{startupReadyByte}) {
		return errors.Join(errors.New("invalid server readiness response"), err)
	}
	if err := validateSocket(); err != nil {
		return err
	}
	if _, err := acknowledge.Write([]byte{startupAcknowledge}); err != nil {
		return fmt.Errorf("acknowledge server readiness: %w", err)
	}
	return nil
}

func readStartupReady(ready startupReader) ([]byte, error) {
	if err := ready.SetReadDeadline(time.Now().Add(startupLimit)); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(ready, 2))
}

func stopStartupProcess(process startupProcess, socketPath string, cause error) error {
	killErr := process.Kill()
	_, waitErr := process.Wait()
	return errors.Join(cause, killErr, waitErr, removeAbandonedSocket(socketPath))
}

func startupHandshake(readyFD, acknowledgeFD uintptr) (func() error, error) {
	if readyFD == 0 && acknowledgeFD == 0 {
		return nil, nil
	}
	if !validStartupDescriptors(readyFD, acknowledgeFD) {
		return nil, errors.New("invalid startup handshake descriptors")
	}
	ready := os.NewFile(readyFD, "startup-ready")
	acknowledge := os.NewFile(acknowledgeFD, "startup-acknowledge")
	return func() error {
		defer acknowledge.Close()
		if err := writeStartupReady(ready); err != nil {
			return err
		}
		return readStartupAcknowledgement(acknowledge)
	}, nil
}

func validStartupDescriptors(readyFD, acknowledgeFD uintptr) bool {
	return readyFD >= 3 && acknowledgeFD >= 3 && readyFD != acknowledgeFD
}

func writeStartupReady(ready *os.File) error {
	_, writeErr := ready.Write([]byte{startupReadyByte})
	return errors.Join(writeErr, ready.Close())
}

func readStartupAcknowledgement(acknowledge *os.File) error {
	var ack [1]byte
	if _, err := io.ReadFull(acknowledge, ack[:]); err != nil {
		return err
	}
	if ack[0] != startupAcknowledge {
		return errors.New("invalid startup acknowledgement")
	}
	return nil
}

func validateStartedSocket(path string) error {
	if err := validateStartedSocketParent(filepath.Dir(path)); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if !isStartedSocket(info, err) {
		return errors.New("started server socket is invalid")
	}
	if fileUID(info) != 0 || info.Mode().Perm() != 0o666 {
		return errors.New("started server socket metadata is invalid")
	}
	return nil
}

func isStartedSocket(info os.FileInfo, err error) bool {
	return err == nil && info.Mode()&os.ModeSymlink == 0 && info.Mode()&os.ModeSocket != 0
}

func validateStartedSocketParent(path string) error {
	if err := validateStartupDirectory(path, true); err != nil {
		return fmt.Errorf("started server socket parent is invalid: %w", err)
	}
	return nil
}

func fileUID(info os.FileInfo) uint32 {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ^uint32(0)
	}
	return stat.Uid
}
