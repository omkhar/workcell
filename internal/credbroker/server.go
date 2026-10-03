// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package credbroker serves provider API keys from host memory to the egress
// sidecar of one session. The sidecar reaches the host socket through an SSH
// reverse forward that the broker supervises. A key is released only for the
// fixed (host, header) pair that its provider uses.
package credbroker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const (
	rateLimit    = 300
	rateWindow   = time.Minute
	maxLineBytes = 4096
	ioTimeout    = 5 * time.Second
)

// Error codes a broker response can carry.
const (
	ErrBadToken    = "bad_token"
	ErrRateLimited = "rate_limited"
	ErrNotGranted  = "not_granted"
)

type grant struct{ host, header string }

// grants is the fixed table of where each credential key may be sent.
var grants = map[string]grant{
	"claude_api_key": {host: "api.anthropic.com", header: "x-api-key"},
	"gemini_env":     {host: "generativelanguage.googleapis.com", header: "x-goog-api-key"},
}

// Request is the one JSON line a client sends on a connection.
type Request struct {
	Token  string `json:"token"`
	Host   string `json:"host"`
	Header string `json:"header"`
}

// Response is the one JSON line the broker sends back.
type Response struct {
	Value string `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

// Config is one broker session.
type Config struct {
	SocketPath  string
	Token       []byte
	Credentials map[string]string // credential key -> value; keys must be in grants
	Log         io.Writer         // receives one JSON line per request; never values
	SSHArgs     []string          // argv for the supervised ssh forward; empty runs none
}

type credential struct{ key, value string }

type server struct {
	token []byte
	creds map[grant]credential
	uid   uint32
	now   func() time.Time

	mu          sync.Mutex
	log         io.Writer
	windowStart time.Time
	count       int
}

func newServer(config Config) (*server, error) {
	if len(config.Token) == 0 {
		return nil, errors.New("session token is required")
	}
	if config.Log == nil {
		return nil, errors.New("request log is required")
	}
	creds := make(map[grant]credential, len(config.Credentials))
	for key, value := range config.Credentials {
		g, ok := grants[key]
		if !ok {
			return nil, fmt.Errorf("credential %s has no broker grant", key)
		}
		creds[g] = credential{key: key, value: value}
	}
	return &server{token: config.Token, creds: creds, uid: uint32(os.Getuid()), now: time.Now, log: config.Log}, nil
}

// Serve listens on config.SocketPath until ctx ends or the ssh forward exits.
// The forward is the only way the sidecar reaches the broker, so the broker
// fails closed with it rather than restarting it.
func Serve(ctx context.Context, config Config) error {
	s, err := newServer(config)
	if err != nil {
		return err
	}
	listener, err := listen(config.SocketPath)
	if err != nil {
		return err
	}
	defer removeSocket(config.SocketPath, listener)
	ctx, cancel := context.WithCancelCause(ctx)
	var sshDone chan struct{}
	defer func() {
		cancel(nil)
		if sshDone != nil {
			<-sshDone
		}
	}()
	if len(config.SSHArgs) > 0 {
		cmd := exec.CommandContext(ctx, "ssh", config.SSHArgs...)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start ssh forward: %w", err)
		}
		sshDone = make(chan struct{})
		go func() {
			defer close(sshDone)
			cancel(fmt.Errorf("ssh forward exited: %v", cmd.Wait()))
		}()
	}
	go func() { <-ctx.Done(); _ = listener.Close() }()
	for {
		connection, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() == nil {
				return err
			}
			break
		}
		go s.serve(connection)
	}
	if cause := context.Cause(ctx); !errors.Is(cause, context.Canceled) {
		return cause
	}
	return nil
}

func (s *server) serve(connection *net.UnixConn) {
	defer connection.Close()
	if uid, err := peerUID(connection); err != nil || uid != s.uid {
		s.record(Request{}, "", "peer_rejected")
		return
	}
	_ = connection.SetDeadline(time.Now().Add(ioTimeout))
	request, err := readRequest(connection)
	if err != nil {
		s.record(Request{}, "", "malformed")
		return
	}
	response, key := s.answer(request)
	result := response.Error
	if result == "" {
		result = "granted"
	}
	s.record(request, key, result)
	line, _ := json.Marshal(response)
	_, _ = connection.Write(append(line, '\n'))
}

func readRequest(reader io.Reader) (Request, error) {
	line, err := bufio.NewReader(io.LimitReader(reader, maxLineBytes)).ReadBytes('\n')
	if err != nil {
		return Request{}, err
	}
	var request Request
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return Request{}, err
	}
	return request, nil
}

// answer checks the token before anything else so an unauthenticated caller
// learns nothing about the grants and cannot spend the session's rate budget.
func (s *server) answer(request Request) (Response, string) {
	if subtle.ConstantTimeCompare([]byte(request.Token), s.token) != 1 {
		return Response{Error: ErrBadToken}, ""
	}
	if !s.allow() {
		return Response{Error: ErrRateLimited}, ""
	}
	c, ok := s.creds[grant{host: request.Host, header: request.Header}]
	if !ok {
		return Response{Error: ErrNotGranted}, ""
	}
	return Response{Value: c.value}, c.key
}

// allow is a fixed window: at most rateLimit requests per rateWindow.
func (s *server) allow() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if now.Sub(s.windowStart) >= rateWindow {
		s.windowStart, s.count = now, 0
	}
	if s.count >= rateLimit {
		return false
	}
	s.count++
	return true
}

type logRecord struct {
	Time       string `json:"time"`
	Host       string `json:"host,omitempty"`
	Header     string `json:"header,omitempty"`
	Credential string `json:"credential,omitempty"`
	Result     string `json:"result"`
}

func (s *server) record(request Request, key, result string) {
	line, _ := json.Marshal(logRecord{
		Time:       s.now().UTC().Format(time.RFC3339Nano),
		Host:       request.Host,
		Header:     request.Header,
		Credential: key,
		Result:     result,
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.log.Write(append(line, '\n'))
}

// listen binds a 0600 socket owned by the broker uid in a directory that no
// other uid can write. The peer uid check still guards the moment between
// bind and chmod.
func listen(path string) (*net.UnixListener, error) {
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if !parent.IsDir() || parent.Mode().Perm()&0o022 != 0 || fileUID(parent) != uint32(os.Getuid()) {
		return nil, errors.New("credential broker socket directory must be owned by the broker uid and not group or world writable")
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("credential broker socket path already exists")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(path, 0o600); err != nil {
		removeSocket(path, listener)
		return nil, err
	}
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
		removeSocket(path, listener)
		return nil, errors.New("credential broker socket verification failed")
	}
	return listener, nil
}

func removeSocket(path string, listener *net.UnixListener) {
	_ = listener.Close()
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
		_ = os.Remove(path)
	}
}

func fileUID(info os.FileInfo) uint32 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Uid
	}
	return ^uint32(0)
}
