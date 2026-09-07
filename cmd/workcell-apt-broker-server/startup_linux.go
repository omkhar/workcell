// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build linux

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/omkhar/workcell/internal/aptbroker"
	"golang.org/x/sys/unix"
)

const (
	serverBinary       = "/usr/local/libexec/workcell/workcell-apt-broker-server"
	startupLimit       = 5 * time.Second
	startupReadyByte   = byte('R')
	startupAcknowledge = byte('A')
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
	if err := requireUnusedSocket(aptbroker.DefaultSocketPath); err != nil {
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
	return normalizeCreatedSocketParent(path)
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

// A pathname that passes a check and a pathname that exec resolves a moment
// later are not the same guarantee. Any uid that can repoint a parent directory
// could substitute another file between the two lookups, and the starter runs
// as root. The binary is opened once, validated through that descriptor, and
// later executed through that same descriptor, so exec cannot reach a file the
// check did not see. O_NOFOLLOW refuses a symlink at the leaf; a swap higher up
// only changes which file is opened, and the checks below reject it.
func openServerBinary(path string) (*os.File, error) {
	binary, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open server binary: %w", err)
	}
	if err := validateServerBinary(binary); err != nil {
		binary.Close()
		return nil, err
	}
	return binary, nil
}

func validateServerBinary(binary *os.File) error {
	var info unix.Stat_t
	if err := unix.Fstat(int(binary.Fd()), &info); err != nil {
		return fmt.Errorf("inspect server binary: %w", err)
	}
	if info.Mode&unix.S_IFMT != unix.S_IFREG || info.Mode&0o022 != 0 {
		return errors.New("server binary is not trusted")
	}
	if info.Uid != 0 {
		return errors.New("server binary is not root-owned")
	}
	return nil
}

func requireUnusedSocket(path string) error {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect existing server socket: %w", err)
	}
	return errors.New("apt broker socket already exists")
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
	return finishStartup(command.Process, readyReader, ackWriter, func() error {
		return validateStartedSocket(aptbroker.DefaultSocketPath)
	})
}

// exec.Cmd gives the child descriptor 3 upward to ExtraFiles in order, so the
// entries below decide these numbers. The child then executes itself through
// the validated descriptor rather than through a pathname exec would resolve a
// second time. The descriptor stays open in the server: it is read-only on the
// server's own root-owned binary, and exec.Cmd gives it to no helper process.
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
	command.Stdin = nil
	command.Stdout = nil
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

func finishStartup(process startupProcess, ready startupReader, acknowledge io.Writer, validateSocket func() error) error {
	if err := completeStartup(ready, acknowledge, validateSocket); err != nil {
		return stopStartupProcess(process, err)
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

func stopStartupProcess(process startupProcess, cause error) error {
	killErr := process.Kill()
	_, waitErr := process.Wait()
	return errors.Join(cause, killErr, waitErr)
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
