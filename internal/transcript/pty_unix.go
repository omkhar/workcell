// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin || linux

package transcript

import (
	"os"
	"os/signal"
	"syscall"
	"unsafe"
)

type windowSize struct {
	Rows   uint16
	Cols   uint16
	XPixel uint16
	YPixel uint16
}

func copyWindowSize(sourceFD, targetFD int) error {
	size := windowSize{}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(sourceFD), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&size))); errno != 0 {
		return errno
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(targetFD), uintptr(syscall.TIOCSWINSZ), uintptr(unsafe.Pointer(&size))); errno != 0 {
		return errno
	}
	return nil
}

func forwardWindowSizeChanges(sourceFD, targetFD int) func() {
	_ = copyWindowSize(sourceFD, targetFD)

	winchCh := make(chan os.Signal, 1)
	stopCh := make(chan struct{})
	signal.Notify(winchCh, syscall.SIGWINCH)

	go func() {
		for {
			select {
			case <-stopCh:
				return
			case <-winchCh:
				_ = copyWindowSize(sourceFD, targetFD)
			}
		}
	}()

	return func() {
		signal.Stop(winchCh)
		close(stopCh)
	}
}
