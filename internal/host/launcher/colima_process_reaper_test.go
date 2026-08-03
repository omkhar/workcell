// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"syscall"
	"testing"
	"time"
)

type reaperSignal struct {
	pid    int
	signal syscall.Signal
}

type reaperFake struct {
	profile     string
	processes   map[int]string
	started     map[int]string
	signals     []reaperSignal
	listErr     error
	startErr    error
	signalErr   error
	termRemoves bool
	killRemoves bool
}

func (f *reaperFake) dependencies() colimaProcessReaperDependencies {
	return colimaProcessReaperDependencies{
		list: func(context.Context) ([]byte, error) {
			if f.listErr != nil {
				return nil, f.listErr
			}
			pids := make([]int, 0, len(f.processes))
			for pid := range f.processes {
				pids = append(pids, pid)
			}
			sort.Ints(pids)
			var output []byte
			for _, pid := range pids {
				output = fmt.Appendf(output, "%d %s\n", pid, f.processes[pid])
			}
			return output, nil
		},
		started: func(pid int) (string, error) {
			if f.startErr != nil {
				return "", f.startErr
			}
			started, exists := f.started[pid]
			if !exists {
				return "", processGoneErr{pid: pid}
			}
			return started, nil
		},
		signal: func(pid int, signal syscall.Signal) error {
			f.signals = append(f.signals, reaperSignal{pid: pid, signal: signal})
			if f.signalErr != nil {
				return f.signalErr
			}
			if signal == syscall.SIGTERM && f.termRemoves ||
				signal == syscall.SIGKILL && f.killRemoves {
				delete(f.processes, pid)
				delete(f.started, pid)
			}
			return nil
		},
		sleep:     func(context.Context, time.Duration) error { return nil },
		termDelay: time.Millisecond,
		termPolls: 1,
		killDelay: time.Millisecond,
	}
}

func newReaperFake() *reaperFake {
	profile := "wcl-c3-test"
	return &reaperFake{
		profile:   profile,
		processes: map[int]string{42: "/usr/local/bin/limactl hostagent /tmp/colima-" + profile + "/ha.pid"},
		started:   map[int]string{42: "started-42"},
	}
}

func TestReapColimaProfileProcessesSignalsBoundIdentity(t *testing.T) {
	tests := []struct {
		name        string
		termRemoves bool
		killRemoves bool
		wantSignals []reaperSignal
		wantError   bool
	}{
		{
			name: "term is sufficient", termRemoves: true,
			wantSignals: []reaperSignal{{pid: 42, signal: syscall.SIGTERM}},
		},
		{
			name: "kill surviving identity", killRemoves: true,
			wantSignals: []reaperSignal{
				{pid: 42, signal: syscall.SIGTERM},
				{pid: 42, signal: syscall.SIGKILL},
			},
		},
		{
			name: "reject surviving identity",
			wantSignals: []reaperSignal{
				{pid: 42, signal: syscall.SIGTERM},
				{pid: 42, signal: syscall.SIGKILL},
			},
			wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := newReaperFake()
			fake.termRemoves, fake.killRemoves = test.termRemoves, test.killRemoves
			err := reapColimaProfileProcesses(context.Background(), fake.profile, fake.dependencies())
			if (err != nil) != test.wantError {
				t.Fatalf("reap error = %v, wantError %t", err, test.wantError)
			}
			if !slices.Equal(fake.signals, test.wantSignals) {
				t.Fatalf("signals = %#v, want %#v", fake.signals, test.wantSignals)
			}
		})
	}
}

func TestReapColimaProfileProcessesPreservesGracefulShutdownWindow(t *testing.T) {
	fake := newReaperFake()
	fake.killRemoves = true
	deps := fake.dependencies()
	deps.termDelay = time.Second
	deps.termPolls = 5
	var sleeps []time.Duration
	deps.sleep = func(_ context.Context, delay time.Duration) error {
		sleeps = append(sleeps, delay)
		return nil
	}
	if err := reapColimaProfileProcesses(context.Background(), fake.profile, deps); err != nil {
		t.Fatalf("reap error = %v", err)
	}
	want := []time.Duration{
		time.Second, time.Second, time.Second, time.Second, time.Second, deps.killDelay,
	}
	if !slices.Equal(sleeps, want) {
		t.Fatalf("sleep delays = %v, want %v", sleeps, want)
	}
}

func TestReapColimaProfileProcessesFailsBeforeSignaling(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*reaperFake)
	}{
		{
			name: "inventory failure",
			mutate: func(fake *reaperFake) {
				fake.listErr = errors.New("ps failed")
			},
		},
		{
			name: "identity failure",
			mutate: func(fake *reaperFake) {
				fake.startErr = errors.New("start time failed")
			},
		},
		{
			name: "signal failure",
			mutate: func(fake *reaperFake) {
				fake.signalErr = errors.New("signal failed")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := newReaperFake()
			test.mutate(fake)
			err := reapColimaProfileProcesses(context.Background(), fake.profile, fake.dependencies())
			if err == nil {
				t.Fatal("reap unexpectedly succeeded")
			}
			if test.name != "signal failure" && len(fake.signals) != 0 {
				t.Fatalf("signals = %#v, want none", fake.signals)
			}
		})
	}
}

func TestCaptureColimaProfileProcessesAcceptsVanishedIdentity(t *testing.T) {
	fake := newReaperFake()
	deps := fake.dependencies()
	calls := 0
	deps.list = func(context.Context) ([]byte, error) {
		calls++
		if calls == 1 {
			return []byte("42 /usr/local/bin/limactl hostagent /tmp/colima-wcl-c3-test/ha.pid\n"), nil
		}
		delete(fake.processes, 42)
		delete(fake.started, 42)
		return nil, nil
	}
	identities, err := captureColimaProcessIdentities(context.Background(), fake.profile, deps)
	if err != nil {
		t.Fatalf("capture error = %v", err)
	}
	if len(identities) != 0 {
		t.Fatalf("identities = %#v, want none", identities)
	}
	if len(fake.signals) != 0 {
		t.Fatalf("signals = %#v, want none", fake.signals)
	}
}

func TestCaptureColimaProfileProcessesRejectsNewIdentity(t *testing.T) {
	fake := newReaperFake()
	deps := fake.dependencies()
	calls := 0
	deps.list = func(context.Context) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, nil
		}
		return []byte("42 /usr/local/bin/limactl hostagent /tmp/colima-wcl-c3-test/ha.pid\n"), nil
	}
	if _, err := captureColimaProcessIdentities(context.Background(), fake.profile, deps); err == nil {
		t.Fatal("capture unexpectedly accepted a new identity")
	}
	if len(fake.signals) != 0 {
		t.Fatalf("signals = %#v, want none", fake.signals)
	}
}

func TestCaptureColimaProfileProcessesAcceptsExitAfterStableInventory(t *testing.T) {
	fake := newReaperFake()
	deps := fake.dependencies()
	startCalls := 0
	deps.started = func(pid int) (string, error) {
		startCalls++
		if startCalls == 2 {
			return "", processGoneErr{pid: pid}
		}
		return fake.started[pid], nil
	}
	identities, err := captureColimaProcessIdentities(context.Background(), fake.profile, deps)
	if err != nil {
		t.Fatalf("capture error = %v", err)
	}
	if len(identities) != 0 {
		t.Fatalf("identities = %#v, want none", identities)
	}
}

func TestCaptureColimaProfileProcessesPreservesRevalidationError(t *testing.T) {
	fake := newReaperFake()
	deps := fake.dependencies()
	cause := errors.New("start lookup failed")
	startCalls := 0
	deps.started = func(pid int) (string, error) {
		startCalls++
		if startCalls == 2 {
			return "", cause
		}
		return fake.started[pid], nil
	}
	if _, err := captureColimaProcessIdentities(context.Background(), fake.profile, deps); !errors.Is(err, cause) {
		t.Fatalf("capture error = %v, want wrapped cause", err)
	}
	if len(fake.signals) != 0 {
		t.Fatalf("signals = %#v, want none", fake.signals)
	}
}

func TestReapColimaProfileProcessesDoesNotSignalReusedPID(t *testing.T) {
	fake := newReaperFake()
	deps := fake.dependencies()
	startCalls := 0
	deps.started = func(pid int) (string, error) {
		startCalls++
		if startCalls == 3 {
			delete(fake.processes, pid)
			fake.started[pid] = "reused"
		}
		return fake.started[pid], nil
	}
	if err := reapColimaProfileProcesses(context.Background(), fake.profile, deps); err != nil {
		t.Fatalf("reap error = %v", err)
	}
	if len(fake.signals) != 0 {
		t.Fatalf("signals = %#v, want none", fake.signals)
	}
}

func TestSignalCurrentProcessIdentityRevalidatesAfterInventory(t *testing.T) {
	fake := newReaperFake()
	deps := fake.dependencies()
	list := deps.list
	deps.list = func(ctx context.Context) ([]byte, error) {
		output, err := list(ctx)
		fake.started[42] = "reused"
		return output, err
	}
	identity := colimaProcessIdentity{pid: 42, started: "started-42"}
	if err := signalCurrentProcessIdentity(
		context.Background(), fake.profile, identity, syscall.SIGTERM, deps,
	); err != nil {
		t.Fatalf("signal identity error = %v", err)
	}
	if len(fake.signals) != 0 {
		t.Fatalf("signals = %#v, want none", fake.signals)
	}
}

func TestReapColimaProfileProcessesRejectsSameStartChangedCommand(t *testing.T) {
	fake := newReaperFake()
	deps := fake.dependencies()
	startCalls := 0
	deps.started = func(pid int) (string, error) {
		startCalls++
		if startCalls == 3 {
			fake.processes[42] = "tail -f /tmp/colima-" + fake.profile + "/ha.pid"
		}
		return fake.started[pid], nil
	}
	if err := reapColimaProfileProcesses(context.Background(), fake.profile, deps); err == nil {
		t.Fatal("reap unexpectedly accepted a changed command identity")
	}
	if len(fake.signals) != 0 {
		t.Fatalf("signals = %#v, want none", fake.signals)
	}
}

func TestReapColimaProfileProcessesDoesNotSignalAfterCancellation(t *testing.T) {
	fake := newReaperFake()
	deps := fake.dependencies()
	ctx, cancel := context.WithCancel(context.Background())
	startCalls := 0
	deps.started = func(pid int) (string, error) {
		startCalls++
		if startCalls == 3 {
			cancel()
		}
		return fake.started[pid], nil
	}
	if err := reapColimaProfileProcesses(ctx, fake.profile, deps); !errors.Is(err, context.Canceled) {
		t.Fatalf("reap error = %v, want context cancellation", err)
	}
	if len(fake.signals) != 0 {
		t.Fatalf("signals = %#v, want none", fake.signals)
	}
}

func TestReapColimaProfileProcessesExcludesCollateralCommands(t *testing.T) {
	fake := newReaperFake()
	fake.termRemoves = true
	fake.processes[43] = "tail -f /tmp/colima-" + fake.profile + "/ha.pid"
	fake.started[43] = "started-43"
	if err := reapColimaProfileProcesses(context.Background(), fake.profile, fake.dependencies()); err != nil {
		t.Fatalf("reap error = %v", err)
	}
	want := []reaperSignal{{pid: 42, signal: syscall.SIGTERM}}
	if !slices.Equal(fake.signals, want) {
		t.Fatalf("signals = %#v, want %#v", fake.signals, want)
	}
	if _, exists := fake.processes[43]; !exists {
		t.Fatal("collateral process was removed")
	}
}

func TestReapColimaProfileProcessesFindsNoUnownedProcess(t *testing.T) {
	profile := fmt.Sprintf("wcl-c3-test-%d", time.Now().UnixNano())
	if err := ReapColimaProfileProcesses(context.Background(), profile); err != nil {
		t.Fatalf("reap error = %v", err)
	}
}

func TestSleepWithContext(t *testing.T) {
	if err := sleepWithContext(context.Background(), 0); err != nil {
		t.Fatalf("zero-duration sleep error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepWithContext(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled sleep error = %v", err)
	}
}
