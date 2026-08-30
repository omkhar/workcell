// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin

package launcher

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const darwinProcessExitWatchPollInterval = 100 * time.Millisecond

type darwinProcessExitWatcher struct {
	doneCh  chan error
	stopCh  chan struct{}
	stopOne sync.Once
}

func (w *darwinProcessExitWatcher) done() <-chan error { return w.doneCh }

func (w *darwinProcessExitWatcher) close() error {
	w.stopOne.Do(func() { close(w.stopCh) })
	return nil
}

func validateProcessExitWatchSupport() error { return nil }

func startProcessExitWatch(pid int) (processExitWatcher, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("process exit watch requires positive pid: %d", pid)
	}
	kqueue, err := unix.Kqueue()
	if err != nil {
		return nil, fmt.Errorf("create process exit kqueue: %w", err)
	}
	change := unix.Kevent_t{
		Ident:  uint64(pid),
		Filter: unix.EVFILT_PROC,
		Flags:  unix.EV_ADD | unix.EV_ONESHOT,
		Fflags: unix.NOTE_EXIT,
	}
	if _, err := unix.Kevent(kqueue, []unix.Kevent_t{change}, nil, nil); err != nil {
		closeErr := unix.Close(kqueue)
		if errors.Is(err, syscall.ESRCH) {
			// The child can already have exited while it remains unreaped.
			return newCompletedExitWatcher(closeErr), nil
		}
		return nil, errors.Join(fmt.Errorf("register process %d exit watch: %w", pid, err), closeErr)
	}

	watcher := &darwinProcessExitWatcher{
		doneCh: make(chan error, 1),
		stopCh: make(chan struct{}),
	}
	go watcher.wait(kqueue, pid)
	return watcher, nil
}

func (w *darwinProcessExitWatcher) wait(kqueue, pid int) {
	var watchErr error
	for {
		select {
		case <-w.stopCh:
			goto done
		default:
		}

		events := make([]unix.Kevent_t, 1)
		timeout := unix.NsecToTimespec(darwinProcessExitWatchPollInterval.Nanoseconds())
		count, err := unix.Kevent(kqueue, nil, events, &timeout)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			watchErr = fmt.Errorf("wait for process %d exit: %w", pid, err)
			break
		}
		if count == 0 {
			continue
		}
		if count != 1 {
			watchErr = fmt.Errorf("wait for process %d exit: unexpected kqueue event count %d", pid, count)
			break
		}
		event := events[0]
		if event.Flags&unix.EV_ERROR != 0 {
			errno := syscall.Errno(event.Data)
			if errno == 0 {
				watchErr = fmt.Errorf("wait for process %d exit: kqueue returned an unspecified error", pid)
			} else {
				watchErr = fmt.Errorf("wait for process %d exit: %w", pid, errno)
			}
			break
		}
		if event.Filter != unix.EVFILT_PROC || event.Ident != uint64(pid) || event.Fflags&unix.NOTE_EXIT == 0 {
			watchErr = fmt.Errorf("wait for process %d exit: unexpected kqueue event", pid)
		}
		break
	}
done:
	w.doneCh <- errors.Join(watchErr, unix.Close(kqueue))
}
