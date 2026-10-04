// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package credbroker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const testToken = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

// syncBuffer is a log writer the test can read while handlers write.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) records(t *testing.T) []logRecord {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []logRecord
	for _, line := range strings.Split(strings.TrimSpace(b.buf.String()), "\n") {
		if line == "" {
			continue
		}
		var record logRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		out = append(out, record)
	}
	return out
}

// socketDir is short enough for the 104-byte macOS socket path limit.
func socketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "wcb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func testServer(t *testing.T, creds map[string]string) (*server, *syncBuffer) {
	t.Helper()
	log := &syncBuffer{}
	s, err := newServer(Config{Token: []byte(testToken), Credentials: creds, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	return s, log
}

// listenServer accepts connections for s on a fresh socket.
func listenServer(t *testing.T, s *server) string {
	t.Helper()
	path := filepath.Join(socketDir(t), "b.sock")
	listener, err := listen(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeSocket(path, listener) })
	go func() {
		for {
			connection, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			go s.serve(connection)
		}
	}()
	return path
}

func TestLookupReleasesOnlyGrantedPairs(t *testing.T) {
	s, log := testServer(t, map[string]string{"claude_api_key": "sk-claude", "gemini_env": "g-key"})
	path := listenServer(t, s)
	ctx := context.Background()
	if v, err := Lookup(ctx, path, testToken, "api.anthropic.com", "x-api-key"); err != nil || v != "sk-claude" {
		t.Fatalf("claude lookup = %q, %v", v, err)
	}
	if v, err := Lookup(ctx, path, testToken, "api.anthropic.com", "authorization"); err != nil || v != "sk-claude" {
		t.Fatalf("claude authorization lookup = %q, %v, want bare value", v, err)
	}
	if v, err := Lookup(ctx, path, testToken, "generativelanguage.googleapis.com", "x-goog-api-key"); err != nil || v != "g-key" {
		t.Fatalf("gemini lookup = %q, %v", v, err)
	}
	for _, pair := range [][2]string{
		{"api.anthropic.com", "x-goog-api-key"},
		{"generativelanguage.googleapis.com", "x-api-key"},
		{"generativelanguage.googleapis.com", "authorization"},
		{"api.anthropic.com", "Authorization"},
		{"evil.example", "x-api-key"},
		{"API.anthropic.com", "x-api-key"},
	} {
		if _, err := Lookup(ctx, path, testToken, pair[0], pair[1]); err != DenyError(ErrNotGranted) {
			t.Fatalf("lookup %v = %v, want not_granted", pair, err)
		}
	}
	if _, err := Lookup(ctx, path, strings.Repeat("0", 64), "api.anthropic.com", "x-api-key"); err != DenyError(ErrBadToken) {
		t.Fatalf("bad token lookup = %v, want bad_token", err)
	}
	if _, err := Lookup(ctx, path, "", "api.anthropic.com", "x-api-key"); err != DenyError(ErrBadToken) {
		t.Fatalf("empty token lookup = %v, want bad_token", err)
	}
	records := log.records(t)
	results := make([]string, 0, len(records))
	for _, record := range records {
		results = append(results, record.Result)
	}
	want := "granted granted granted not_granted not_granted not_granted not_granted not_granted not_granted bad_token bad_token"
	if got := strings.Join(results, " "); got != want {
		t.Fatalf("log results = %q, want %q", got, want)
	}
	if records[0].Credential != "claude_api_key" || records[0].Host != "api.anthropic.com" {
		t.Fatalf("first record = %+v", records[0])
	}
	raw, _ := json.Marshal(records)
	if bytes.Contains(raw, []byte("sk-claude")) || bytes.Contains(raw, []byte("g-key")) || bytes.Contains(raw, []byte(testToken)) {
		t.Fatalf("log leaks a secret: %s", raw)
	}
}

func TestPeerWithAnotherUIDGetsNoAnswer(t *testing.T) {
	s, log := testServer(t, map[string]string{"claude_api_key": "sk-claude"})
	s.uid++
	path := listenServer(t, s)
	if _, err := Lookup(context.Background(), path, testToken, "api.anthropic.com", "x-api-key"); !errors.Is(err, io.EOF) && !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("lookup from another uid = %v, want EOF or reset", err)
	}
	if records := log.records(t); len(records) != 1 || records[0].Result != "peer_rejected" {
		t.Fatalf("records = %+v, want one peer_rejected", records)
	}
}

func TestMalformedRequestIsLoggedAndDropped(t *testing.T) {
	s, log := testServer(t, map[string]string{"claude_api_key": "sk-claude"})
	for _, line := range []string{`{"token":"x","extra":1}` + "\n", `not json` + "\n", `{"token":"x"}`} {
		if _, err := readRequest(strings.NewReader(line)); err == nil {
			t.Fatalf("readRequest(%q) accepted", line)
		}
	}
	path := listenServer(t, s)
	connection, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.Write([]byte("not json\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("read after malformed request = %v, want EOF", err)
	}
	if records := log.records(t); len(records) != 1 || records[0].Result != "malformed" {
		t.Fatalf("records = %+v, want one malformed", records)
	}
}

func TestRateLimitIsAFixedWindowForValidTokens(t *testing.T) {
	s, _ := testServer(t, map[string]string{"claude_api_key": "sk-claude"})
	clock := time.Unix(1000, 0)
	s.now = func() time.Time { return clock }
	good := Request{Token: testToken, Host: "api.anthropic.com", Header: "x-api-key"}
	for i := 0; i < rateLimit; i++ {
		if response, _ := s.answer(good); response.Error != "" {
			t.Fatalf("request %d refused: %s", i, response.Error)
		}
	}
	if response, _ := s.answer(Request{Token: "wrong"}); response.Error != ErrBadToken {
		t.Fatalf("bad token over the limit = %+v, want bad_token", response)
	}
	if response, _ := s.answer(good); response.Error != ErrRateLimited {
		t.Fatalf("request over the limit = %+v, want rate_limited", response)
	}
	clock = clock.Add(rateWindow - time.Nanosecond)
	if response, _ := s.answer(good); response.Error != ErrRateLimited {
		t.Fatalf("request inside the window = %+v, want rate_limited", response)
	}
	clock = clock.Add(time.Nanosecond)
	if response, key := s.answer(good); response.Value != "sk-claude" || key != "claude_api_key" {
		t.Fatalf("request in the next window = %+v %q", response, key)
	}
}

func TestGrantFiltering(t *testing.T) {
	s, _ := testServer(t, map[string]string{"claude_api_key": "sk-claude"})
	if response, _ := s.answer(Request{Token: testToken, Host: "generativelanguage.googleapis.com", Header: "x-goog-api-key"}); response.Error != ErrNotGranted {
		t.Fatalf("unconfigured gemini = %+v, want not_granted", response)
	}
	if _, err := newServer(Config{Token: []byte(testToken), Credentials: map[string]string{"codex_auth": "x"}, Log: io.Discard}); err == nil {
		t.Fatal("newServer accepted a credential with no grant")
	}
	if _, err := newServer(Config{Credentials: map[string]string{}, Log: io.Discard}); err == nil {
		t.Fatal("newServer accepted an empty token")
	}
}

func TestListenRefusesSharedDirectoryAndExistingPath(t *testing.T) {
	dir := socketDir(t)
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := listen(filepath.Join(dir, "b.sock")); err == nil {
		t.Fatal("listen accepted a world-writable directory")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(dir, "taken")
	if err := os.WriteFile(existing, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := listen(existing); err == nil {
		t.Fatal("listen replaced an existing path")
	}
	path := filepath.Join(dir, "b.sock")
	listener, err := listen(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v, %v; want 0600", info.Mode(), err)
	}
	removeSocket(path, listener)
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket left behind: %v", err)
	}
}

func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func fakeSSH(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	writeScript(t, dir, "ssh", body)
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	return dir
}

func TestServeExitsWhenSSHForwardDies(t *testing.T) {
	dir := fakeSSH(t, `printf '%s\n' "$@" > "$(dirname "$0")/args"; exit 3`+"\n")
	path := filepath.Join(socketDir(t), "b.sock")
	err := Serve(context.Background(), Config{SocketPath: path, Token: []byte(testToken), Log: io.Discard, SSHArgs: []string{"-N", "colima-x"}})
	if err == nil || !strings.Contains(err.Error(), "ssh forward exited: exit status 3") {
		t.Fatalf("Serve = %v, want ssh forward exit", err)
	}
	if args, _ := os.ReadFile(filepath.Join(dir, "args")); string(args) != "-N\ncolima-x\n" {
		t.Fatalf("ssh args = %q", args)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket left behind: %v", err)
	}
}

func TestServeStopsSSHForwardOnCancel(t *testing.T) {
	dir := fakeSSH(t, `echo $$ > "$(dirname "$0")/pid"; exec sleep 30`+"\n")
	path := filepath.Join(socketDir(t), "b.sock")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Config{SocketPath: path, Token: []byte(testToken), Log: io.Discard, SSHArgs: []string{"colima-x"}})
	}()
	pidFile := filepath.Join(dir, "pid")
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(pidFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake ssh never started")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve after cancel = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not stop")
	}
	pid, _ := os.ReadFile(pidFile)
	if n, err := strconv.Atoi(strings.TrimSpace(string(pid))); err != nil || syscall.Kill(n, 0) == nil {
		t.Fatalf("fake ssh %q still running", pid)
	}
}

func TestListenRefusesSymlinkedAncestor(t *testing.T) {
	dir := socketDir(t)
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if listener, err := listen(filepath.Join(link, "b.sock")); err == nil {
		removeSocket(filepath.Join(real, "b.sock"), listener)
		t.Fatal("listen followed a symlinked ancestor")
	}
	if _, err := os.Lstat(filepath.Join(real, "b.sock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket created through the symlink: %v", err)
	}
}

// failingLog fails every write or every sync.
type failingLog struct{ write, sync error }

func (f failingLog) Write(p []byte) (int, error) {
	if f.write != nil {
		return 0, f.write
	}
	return len(p), nil
}

func (f failingLog) Sync() error { return f.sync }

func TestGrantIsWithheldWhenTheRecordIsNotDurable(t *testing.T) {
	creds := map[string]string{"claude_api_key": "sk-claude"}
	for name, log := range map[string]failingLog{
		"write fails": {write: errors.New("disk full")},
		"sync fails":  {sync: errors.New("io error")},
	} {
		s, err := newServer(Config{Token: []byte(testToken), Credentials: creds, Log: log})
		if err != nil {
			t.Fatal(err)
		}
		path := listenServer(t, s)
		if v, err := Lookup(context.Background(), path, testToken, "api.anthropic.com", "x-api-key"); err != DenyError(ErrLogFailed) || v != "" {
			t.Fatalf("%s: Lookup = (%q, %v), want withheld with %s", name, v, err, ErrLogFailed)
		}
	}
}

func TestLookupStopsWhenCancelledAfterConnecting(t *testing.T) {
	path := filepath.Join(socketDir(t), "b.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		time.Sleep(10 * time.Second)
	}()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	start := time.Now()
	if _, err := Lookup(ctx, path, testToken, "api.anthropic.com", "x-api-key"); err == nil {
		t.Fatal("Lookup succeeded against a silent listener")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("cancelled Lookup took %v, want a prompt return", elapsed)
	}
}
