// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin

package transcript

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

const ptyNameBufferSize = 128

func openPTY() (*os.File, string, error) {
	masterFD, err := syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", err
	}
	if err := ioctlNoArg(masterFD, syscall.TIOCPTYGRANT); err != nil {
		_ = syscall.Close(masterFD)
		return nil, "", err
	}
	if err := ioctlNoArg(masterFD, syscall.TIOCPTYUNLK); err != nil {
		_ = syscall.Close(masterFD)
		return nil, "", err
	}
	name, err := ptySlaveName(masterFD)
	if err != nil {
		_ = syscall.Close(masterFD)
		return nil, "", err
	}
	return os.NewFile(uintptr(masterFD), "pty-master"), name, nil
}

func ptySlaveName(masterFD int) (string, error) {
	buf := make([]byte, ptyNameBufferSize)
	if err := ioctlBytes(masterFD, syscall.TIOCPTYGNAME, &buf[0]); err != nil {
		return "", err
	}
	if n := indexByte(buf, 0); n >= 0 {
		buf = buf[:n]
	}
	if len(buf) == 0 {
		return "", errors.New("pty slave name is empty")
	}
	return string(buf), nil
}

func ioctlNoArg(fd int, req uintptr) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func ioctlBytes(fd int, req uintptr, ptr *byte) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(unsafe.Pointer(ptr)))
	if errno != 0 {
		return errno
	}
	return nil
}

func spawnPTYReal(command []string, stdin, stdout *os.File, stdinRead, masterRead ReadFunc) (int, error) {
	master, slaveName, err := openPTY()
	if err != nil {
		return 0, err
	}
	defer master.Close()

	slave, err := os.OpenFile(slaveName, os.O_RDWR, 0)
	if err != nil {
		return 0, err
	}
	defer slave.Close()

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
	}

	if err := cmd.Start(); err != nil {
		return 0, err
	}

	_ = slave.Close()

	stdinFD := int(stdin.Fd())
	stdoutFD := int(stdout.Fd())
	masterFD := int(master.Fd())
	stdinClosed := false

	for {
		readfds := syscall.FdSet{}
		maxFD := masterFD
		fdSet(&readfds, masterFD)
		if !stdinClosed {
			fdSet(&readfds, stdinFD)
			if stdinFD > maxFD {
				maxFD = stdinFD
			}
		}

		err = syscall.Select(maxFD+1, &readfds, nil, nil, nil)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return 0, err
		}

		if !stdinClosed && fdIsSet(&readfds, stdinFD) {
			data, readErr := stdinRead(stdinFD)
			if len(data) > 0 {
				if err := writeAtFDReal(masterFD, data); err != nil {
					return 0, err
				}
			}
			if readErr != nil || len(data) == 0 {
				stdinClosed = true
			}
		}

		if fdIsSet(&readfds, masterFD) {
			data, readErr := masterRead(masterFD)
			if len(data) > 0 {
				if err := writeAtFDReal(stdoutFD, data); err != nil {
					return 0, err
				}
			}
			if readErr != nil || len(data) == 0 {
				break
			}
		}
	}

	err = cmd.Wait()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return 0, err
		}
	}
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok {
		return 0, fmt.Errorf("unexpected process state type %T", cmd.ProcessState.Sys())
	}
	return int(status), nil
}

func fdSet(set *syscall.FdSet, fd int) {
	index := fd / 32
	bit := uint(fd % 32)
	set.Bits[index] |= 1 << bit
}

func fdIsSet(set *syscall.FdSet, fd int) bool {
	index := fd / 32
	bit := uint(fd % 32)
	return set.Bits[index]&(1<<bit) != 0
}

func indexByte(b []byte, target byte) int {
	for i, c := range b {
		if c == target {
			return i
		}
	}
	return -1
}
