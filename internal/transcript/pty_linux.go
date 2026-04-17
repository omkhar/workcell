// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build linux

package transcript

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"
)

const ptyNameBufferSize = 128

func openPTY() (*os.File, string, error) {
	masterFD, err := syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", err
	}

	unlock := 0
	if err := ioctlInt(masterFD, syscall.TIOCSPTLCK, &unlock); err != nil {
		_ = syscall.Close(masterFD)
		return nil, "", err
	}

	ptyNum := 0
	if err := ioctlInt(masterFD, syscall.TIOCGPTN, &ptyNum); err != nil {
		_ = syscall.Close(masterFD)
		return nil, "", err
	}

	name := "/dev/pts/" + strconv.Itoa(ptyNum)
	return os.NewFile(uintptr(masterFD), "pty-master"), name, nil
}

func ioctlInt(fd int, req uintptr, value *int) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(unsafe.Pointer(value)))
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

	stdinFD := int(stdin.Fd())
	stdoutFD := int(stdout.Fd())
	masterFD := int(master.Fd())
	_ = copyWindowSize(stdinFD, int(slave.Fd()))

	if err := cmd.Start(); err != nil {
		return 0, err
	}

	stopForwarding := forwardWindowSizeChanges(stdinFD, masterFD)
	defer stopForwarding()

	_ = slave.Close()

	stdinClosed := false
	var loopErr error

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

		_, err = syscall.Select(maxFD+1, &readfds, nil, nil, nil)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return 0, err
		}

		if !stdinClosed && fdIsSet(&readfds, stdinFD) {
			data, readErr := stdinRead(stdinFD)
			if len(data) > 0 {
				if err := writeAtFD(masterFD, data); err != nil {
					loopErr = err
					break
				}
			}
			if readErr != nil {
				if isRetryableIOError(readErr) {
					continue
				}
				if errors.Is(readErr, io.EOF) {
					stdinClosed = true
					continue
				}
				loopErr = readErr
				break
			}
			if len(data) == 0 {
				stdinClosed = true
			}
		}

		if fdIsSet(&readfds, masterFD) {
			data, readErr := masterRead(masterFD)
			if len(data) > 0 {
				if err := writeAtFD(stdoutFD, data); err != nil {
					loopErr = err
					break
				}
			}
			if readErr != nil {
				if isRetryableIOError(readErr) {
					continue
				}
				if errors.Is(readErr, io.EOF) {
					break
				}
				loopErr = readErr
				break
			}
			if len(data) == 0 {
				break
			}
		}
	}

	if loopErr != nil && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}

	err = cmd.Wait()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			if loopErr != nil {
				return 0, loopErr
			}
			return 0, err
		}
	}
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok {
		return 0, fmt.Errorf("unexpected process state type %T", cmd.ProcessState.Sys())
	}
	if loopErr != nil {
		return int(status), loopErr
	}
	return int(status), nil
}

func fdSet(set *syscall.FdSet, fd int) {
	wordBits := 8 * int(unsafe.Sizeof(set.Bits[0]))
	index := fd / wordBits
	bit := uint(fd % wordBits)
	set.Bits[index] |= 1 << bit
}

func fdIsSet(set *syscall.FdSet, fd int) bool {
	wordBits := 8 * int(unsafe.Sizeof(set.Bits[0]))
	index := fd / wordBits
	bit := uint(fd % wordBits)
	return set.Bits[index]&(1<<bit) != 0
}
