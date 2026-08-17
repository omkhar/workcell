// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"context"
	"errors"
	"fmt"
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
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `printf ready >"$1"; while :; do :; done`, "fixture", marker)
	s := defaultProcessGroupSupervisor()
	s.signalGroup = func(_ int, _ syscall.Signal) error {
		return errors.Join(cmd.Process.Kill(), cleanupErr)
	}
	s.waitAbsent = func(_ int, _ time.Duration) (bool, error) { return true, nil }
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
		!errors.Is(result.cause, cause) || result.runErr == nil || cmd.ProcessState == nil {
		t.Fatalf("run result = %+v; process state = %v", result, cmd.ProcessState)
	}

	preCtx, preCancel := context.WithCancelCause(context.Background())
	preCancel(cause)
	preResult := defaultProcessGroupSupervisor().run(preCtx, exec.CommandContext(preCtx, "/usr/bin/true"))
	if !preResult.preStartCanceled || preResult.cleanupRan || !errors.Is(preResult.cause, cause) {
		t.Fatalf("pre-start run result = %+v", preResult)
	}
}

func TestProcessGroupSupervisorRejectsPresetGroupBeforeStart(t *testing.T) {
	for _, tc := range []struct {
		name string
		attr syscall.SysProcAttr
	}{
		{name: "selected group", attr: syscall.SysProcAttr{Pgid: 42}},
		{name: "foreground group", attr: syscall.SysProcAttr{Foreground: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "started")
			cmd := exec.Command("/bin/sh", "-c", `: >"$1"`, "fixture", marker)
			cmd.SysProcAttr = &tc.attr
			result := defaultProcessGroupSupervisor().run(context.Background(), cmd)
			if result.runErr == nil || !strings.Contains(result.runErr.Error(), "must not select") {
				t.Fatalf("run result = %+v", result)
			}
			if cmd.Process != nil {
				t.Fatalf("rejected command started process %d", cmd.Process.Pid)
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("start marker exists: %v", err)
			}
		})
	}
}

func TestProcessGroupSupervisorContinuesCleanupAfterErrors(t *testing.T) {
	cause := errors.New("cancel fixture")
	signalErr := errors.New("TERM fixture")
	pollErr := errors.New("poll fixture")
	killErr := errors.New("KILL fixture")
	fallbackErr := errors.New("leader fallback fixture")
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	marker := filepath.Join(t.TempDir(), "process")
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `trap '' TERM; printf '%s %s\n' "$$" "$(ps -o pgid= -p $$ | tr -d ' ')" >"$1"; while :; do :; done`, "fixture", marker)
	s := defaultProcessGroupSupervisor()
	s.termGrace = 100 * time.Millisecond
	s.proofLimit = 100 * time.Millisecond
	s.waitDelay = 100 * time.Millisecond
	s.signalGroup = func(groupID int, signal syscall.Signal) error {
		actual, err := syscall.Getpgid(groupID)
		if err != nil || actual != groupID || validateSafeProcessGroupID(groupID) != nil {
			return fmt.Errorf("unsafe fixture group %d (actual %d): %w", groupID, actual, err)
		}
		if signal == syscall.SIGTERM {
			return signalErr
		}
		return killErr
	}
	waitCall := 0
	s.waitAbsent = func(_ int, _ time.Duration) (bool, error) {
		waitCall++
		if waitCall == 1 {
			return false, pollErr
		}
		return false, nil
	}
	s.killProcess = func(*os.Process) error { return fallbackErr }
	go func() {
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if fields := pollProcessMarker(marker, 25*time.Millisecond, 2); len(fields) == 2 {
				cancel(cause)
				return
			}
		}
		cancel(cause)
	}()
	results := make(chan processGroupRunResult, 1)
	go func() { results <- s.run(ctx, cmd) }()
	var result processGroupRunResult
	select {
	case result = <-results:
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		<-results
		t.Fatal("cleanup did not return before its bounded fallback")
	}
	if !result.cleanupRan || !errors.Is(result.cleanupErr, signalErr) || !errors.Is(result.cleanupErr, pollErr) ||
		!errors.Is(result.cleanupErr, killErr) || !errors.Is(result.cleanupErr, fallbackErr) ||
		!errors.Is(result.cause, cause) || result.runErr == nil {
		t.Fatalf("run result = %+v", result)
	}
	absent, err := waitForProcessGroupExit(cmd.Process.Pid, time.Second)
	if err != nil || !absent {
		t.Fatalf("fixture group %d remains: absent=%v err=%v", cmd.Process.Pid, absent, err)
	}
}

func TestOwnedProcessGroupSurvivesLeaderExit(t *testing.T) {
	dir := t.TempDir()
	marker, release := filepath.Join(dir, "processes"), filepath.Join(dir, "release")
	cmd := exec.Command("/bin/sh", "-c", `
while [ ! -f "$WORKCELL_TEST_OUTER_GROUP_READY" ]; do sleep .01; done
sh -c 'trap "" TERM; while :; do sleep 1; done' fixture-child "$1" & child=$!
pgid=$(ps -o pgid= -p $$ | tr -d ' ')
printf '%s %s %s\n' "$pgid" "$$" "$child" >"$1.tmp"; mv "$1.tmp" "$1"
while [ ! -f "$2" ]; do sleep .01; done
`, "fixture", marker, release)
	fixture := startOwnedFixtureCommand(t, cmd)
	if fields := pollProcessMarker(marker, 5*time.Second, 3); len(fields) != 3 {
		t.Fatal("process marker was not ready")
	}
	original, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("malformed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateOwnedFixtureProcessGroupMarker(fixture.group, marker); err == nil {
		t.Fatal("malformed behavior marker passed validation")
	}
	if err := os.WriteFile(marker, original, 0o600); err != nil {
		t.Fatal(err)
	}
	group := fixture.group
	if err := validateOwnedFixtureProcessGroupMarker(group, marker); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(release, []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fixture.wait(); err != nil {
		t.Fatal(err)
	}
	groupID, absent, err := ownedProcessGroupForProcess(cmd.Process)
	if err != nil || absent || int(groupID) != group.id {
		t.Fatalf("ownedProcessGroupForProcess() = %d, %v, %v", groupID, absent, err)
	}
	if err := signalProcessGroup(group.id, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	group.proveAbsent(t)
	_, absent, err = ownedProcessGroupForProcess(cmd.Process)
	if err != nil || !absent {
		t.Fatalf("absent group probe = %v, %v", absent, err)
	}
}

func TestFixtureOwnerHandshakeFailsClosed(t *testing.T) {
	if os.Getenv("WORKCELL_OWNER_HANDSHAKE_HELPER") == "1" {
		waitForOwnedFixtureCommandReady()
		cmd := exec.Command(os.Getenv("WORKCELL_OWNER_HANDSHAKE_BIN"))
		cmd.Stdout = os.Stdout
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			os.Exit(125)
		}
		_ = cmd.Wait()
		os.Exit(0)
	}
	for _, tc := range []struct {
		name   string
		output string
	}{
		{"missing", ""},
		{"malformed", "printf 'bad owner record\\n'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			fake := writeFakeColima(t, dir, "#!/bin/sh\n"+tc.output+`
count=0
while [ ! -f "$WORKCELL_OWNER_HANDSHAKE_ACK" ] && [ "$count" -lt 100 ]; do sleep .01; count=$((count + 1)); done
exit 125
`)
			cmd := exec.Command(os.Args[0], "-test.run=^TestFixtureOwnerHandshakeFailsClosed$")
			ownerPipe, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Env = append(os.Environ(),
				"WORKCELL_OWNER_HANDSHAKE_HELPER=1",
				"WORKCELL_OWNER_HANDSHAKE_BIN="+fake,
				"WORKCELL_OWNER_HANDSHAKE_ACK="+filepath.Join(dir, "not-created"),
			)
			fixture := startOwnedFixtureCommand(t, cmd)
			if group, err := readOwnedFixtureProcessGroupPipe(ownerPipe, cmd.Process.Pid, fake); err == nil || group != nil {
				t.Fatalf("invalid owner handshake = %v, %v", group, err)
			}
			if err := fixture.wait(); err != nil {
				t.Fatal(err)
			}
			fixture.group.proveAbsent(t)
			if !waitForFixtureCommandPathAbsent(fake, 5*time.Second) {
				t.Fatalf("fixture command remains: %s", fake)
			}
		})
	}
}

func waitForFixtureCommandPathAbsent(path string, limit time.Duration) bool {
	for deadline := time.Now().Add(limit); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
		output, err := exec.Command("/bin/ps", "-axo", "command=").Output()
		if err == nil && !strings.Contains(string(output), path) {
			return true
		}
	}
	return false
}

func TestProcessGroupSupervisorTerminate(t *testing.T) {
	sentinel := errors.New("fixture failure")
	for _, tc := range []struct {
		name         string
		waits        []bool
		signalFail   int
		waitFail     int
		wantSignal   []syscall.Signal
		wantFallback int
		wantErr      error
		wantText     string
	}{
		{name: "TERM is sufficient", waits: []bool{true}, wantSignal: []syscall.Signal{syscall.SIGTERM}},
		{name: "escalates to KILL", waits: []bool{false, true}, wantSignal: []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}},
		{name: "TERM fails", waits: []bool{false, true}, signalFail: 1, wantSignal: []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}, wantErr: sentinel},
		{name: "TERM poll fails", waits: []bool{false, true}, waitFail: 1, wantSignal: []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}, wantErr: sentinel},
		{name: "KILL fails", waits: []bool{false, true}, signalFail: 2, wantSignal: []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}, wantFallback: 1, wantErr: sentinel},
		{name: "KILL poll fails", waits: []bool{false}, waitFail: 2, wantSignal: []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}, wantFallback: 1, wantErr: sentinel},
		{name: "group survives KILL", waits: []bool{false, false}, wantSignal: []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}, wantFallback: 1, wantText: "remains after SIGKILL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var signals []syscall.Signal
			waitCall := 0
			fallbacks := 0
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
				killProcess: func(*os.Process) error { fallbacks++; return nil },
				termGrace:   time.Second,
				proofLimit:  time.Second,
			}
			err := s.terminate(ownedProcessGroupID(4242), &os.Process{Pid: 4242})
			if !reflect.DeepEqual(signals, tc.wantSignal) {
				t.Fatalf("signals = %v, want %v", signals, tc.wantSignal)
			}
			if fallbacks != tc.wantFallback {
				t.Fatalf("leader fallbacks = %d, want %d", fallbacks, tc.wantFallback)
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

func TestProcessGroupHelpersRejectUnsafeIDs(t *testing.T) {
	for _, groupID := range []int{-1, 0, 1, syscall.Getpgrp()} {
		killCalled := false
		recordKill := func(int, syscall.Signal) error { killCalled = true; return nil }
		err := signalProcessGroupWithKill(groupID, syscall.SIGTERM, recordKill)
		if err == nil || killCalled {
			t.Fatalf("signalProcessGroupWithKill(%d) = %v; kill called=%v", groupID, err, killCalled)
		}
		probeCalled := false
		recordProbe := func(int, syscall.Signal) error { probeCalled = true; return nil }
		absent, err := waitForProcessGroupExitWithProbe(groupID, 0, recordProbe, time.Now, time.Sleep)
		if err == nil || absent || probeCalled {
			t.Fatalf("waitForProcessGroupExitWithProbe(%d) = %v, %v; probe called=%v", groupID, absent, err, probeCalled)
		}
	}
	group := ownedFixtureProcessGroup{id: 42, members: map[int]string{42: "original"}}
	if group.hasLiveMemberWith(func(int) (int, string, error) { return 42, "reused", nil }) {
		t.Fatal("stale process generation was accepted")
	}
	if !group.hasLiveMemberWith(func(int) (int, string, error) { return 42, "original", nil }) {
		t.Fatal("matching process generation was rejected")
	}
}

func TestWaitForProcessGroupExitTreatsEPERMAsPresent(t *testing.T) {
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
