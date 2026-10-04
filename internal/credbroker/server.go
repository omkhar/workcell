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

	"golang.org/x/sys/unix"

	"github.com/omkhar/workcell/internal/rootio"
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
	ErrLogFailed   = "log_failed"
)

type grant struct{ host, header string }

// grants is the fixed table of where each credential key may be sent. The
// broker always returns the bare value; for authorization the proxy adds the
// "Bearer " prefix where the placeholder header carried it.
var grants = map[string][]grant{
	"claude_api_key": {{host: "api.anthropic.com", header: "x-api-key"}, {host: "api.anthropic.com", header: "authorization"}},
	"gemini_env":     {{host: "generativelanguage.googleapis.com", header: "x-goog-api-key"}},
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

// RequestLog receives one JSON line per request and makes it durable on
// Sync; a grant is released only after its record is durable, so a writer
// that cannot sync is not a request log.
type RequestLog interface {
	io.Writer
	Sync() error
}

// Config is one broker session.
type Config struct {
	SocketPath  string
	Token       []byte
	Credentials map[string]string // credential key -> value; keys must be in grants
	Log         RequestLog        // never receives values
	SSHArgs     []string          // argv for the supervised ssh forward; empty runs none
}

type credential struct{ key, value string }

type server struct {
	token []byte
	creds map[grant]credential
	uid   uint32
	now   func() time.Time

	mu          sync.Mutex
	log         RequestLog
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
		keyGrants, ok := grants[key]
		if !ok {
			return nil, fmt.Errorf("credential %s has no broker grant", key)
		}
		for _, g := range keyGrants {
			creds[g] = credential{key: key, value: value}
		}
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
	socket, err := listen(config.SocketPath)
	if err != nil {
		return err
	}
	defer socket.close()
	listener := socket.listener
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
	// The record is the ledger of every release: a grant whose record is not
	// durable is withheld, so the log never under-reports what left the host.
	if err := s.record(request, key, result); err != nil {
		response = Response{Error: ErrLogFailed}
	}
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

// record appends one line and makes it durable before it returns, so a
// caller can withhold a grant whose record did not reach the log.
func (s *server) record(request Request, key, result string) error {
	line, _ := json.Marshal(logRecord{
		Time:       s.now().UTC().Format(time.RFC3339Nano),
		Host:       request.Host,
		Header:     request.Header,
		Credential: key,
		Result:     result,
	})
	line = append(line, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.log.Write(line)
	if err == nil && n != len(line) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return err
	}
	return s.log.Sync()
}

// boundSocket is a listening socket with the parent directory descriptor it
// was created through. The descriptor lives as long as the listener, so the
// final unlink also goes through it rather than through a pathname.
type boundSocket struct {
	listener *net.UnixListener
	parent   *os.File
	name     string
}

// close stops the listener, unlinks the socket through the pinned parent
// while it is still a socket, and releases the parent.
func (b *boundSocket) close() {
	_ = b.listener.Close()
	var leaf unix.Stat_t
	if err := unix.Fstatat(int(b.parent.Fd()), b.name, &leaf, unix.AT_SYMLINK_NOFOLLOW); err == nil && leaf.Mode&unix.S_IFMT == unix.S_IFSOCK {
		_ = unix.Unlinkat(int(b.parent.Fd()), b.name, 0)
	}
	_ = b.parent.Close()
}

// listen binds a 0600 socket owned by the broker uid in a directory that no
// other uid can write. The parent is reached one descriptor at a time with
// O_NOFOLLOW and pinned; the bind, the mode change and the checks all go
// through that descriptor, so a pathname cannot be repointed in between. The
// peer uid check still guards the moment between bind and chmod.
func listen(path string) (*boundSocket, error) {
	parent, cleaned, err := rootio.OpenParentDirectoryNoFollow(path)
	if err != nil {
		return nil, err
	}
	var parentStat unix.Stat_t
	if err := unix.Fstat(int(parent.Fd()), &parentStat); err != nil {
		_ = parent.Close()
		return nil, err
	}
	if parentStat.Mode&unix.S_IFMT != unix.S_IFDIR || parentStat.Mode&0o022 != 0 || parentStat.Uid != uint32(os.Getuid()) {
		_ = parent.Close()
		return nil, errors.New("credential broker socket directory must be owned by the broker uid and not group or world writable")
	}
	name := filepath.Base(cleaned)
	var leaf unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), name, &leaf, unix.AT_SYMLINK_NOFOLLOW); !errors.Is(err, unix.ENOENT) {
		_ = parent.Close()
		return nil, errors.New("credential broker socket path already exists")
	}
	// bind(2) takes a pathname, so the socket is created by name; every check
	// and the mode change then go through the pinned parent. A bind that an
	// ancestor swap redirected elsewhere leaves no socket under the pinned
	// parent, fails the check below, and is closed.
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: cleaned, Net: "unix"})
	if err != nil {
		_ = parent.Close()
		return nil, err
	}
	listener.SetUnlinkOnClose(false)
	bound := &boundSocket{listener: listener, parent: parent, name: name}
	fail := func(err error) (*boundSocket, error) {
		bound.close()
		return nil, err
	}
	if err := unix.Fstatat(int(parent.Fd()), name, &leaf, unix.AT_SYMLINK_NOFOLLOW); err != nil || leaf.Mode&unix.S_IFMT != unix.S_IFSOCK || leaf.Uid != uint32(os.Getuid()) {
		return fail(errors.New("credential broker socket verification failed"))
	}
	if err := unix.Fchmodat(int(parent.Fd()), name, 0o600, 0); err != nil {
		return fail(err)
	}
	if err := unix.Fstatat(int(parent.Fd()), name, &leaf, unix.AT_SYMLINK_NOFOLLOW); err != nil || leaf.Mode&unix.S_IFMT != unix.S_IFSOCK || leaf.Mode&0o777 != 0o600 {
		return fail(errors.New("credential broker socket verification failed"))
	}
	return bound, nil
}

func fileUID(info os.FileInfo) uint32 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Uid
	}
	return ^uint32(0)
}
