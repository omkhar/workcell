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
	if err := validateServerBinary(serverBinary); err != nil {
		return err
	}
	return launchServer(serverBinary, peerUID)
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

func validateServerBinary(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect server binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return errors.New("server binary is not trusted")
	}
	if fileUID(info) != 0 {
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

func launchServer(binary string, peerUID uint32) error {
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
	command := exec.Command(binary, "--socket", aptbroker.DefaultSocketPath, "--peer-uid", strconv.FormatUint(uint64(peerUID), 10), "--ready-fd", "3", "--ack-fd", "4")
	command.Env = fixedServerEnvironment()
	command.Stdin = nil
	command.Stdout = nil
	command.Stderr = os.Stderr
	command.ExtraFiles = []*os.File{readyWriter, ackReader}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
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
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o755 {
		return errors.New("started server socket parent is invalid")
	}
	if fileUID(info) != 0 {
		return errors.New("started server socket parent is not root-owned")
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
