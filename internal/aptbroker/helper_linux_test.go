// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build linux

package aptbroker

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStopOwnedHelperDrainsBeforeSingleWait(t *testing.T) {
	var log []string
	system := recordingHelperSystem(&log)
	watcher := closedHelperWatcher(nil)
	err := stopOwnedHelper(&exec.Cmd{}, helperOwner{pid: 42}, drainingHelperOutput(&log), watcher, nil, system)
	if !errors.Is(err, errHelperOutputDrain) {
		t.Fatalf("forced-drain cleanup error = %v, want %v", err, errHelperOutputDrain)
	}
	want := []string{"group", "signal killed", "exact kill", "watch", "drain", "drain", "wait"}
	if !slices.Equal(log, want) {
		t.Fatalf("forced-drain cleanup order = %q, want %q", log, want)
	}
}

func TestStopOwnedHelperCancellationUsesTermGraceThenKill(t *testing.T) {
	var log []string
	system := recordingHelperSystem(&log)
	watcher := &helperWatcher{done: make(chan struct{})}
	err := stopOwnedHelper(&exec.Cmd{}, helperOwner{pid: 42}, closedHelperOutput(), watcher, context.Canceled, system)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"group", "signal terminated", "watch", "group", "signal killed", "exact kill", "watch", "wait"}
	if !slices.Equal(log, want) {
		t.Fatalf("canceled cleanup order = %q, want %q", log, want)
	}
}

func TestStopOwnedHelperNeverSignalsUnprovedGroup(t *testing.T) {
	var log []string
	system := recordingHelperSystem(&log)
	system.processGroupID = func(int) (int, error) { log = append(log, "group rejected"); return 0, errors.New("unproved") }
	err := stopOwnedHelper(&exec.Cmd{}, helperOwner{pid: 42}, closedHelperOutput(), nil, context.Canceled, system)
	if err == nil {
		t.Fatal("unproved cleanup succeeded")
	}
	want := []string{"group rejected", "watch", "group rejected", "exact kill", "watch", "wait"}
	if !slices.Equal(log, want) {
		t.Fatalf("unproved cleanup order = %q, want %q", log, want)
	}
}

func TestStopOwnedHelperRevalidatesBeforeKill(t *testing.T) {
	var log []string
	system := recordingHelperSystem(&log)
	checks := 0
	system.processGroupID = func(pid int) (int, error) {
		checks++
		log = append(log, "group")
		if checks == 2 {
			return 0, errors.New("ownership changed")
		}
		return pid, nil
	}
	err := stopOwnedHelper(&exec.Cmd{}, helperOwner{pid: 42}, closedHelperOutput(), nil, context.Canceled, system)
	if err == nil {
		t.Fatal("ownership change was ignored")
	}
	want := []string{"group", "signal terminated", "watch", "group", "exact kill", "watch", "wait"}
	if !slices.Equal(log, want) {
		t.Fatalf("ownership-change cleanup order = %q, want %q", log, want)
	}
}

func TestStopOwnedHelperPropagatesCleanupFailure(t *testing.T) {
	var log []string
	system := recordingHelperSystem(&log)
	wantErr := errors.New("signal failed")
	system.signalGroup = func(_ int, signal syscall.Signal) error {
		log = append(log, "signal "+signal.String())
		return wantErr
	}
	err := stopOwnedHelper(&exec.Cmd{}, helperOwner{pid: 42}, closedHelperOutput(), nil, nil, system)
	if !errors.Is(err, wantErr) {
		t.Fatalf("cleanup error = %v, want %v", err, wantErr)
	}
}

func TestSuperviseHelperStartFailuresCleanSafely(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*helperSystem, *[]string)
		want      []string
	}{
		{"ownership proof", func(system *helperSystem, log *[]string) {
			system.processGroupID = func(int) (int, error) { *log = append(*log, "group rejected"); return 0, errors.New("unproved") }
		}, []string{"group rejected", "exact kill", "wait"}},
		{"watcher", func(system *helperSystem, log *[]string) {
			system.openWatcher = func(int) (*helperWatcher, error) {
				*log = append(*log, "watch open failed")
				return nil, errors.New("watch failed")
			}
		}, []string{"group", "watch open failed", "group", "signal terminated", "watch", "group", "signal killed", "exact kill", "watch", "wait"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var log []string
			system := recordingHelperSystem(&log)
			test.configure(&system, &log)
			command := &exec.Cmd{Process: &os.Process{Pid: 42}}
			if _, err := superviseHelperStart(command, closedHelperOutput(), system); err == nil {
				t.Fatal("startup failure was ignored")
			}
			if !slices.Equal(log, test.want) {
				t.Fatalf("startup cleanup order = %q, want %q", log, test.want)
			}
		})
	}
}

func TestRunOwnedHelperPublishesOwnershipBeforeCancellationCleanup(t *testing.T) {
	server, client := unixConnectionPair(t)
	defer server.Close()
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	var log []string
	system := recordingHelperSystem(&log)
	system.start = func(command *exec.Cmd) error {
		log = append(log, "start")
		command.Process = &os.Process{Pid: 42}
		return nil
	}
	system.openWatcher = func(int) (*helperWatcher, error) {
		log = append(log, "watch open")
		cancel()
		return &helperWatcher{fd: -1, done: make(chan struct{})}, nil
	}
	system.wait = func(*exec.Cmd) error {
		log = append(log, "wait")
		return errors.New("fixture wait")
	}
	_, err := runOwnedHelperWithSystem(ctx, server, Request{}, testHelperConfig("/fixture", time.Second), system)
	if err == nil {
		t.Fatal("canceled helper succeeded")
	}
	want := []string{"start", "group", "watch open", "group", "signal terminated", "watch", "group", "signal killed", "exact kill", "watch", "wait"}
	if !slices.Equal(log, want) {
		t.Fatalf("startup cancellation order = %q, want %q", log, want)
	}
}

func TestWatchDisconnectReportsClosure(t *testing.T) {
	server, client := unixConnectionPair(t)
	defer server.Close()
	ctx, cancel := context.WithCancelCause(context.Background())
	go watchDisconnect(ctx, server, cancel)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
		if context.Cause(ctx) == nil {
			t.Fatal("disconnect has no cause")
		}
	case <-time.After(time.Second):
		t.Fatal("disconnect was not reported")
	}
}

func TestHelperOutputCloseBoundsRetainedReaders(t *testing.T) {
	stdoutReader, stdoutWriter := io.Pipe()
	stderrReader, stderrWriter := io.Pipe()
	defer stdoutWriter.Close()
	defer stderrWriter.Close()
	output := &helperOutput{
		stdout: newLimitedBuffer(), stderr: newLimitedBuffer(),
		readers: []io.ReadCloser{stdoutReader, stderrReader},
		done:    make(chan struct{}), overflow: make(chan struct{}, 1),
	}
	go output.copy()
	finished := make(chan error, 1)
	go func() {
		finished <- output.finish(10 * time.Millisecond)
	}()
	select {
	case err := <-finished:
		if !errors.Is(err, errHelperOutputDrain) {
			t.Fatalf("retained output drain error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("retained output readers did not close within the test bound")
	}
}

func TestHelperOutputFinishPreservesBufferedData(t *testing.T) {
	output := closedHelperOutput()
	if _, err := output.stdout.Write([]byte("complete output")); err != nil {
		t.Fatal(err)
	}
	if err := output.finish(time.Second); err != nil {
		t.Fatal(err)
	}
	if got := string(output.stdout.bytes()); got != "complete output" {
		t.Fatalf("finished output = %q", got)
	}
}

func TestHelperResponsePreservesCommittedStatusAndReportsCleanup(t *testing.T) {
	command := exec.Command("/bin/sh", "-c", "exit 9")
	if err := command.Run(); err == nil {
		t.Fatal("fixture command unexpectedly succeeded")
	}
	wantCleanup := errors.New("drain cleanup failed")
	var reported error
	response, err := helperResponse(command, closedHelperOutput(), errHelperTimeout, wantCleanup, func(report error) {
		reported = report
	})
	if err != nil || response.Status != 124 || !errors.Is(reported, wantCleanup) {
		t.Fatalf("helperResponse() = (%#v, %v), report=%v", response, err, reported)
	}
}

func TestHelperResponsePreservesCleanupPrecedenceOverLatentOverflow(t *testing.T) {
	output := closedHelperOutput()
	output.stdout.overflow.Store(true)
	wantCleanup := errors.New("cleanup failed")
	reported := false
	_, err := helperResponse(&exec.Cmd{}, output, nil, wantCleanup, func(error) { reported = true })
	if !errors.Is(err, wantCleanup) || reported {
		t.Fatalf("latent overflow compound result = (%v, reported=%t)", err, reported)
	}
}

func recordingHelperSystem(log *[]string) helperSystem {
	return helperSystem{
		start:          func(*exec.Cmd) error { *log = append(*log, "start"); return nil },
		processGroupID: func(pid int) (int, error) { *log = append(*log, "group"); return pid, nil },
		openWatcher: func(int) (*helperWatcher, error) {
			*log = append(*log, "watch open")
			return closedHelperWatcher(nil), nil
		},
		signalGroup: func(_ int, signal syscall.Signal) error { *log = append(*log, "signal "+signal.String()); return nil },
		killExact:   func(*exec.Cmd) error { *log = append(*log, "exact kill"); return nil },
		wait:        func(*exec.Cmd) error { *log = append(*log, "wait"); return nil },
		waitWatcher: func(*helperWatcher, time.Duration) { *log = append(*log, "watch") },
		afterFunc:   context.AfterFunc,
		drainLimit:  time.Millisecond,
	}
}

func closedHelperWatcher(err error) *helperWatcher {
	watcher := &helperWatcher{done: make(chan struct{}), err: err}
	close(watcher.done)
	return watcher
}

func closedHelperOutput() *helperOutput {
	done := make(chan struct{})
	close(done)
	return &helperOutput{stdout: newLimitedBuffer(), stderr: newLimitedBuffer(), done: done}
}

type blockingOutputReader struct {
	closed chan struct{}
	log    *[]string
}

func (reader *blockingOutputReader) Read([]byte) (int, error) {
	<-reader.closed
	return 0, io.EOF
}

func (reader *blockingOutputReader) Close() error {
	*reader.log = append(*reader.log, "drain")
	close(reader.closed)
	return nil
}

func drainingHelperOutput(log *[]string) *helperOutput {
	output := &helperOutput{
		stdout: newLimitedBuffer(), stderr: newLimitedBuffer(),
		readers: []io.ReadCloser{
			&blockingOutputReader{closed: make(chan struct{}), log: log},
			&blockingOutputReader{closed: make(chan struct{}), log: log},
		},
		done: make(chan struct{}), overflow: make(chan struct{}, 1),
	}
	go output.copy()
	return output
}

func TestCommittedHelperCausePreservesWinner(t *testing.T) {
	canceled, cancel := context.WithCancelCause(context.Background())
	cancel(errors.New("canceled first"))
	exited := &helperWatcher{done: make(chan struct{})}
	close(exited.done)
	cancelFirst := func(ctx context.Context, callback func()) func() bool {
		callback()
		return func() bool { return false }
	}
	if err := committedHelperCause(canceled, exited, cancelFirst); err == nil || err.Error() != "canceled first" {
		t.Fatalf("committed cancellation = %v", err)
	}

	exitFirst := func(context.Context, func()) func() bool { return func() bool { return true } }
	if err := committedHelperCause(context.Background(), exited, exitFirst); err != nil {
		t.Fatalf("committed exit = %v", err)
	}
	wantWatchErr := errors.New("watch failed")
	exited.err = wantWatchErr
	if err := committedHelperCause(context.Background(), exited, exitFirst); !errors.Is(err, wantWatchErr) {
		t.Fatalf("committed watch failure = %v", err)
	}
}

func TestRunOwnedHelperPreservesStatusOutputAndEnvironment(t *testing.T) {
	helper := writeHelper(t, "printf '%s' \"$DEBIAN_FRONTEND\"\nprintf error >&2\nexit 37\n")
	server, client := unixConnectionPair(t)
	defer server.Close()
	defer client.Close()
	response, err := runOwnedHelper(context.Background(), server, Request{
		Args: []string{"apt-get"}, Env: map[string]string{"DEBIAN_FRONTEND": "noninteractive"},
	}, testHelperConfig(helper, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != 37 || string(response.Stdout) != "noninteractive" || !strings.HasSuffix(string(response.Stderr), "error") {
		t.Fatalf("response = %#v", response)
	}
}

func TestRunOwnedHelperDoesNotStartAfterCancellation(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	helper := writeHelper(t, "touch \"$1\"\n")
	server, client := unixConnectionPair(t)
	defer server.Close()
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := runOwnedHelper(ctx, server, Request{Args: []string{marker}}, testHelperConfig(helper, time.Second))
	if err == nil {
		t.Fatal("pre-canceled helper run succeeded")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("pre-canceled helper started: %v", err)
	}
}

func TestRunOwnedHelperBoundsOutput(t *testing.T) {
	helper := writeHelper(t, "head -c 1048577 /dev/zero\n")
	server, client := unixConnectionPair(t)
	defer server.Close()
	defer client.Close()
	response, err := runOwnedHelper(context.Background(), server, Request{Args: []string{"apt-get"}}, testHelperConfig(helper, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != 1 || len(response.Stdout) != MaxOutputBytes {
		t.Fatalf("overflow response status=%d stdout=%d", response.Status, len(response.Stdout))
	}
}

func TestRunOwnedHelperReturnsTimeoutStatus(t *testing.T) {
	helper := writeHelper(t, "sleep 1\n")
	server, client := unixConnectionPair(t)
	defer server.Close()
	defer client.Close()
	response, err := runOwnedHelper(context.Background(), server, Request{Args: []string{"apt-get"}}, testHelperConfig(helper, 10*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != 124 {
		t.Fatalf("timeout status = %d, want 124", response.Status)
	}
}

func unixConnectionPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(t.TempDir(), "socket"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.DialUnix("unix", nil, listener.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	server, err := listener.AcceptUnix()
	if err != nil {
		client.Close()
		t.Fatal(err)
	}
	return server, client
}

func writeHelper(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func testHelperConfig(path string, timeout time.Duration) helperConfig {
	return helperConfig{path: path, timeout: timeout, report: func(error) {}}
}
