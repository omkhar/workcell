// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"syscall"
	"testing"
)

func TestSignalCurrentProcessIdentityUsesBoundHandleInOrder(t *testing.T) {
	events := []string{}
	handle := &reaperSignalHandle{
		pid: 42,
		signal: func(int, syscall.Signal) error {
			events = append(events, "signal")
			return nil
		},
		close: func() error {
			events = append(events, "close")
			return nil
		},
	}
	deps := colimaProcessReaperDependencies{
		openSignal: func(int) (exactProcessSignalHandle, error) {
			events = append(events, "open")
			return handle, nil
		},
		started: func(int) (string, error) {
			events = append(events, "generation")
			return "generation", nil
		},
		list: func(context.Context) ([]byte, error) {
			events = append(events, "command")
			return []byte("42 /usr/local/bin/limactl hostagent /tmp/colima-test/ha.pid\n"), nil
		},
	}
	err := signalCurrentProcessIdentity(context.Background(), "test", colimaProcessIdentity{pid: 42, started: "generation"}, syscall.SIGTERM, deps)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"open", "generation", "command", "generation", "signal", "close"}
	if !slices.Equal(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestSignalCurrentProcessIdentityDoesNotSignalReplacementAfterOpen(t *testing.T) {
	signaled := false
	closed := false
	deps := colimaProcessReaperDependencies{
		openSignal: func(int) (exactProcessSignalHandle, error) {
			return &reaperSignalHandle{pid: 42, signal: func(int, syscall.Signal) error {
				signaled = true
				return nil
			}, close: func() error { closed = true; return nil }}, nil
		},
		started: func(int) (string, error) { return "replacement", nil },
	}
	err := signalCurrentProcessIdentity(context.Background(), "test", colimaProcessIdentity{pid: 42, started: "original"}, syscall.SIGTERM, deps)
	if err != nil || signaled || !closed {
		t.Fatalf("signal replacement = error %v, signaled %t, closed %t", err, signaled, closed)
	}
}

func TestSignalCurrentProcessIdentityReportsHandleErrors(t *testing.T) {
	openErr := errors.New("open failed")
	err := signalCurrentProcessIdentity(context.Background(), "test", colimaProcessIdentity{pid: 42}, syscall.SIGTERM, colimaProcessReaperDependencies{
		openSignal: func(int) (exactProcessSignalHandle, error) { return nil, openErr },
	})
	if !errors.Is(err, openErr) {
		t.Fatalf("open error = %v, want %v", err, openErr)
	}

	signalErr := errors.New("signal failed")
	closeErr := errors.New("close failed")
	deps := colimaProcessReaperDependencies{
		openSignal: func(int) (exactProcessSignalHandle, error) {
			return &reaperSignalHandle{pid: 42, signal: func(int, syscall.Signal) error { return signalErr }, close: func() error { return closeErr }}, nil
		},
		started: func(int) (string, error) { return "generation", nil },
		list: func(context.Context) ([]byte, error) {
			return []byte("42 /usr/local/bin/limactl hostagent /tmp/colima-test/ha.pid\n"), nil
		},
	}
	err = signalCurrentProcessIdentity(context.Background(), "test", colimaProcessIdentity{pid: 42, started: "generation"}, syscall.SIGTERM, deps)
	if !errors.Is(err, signalErr) || !errors.Is(err, closeErr) {
		t.Fatalf("signal/close error = %v", err)
	}
}

func TestSignalCurrentProcessIdentityTreatsGoneBeforeOpenAsComplete(t *testing.T) {
	signaled := false
	closed := false
	err := signalCurrentProcessIdentity(context.Background(), "test", colimaProcessIdentity{pid: 42}, syscall.SIGTERM, colimaProcessReaperDependencies{
		openSignal: func(int) (exactProcessSignalHandle, error) {
			return &reaperSignalHandle{
				pid:    42,
				signal: func(int, syscall.Signal) error { signaled = true; return nil },
				close:  func() error { closed = true; return nil },
			}, processGoneErr{pid: 42}
		},
	})
	if err != nil || signaled || closed {
		t.Fatalf("gone before open = error %v, signaled %t, closed %t", err, signaled, closed)
	}
}

func TestSignalCurrentProcessIdentityTreatsSignalESRCHAsComplete(t *testing.T) {
	for _, signalErr := range []error{
		syscall.ESRCH,
		fmt.Errorf("wrapped signal error: %w", syscall.ESRCH),
	} {
		closed := false
		deps := colimaProcessReaperDependencies{
			openSignal: func(int) (exactProcessSignalHandle, error) {
				return &reaperSignalHandle{
					pid:    42,
					signal: func(int, syscall.Signal) error { return signalErr },
					close:  func() error { closed = true; return nil },
				}, nil
			},
			started: func(int) (string, error) { return "generation", nil },
			list: func(context.Context) ([]byte, error) {
				return []byte("42 /usr/local/bin/limactl hostagent /tmp/colima-test/ha.pid\n"), nil
			},
		}
		err := signalCurrentProcessIdentity(context.Background(), "test", colimaProcessIdentity{pid: 42, started: "generation"}, syscall.SIGTERM, deps)
		if err != nil || !closed {
			t.Errorf("signal ESRCH %q = error %v, closed %t", signalErr, err, closed)
		}
	}
}

func TestLegacyPIDSignalHandleSignalsThroughKill(t *testing.T) {
	handle, err := openLegacyPIDSignalHandle(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Signal(0); err != nil {
		t.Fatalf("legacy handle probe signal = %v, want nil", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("legacy handle close = %v, want nil", err)
	}
}
