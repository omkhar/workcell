// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProcessGroupSupervisorRunRetainsCancellationResult(t *testing.T) {
	cause := errors.New("cancel fixture")
	cleanupErr := errors.New("cleanup fixture")
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	marker := filepath.Join(t.TempDir(), "ready")
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `printf ready >"$1"; sleep 30`, "fixture", marker)
	s := defaultProcessGroupSupervisor()
	s.signalGroup = func(groupID int, signal syscall.Signal) error {
		if err := signalProcessGroup(groupID, signal); err != nil {
			return err
		}
		return cleanupErr
	}
	go func() {
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(marker); err == nil {
				cancel(cause)
				return
			}
		}
		cancel(cause)
	}()
	result := s.run(ctx, cmd)
	if !result.cleanupRan || result.preStartCanceled || !errors.Is(result.cleanupErr, cleanupErr) ||
		!errors.Is(result.cause, cause) || result.runErr == nil {
		t.Fatalf("run result = %+v", result)
	}

	preCtx, preCancel := context.WithCancelCause(context.Background())
	preCancel(cause)
	preResult := defaultProcessGroupSupervisor().run(preCtx, exec.CommandContext(preCtx, "/usr/bin/true"))
	if !preResult.preStartCanceled || preResult.cleanupRan || !errors.Is(preResult.cause, cause) {
		t.Fatalf("pre-start run result = %+v", preResult)
	}
}

func TestProcessGroupSupervisorTerminate(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("fixture failure")
	for _, tc := range []struct {
		name       string
		waits      []bool
		signalFail int
		waitFail   int
		wantSignal []syscall.Signal
		wantErr    error
		wantText   string
	}{
		{name: "TERM is sufficient", waits: []bool{true}, wantSignal: []syscall.Signal{syscall.SIGTERM}},
		{name: "escalates to KILL", waits: []bool{false, true}, wantSignal: []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}},
		{name: "TERM fails", signalFail: 1, wantSignal: []syscall.Signal{syscall.SIGTERM}, wantErr: sentinel},
		{name: "TERM poll fails", waitFail: 1, wantSignal: []syscall.Signal{syscall.SIGTERM}, wantErr: sentinel},
		{name: "KILL fails", waits: []bool{false}, signalFail: 2, wantSignal: []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}, wantErr: sentinel},
		{name: "KILL poll fails", waits: []bool{false}, waitFail: 2, wantSignal: []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}, wantErr: sentinel},
		{name: "group survives KILL", waits: []bool{false, false}, wantSignal: []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}, wantText: "remains after SIGKILL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var signals []syscall.Signal
			waitCall := 0
			s := processGroupSupervisor{
				signalGroup: func(_ int, signal syscall.Signal) error {
					signals = append(signals, signal)
					if len(signals) == tc.signalFail {
						return sentinel
					}
					return nil
				},
				waitAbsent: func(_ int, _ time.Duration) (bool, error) {
					waitCall++
					if waitCall == tc.waitFail {
						return false, sentinel
					}
					if waitCall <= len(tc.waits) {
						return tc.waits[waitCall-1], nil
					}
					return false, nil
				},
				termGrace:  time.Second,
				proofLimit: time.Second,
			}
			err := s.terminate(&exec.Cmd{Process: &os.Process{Pid: 4242}})
			if !reflect.DeepEqual(signals, tc.wantSignal) {
				t.Fatalf("signals = %v, want %v", signals, tc.wantSignal)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantText != "" && (err == nil || !strings.Contains(err.Error(), tc.wantText)) {
				t.Fatalf("error = %v, want %q", err, tc.wantText)
			}
			if tc.wantErr == nil && tc.wantText == "" && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWaitForProcessGroupExitTreatsEPERMAsPresent(t *testing.T) {
	t.Parallel()
	now := time.Unix(1, 0)
	absent, err := waitForProcessGroupExitWithProbe(
		42,
		0,
		func(pid int, signal syscall.Signal) error {
			if pid != -42 || signal != 0 {
				t.Fatalf("probe = (%d, %d)", pid, signal)
			}
			return syscall.EPERM
		},
		func() time.Time { return now },
		func(time.Duration) { t.Fatal("unexpected sleep") },
	)
	if err != nil || absent {
		t.Fatalf("waitForProcessGroupExitWithProbe() = %v, %v, want present", absent, err)
	}
}
