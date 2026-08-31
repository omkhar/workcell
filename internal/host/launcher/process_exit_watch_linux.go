// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build linux

package launcher

import (
	"errors"
	"fmt"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

func validateProcessExitWatchSupport() error { return nil }

type pidfdExitWatcher struct {
	doneCh           chan error
	finished, stopCh chan struct{}
	stopOnce         sync.Once
	fd, pid          int
}

func (w *pidfdExitWatcher) done() <-chan error { return w.doneCh }

func (w *pidfdExitWatcher) close() error {
	w.stopOnce.Do(func() { close(w.stopCh) })
	<-w.finished
	return nil
}

func startProcessExitWatch(pid int) (processExitWatcher, error) {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return nil, fmt.Errorf("open process %d exit watch: %w", pid, err)
	}
	watcher := &pidfdExitWatcher{
		doneCh:   make(chan error, 1),
		finished: make(chan struct{}),
		stopCh:   make(chan struct{}),
		fd:       fd,
		pid:      pid,
	}
	go watcher.run()
	return watcher, nil
}

func (w *pidfdExitWatcher) run() {
	var watchErr error
	defer func() {
		w.doneCh <- errors.Join(watchErr, unix.Close(w.fd))
		close(w.finished)
	}()

	pollFDs := []unix.PollFd{{Fd: int32(w.fd), Events: unix.POLLIN}}
	for {
		select {
		case <-w.stopCh:
			return
		default:
		}

		pollFDs[0].Revents = 0
		count, err := unix.Poll(pollFDs, 100)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			watchErr = fmt.Errorf("wait for process %d exit: %w", w.pid, err)
			return
		}
		select {
		case <-w.stopCh:
			return
		default:
		}
		if count == 0 {
			continue
		}
		if count != 1 || pollFDs[0].Revents&unix.POLLIN == 0 {
			watchErr = fmt.Errorf("wait for process %d exit: unexpected pidfd event", w.pid)
		}
		return
	}
}
