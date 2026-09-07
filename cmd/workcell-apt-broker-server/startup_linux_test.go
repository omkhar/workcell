// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build linux

package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"
)

type fixtureStartupProcess struct {
	log *[]string
}

func (p fixtureStartupProcess) Kill() error {
	*p.log = append(*p.log, "kill")
	return nil
}

func (p fixtureStartupProcess) Wait() (*os.ProcessState, error) {
	*p.log = append(*p.log, "wait")
	return nil, nil
}

func (p fixtureStartupProcess) Release() error {
	*p.log = append(*p.log, "release")
	return nil
}

type fixtureStartupReader struct {
	io.Reader
	deadlineErr error
}

type fixtureErrorReader struct {
	err error
}

func (r fixtureErrorReader) Read([]byte) (int, error) {
	return 0, r.err
}

func (r fixtureStartupReader) SetReadDeadline(time.Time) error {
	return r.deadlineErr
}

type fixtureStartupWriter struct {
	log *[]string
	err error
}

func (w fixtureStartupWriter) Write(body []byte) (int, error) {
	*w.log = append(*w.log, "ack")
	if w.err != nil {
		return 0, w.err
	}
	return len(body), nil
}

func TestStartPeerUIDRequiresExactArgument(t *testing.T) {
	for _, arguments := range [][]string{nil, {"--peer-uid"}, {"--peer-uid", "0"}, {"--socket", "1000"}, {"--peer-uid", "1000", "extra"}} {
		if _, err := startPeerUID(arguments); err == nil {
			t.Fatalf("startPeerUID(%q) succeeded", arguments)
		}
	}
	if uid, err := startPeerUID([]string{"--peer-uid", "1000"}); err != nil || uid != 1000 {
		t.Fatalf("startPeerUID() = (%d, %v)", uid, err)
	}
}

func TestStartupHandshakeRequiresAcknowledgement(t *testing.T) {
	readyReader, readyWriter := testPipe(t)
	defer readyReader.Close()
	ackReader, ackWriter := testPipe(t)
	defer ackWriter.Close()
	handshake, err := startupHandshake(readyWriter.Fd(), ackReader.Fd())
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- handshake() }()
	ready, err := io.ReadAll(readyReader)
	if err != nil || !reflect.DeepEqual(ready, []byte{startupReadyByte}) {
		t.Fatalf("readiness = (%q, %v)", ready, err)
	}
	select {
	case err := <-finished:
		t.Fatalf("handshake completed before acknowledgement: %v", err)
	default:
	}
	if _, err := ackWriter.Write([]byte{startupAcknowledge}); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func testPipe(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	return reader, writer
}

func TestValidateServerBinaryRejectsWritableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server")
	if err := os.WriteFile(path, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := validateServerBinary(path); err == nil {
		t.Fatal("writable server binary was accepted")
	}
}

func TestFixedServerEnvironmentIsMinimal(t *testing.T) {
	t.Setenv("WORKCELL_APT_BROKER_HELPER_TIMEOUT_SECONDS", "7")
	want := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"LC_ALL=C",
		"LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so",
		"WORKCELL_APT_BROKER_HELPER_TIMEOUT_SECONDS=7",
	}
	if got := fixedServerEnvironment(); !reflect.DeepEqual(got, want) {
		t.Fatalf("server environment = %q", got)
	}
}

func TestFinishStartupTransfersOnlyValidatedServer(t *testing.T) {
	fixtureErr := errors.New("fixture failure")
	for _, test := range []struct {
		name        string
		ready       startupReader
		validateErr error
		ackErr      error
		wantLog     []string
		wantError   bool
	}{
		{name: "success", ready: startupBody("R"), wantLog: []string{"validate", "ack", "release"}},
		{name: "empty readiness", ready: startupBody(""), wantLog: []string{"kill", "wait"}, wantError: true},
		{name: "duplicate readiness", ready: startupBody("RR"), wantLog: []string{"kill", "wait"}, wantError: true},
		{name: "deadline failure", ready: fixtureStartupReader{Reader: bytes.NewReader(nil), deadlineErr: fixtureErr}, wantLog: []string{"kill", "wait"}, wantError: true},
		{name: "read timeout", ready: fixtureStartupReader{Reader: fixtureErrorReader{err: fixtureErr}}, wantLog: []string{"kill", "wait"}, wantError: true},
		{name: "socket metadata", ready: startupBody("R"), validateErr: fixtureErr, wantLog: []string{"validate", "kill", "wait"}, wantError: true},
		{name: "acknowledgement", ready: startupBody("R"), ackErr: fixtureErr, wantLog: []string{"validate", "ack", "kill", "wait"}, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var log []string
			validate := func() error {
				log = append(log, "validate")
				return test.validateErr
			}
			writer := fixtureStartupWriter{log: &log, err: test.ackErr}
			err := finishStartup(fixtureStartupProcess{log: &log}, test.ready, writer, validate)
			if !reflect.DeepEqual(log, test.wantLog) || (err != nil) != test.wantError {
				t.Fatalf("finishStartup() = (%v, %v), want (%v, error=%v)", log, err, test.wantLog, test.wantError)
			}
		})
	}
}

func startupBody(body string) startupReader {
	return fixtureStartupReader{Reader: bytes.NewBufferString(body)}
}

func TestPrepareSocketParentRejectsSymlinkWithoutChangingTarget(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("root-owned directory fixture requires root")
	}
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "socket-root")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := prepareSocketParent(path); err == nil {
		t.Fatal("symlink socket parent was accepted")
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("symlink target changed: (%v, %v)", info, err)
	}
}

func TestCreateSocketParentNormalizesRestrictiveUmask(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("root ownership check requires root")
	}
	path := filepath.Join(t.TempDir(), "socket-root")
	oldMask := syscall.Umask(0o077)
	defer syscall.Umask(oldMask)
	if err := createSocketParent(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("created socket parent = (%v, %v)", info, err)
	}
}

func TestCreateSocketParentDoesNotModifyExistingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "socket-root")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := createSocketParent(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("existing socket parent changed: (%v, %v)", info, err)
	}
}

func TestRequireUnusedSocketRejectsEveryExistingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "socket")
	if err := requireUnusedSocket(path); err != nil {
		t.Fatalf("missing socket was rejected: %v", err)
	}
	if err := os.WriteFile(path, []byte("stale fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := requireUnusedSocket(path); err == nil {
		t.Fatal("existing socket path was accepted")
	}
}
