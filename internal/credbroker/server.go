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

	"github.com/omkhar/workcell/internal/aptbroker"
	"github.com/omkhar/workcell/internal/rootio"
)

const (
	rateLimit    = 300
	rateWindow   = time.Minute
	maxLineBytes = 4096
	ioTimeout    = 5 * time.Second
	// maxConns bounds the handlers alive at once: an idle connection holds a
	// goroutine and a descriptor for ioTimeout, and the rate limit applies
	// only after a complete request, so admission is limited first.
	maxConns = 64
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
	// Rejected requests have their own window so an unauthenticated caller
	// can neither spend the authenticated quota nor fill the ledger.
	rejectStart time.Time
	rejected    int
	conns       map[*net.UnixConn]struct{}
	handlers    sync.WaitGroup
	logFailed   error
	admissions  chan struct{}
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
	return &server{token: config.Token, creds: creds, uid: uint32(os.Getuid()), now: time.Now, log: config.Log, conns: map[*net.UnixConn]struct{}{}, admissions: make(chan struct{}, maxConns)}, nil
}

// track registers an accepted connection so shutdown can close it.
func (s *server) track(connection *net.UnixConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conns[connection] = struct{}{}
}

func (s *server) untrack(connection *net.UnixConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, connection)
}

// stopHandlers closes every accepted connection and waits for its handler,
// so no credential is released after Serve returns.
func (s *server) stopHandlers() {
	s.mu.Lock()
	for connection := range s.conns {
		_ = connection.Close()
	}
	s.mu.Unlock()
	s.handlers.Wait()
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
	defer s.stopHandlers()
	for {
		connection, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() == nil {
				return err
			}
			break
		}
		// Admit at most maxConns handlers; an excess connection is closed
		// before any handler starts, so a burst of idle connections cannot
		// exhaust descriptors and end Serve.
		select {
		case s.admissions <- struct{}{}:
		default:
			_ = connection.Close()
			continue
		}
		// Track before the handler starts: stopHandlers runs only after this
		// loop has exited, so every accepted connection is registered by then.
		s.track(connection)
		s.handlers.Add(1)
		go func() {
			defer s.handlers.Done()
			defer func() { <-s.admissions }()
			s.serve(connection)
		}()
	}
	if cause := context.Cause(ctx); !errors.Is(cause, context.Canceled) {
		return cause
	}
	return nil
}

func (s *server) serve(connection *net.UnixConn) {
	defer s.untrack(connection)
	defer connection.Close()
	// Every rejection, including one before the request parses, spends the
	// rejection budget before it is recorded, so an unauthenticated caller
	// cannot fill the ledger through any path.
	if uid, err := aptbroker.SocketPeerUID(connection); err != nil || uid != s.uid {
		if s.allowRejected() {
			_ = s.record(Request{}, "", "peer_rejected")
		}
		return
	}
	_ = connection.SetDeadline(time.Now().Add(ioTimeout))
	request, err := readRequest(connection)
	if err != nil {
		if s.allowRejected() {
			_ = s.record(Request{}, "", "malformed")
		}
		return
	}
	response, key := s.answer(request)
	result := response.Error
	if result == "" {
		result = "granted"
	}
	if result != "granted" && !s.allowRejected() {
		return // over the rejection budget: no record, no answer
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
	return fixedWindow(s.now(), &s.windowStart, &s.count)
}

// allowRejected bounds the rejected requests recorded per rateWindow.
func (s *server) allowRejected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fixedWindow(s.now(), &s.rejectStart, &s.rejected)
}

func fixedWindow(now time.Time, start *time.Time, count *int) bool {
	if now.Sub(*start) >= rateWindow {
		*start, *count = now, 0
	}
	if *count >= rateLimit {
		return false
	}
	*count++
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
	// A failed write or sync may have left a partial record; nothing is
	// appended after it, so the ledger never carries a release it cannot
	// account for. The broker answers log_failed from then on.
	if s.logFailed != nil {
		return s.logFailed
	}
	n, err := s.log.Write(line)
	if err == nil && n != len(line) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = s.log.Sync()
	}
	if err != nil {
		s.logFailed = fmt.Errorf("request log failed; no further grants: %w", err)
		return s.logFailed
	}
	return nil
}

// requireAncestryWritableOnlyBy refuses a socket directory when any ancestor
// could be renamed or replaced by a uid other than root or uid: every
// ancestor must be a real directory owned by root or uid and either not
// group or world writable or sticky, since a sticky directory lets another
// uid add entries but not rename or remove ours. Without this, the pathname
// bind(2) could land in a replacement tree between the checked walk and the
// bind. The apt broker keeps its own root-only rule, which admits no sticky
// directory, because its socket lives under a root-owned run directory.
func requireAncestryWritableOnlyBy(dir string, uid uint32) error {
	current, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	for {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("credential broker socket ancestor %s is not a directory", current)
		}
		owner := fileUID(info)
		mode := info.Mode()
		if (owner != 0 && owner != uid) || (mode.Perm()&0o022 != 0 && mode&os.ModeSticky == 0) {
			return fmt.Errorf("credential broker socket ancestor %s is writable by another uid", current)
		}
		next := filepath.Dir(current)
		if next == current {
			return nil
		}
		current = next
	}
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
//
// Residual risk, accepted: the check and the unlink address the leaf by name
// under the pinned parent, and POSIX has no unlink by inode, so a process that
// can write that directory could swap the leaf between the two calls. listen
// admits only a directory owned by the broker uid that no other uid can
// write, so that process is the broker uid itself or root, both of which
// already control the broker; the guard exists to keep the broker from
// removing another occupant's entry by accident, not to defend against a
// hostile same-uid process, which is outside the threat model, as it is for
// scripts/lib/owned-root.sh.
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
	// cleaned is the canonical path the no-follow walk opened, so its
	// ancestors are the directories that walk pinned.
	if err := requireAncestryWritableOnlyBy(filepath.Dir(cleaned), uint32(os.Getuid())); err != nil {
		_ = parent.Close()
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
