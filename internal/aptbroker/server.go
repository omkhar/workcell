// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package aptbroker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const (
	DefaultHelperPath    = "/usr/local/libexec/workcell/apt-helper.sh"
	DefaultTimeout       = 300 * time.Second
	MaxTimeout           = 900 * time.Second
	maxConcurrent        = 4
	requestReadTimeout   = 10 * time.Second
	responseWriteTimeout = 10 * time.Second
	serverShutdownLimit  = 5 * time.Second
)

type ServerConfig struct {
	SocketPath        string
	HelperPath        string
	Timeout           time.Duration
	MaxConcurrent     int
	ExpectedPeerUID   uint32
	RequireRootSocket bool
	ErrorWriter       io.Writer
	Ready             func() error
}

func (c *ServerConfig) normalize() error {
	c.applyDefaults()
	if !validHelperTimeout(c.Timeout) {
		return fmt.Errorf("invalid helper timeout")
	}
	if !validConcurrency(c.MaxConcurrent) {
		return fmt.Errorf("invalid concurrent request limit")
	}
	if c.ExpectedPeerUID == 0 {
		return fmt.Errorf("peer uid is required")
	}
	return nil
}

func validHelperTimeout(timeout time.Duration) bool {
	return timeout > 0 && timeout <= MaxTimeout
}

func validConcurrency(concurrency int) bool {
	return concurrency >= 1 && concurrency <= maxConcurrent
}

func (c *ServerConfig) applyDefaults() {
	if c.SocketPath == "" {
		c.SocketPath = DefaultSocketPath
	}
	if c.HelperPath == "" {
		c.HelperPath = DefaultHelperPath
	}
	if c.Timeout == 0 {
		c.Timeout = DefaultTimeout
	}
	if c.MaxConcurrent == 0 {
		c.MaxConcurrent = maxConcurrent
	}
}

func (c ServerConfig) report(err error) {
	if err == nil {
		return
	}
	writer := c.ErrorWriter
	if writer == nil {
		writer = os.Stderr
	}
	_, _ = fmt.Fprintf(writer, "Workcell apt broker: %v\n", err)
}

func Serve(ctx context.Context, config ServerConfig) error {
	if err := validateServerSupport(); err != nil {
		return err
	}
	if err := config.normalize(); err != nil {
		return err
	}
	listener, err := listenSocket(config.SocketPath, config.RequireRootSocket)
	if err != nil {
		return err
	}
	defer removeSocket(config.SocketPath, listener)
	if config.Ready != nil {
		if err := config.Ready(); err != nil {
			return fmt.Errorf("complete startup handshake: %w", err)
		}
	}
	serverContext, cancel := context.WithCancel(ctx)
	defer cancel()
	go closeListenerOnCancel(serverContext, listener)
	return acceptConnections(serverContext, cancel, listener, config)
}

func closeListenerOnCancel(ctx context.Context, listener *net.UnixListener) {
	<-ctx.Done()
	_ = listener.Close()
}

func acceptConnections(ctx context.Context, cancel context.CancelFunc, listener *net.UnixListener, config ServerConfig) error {
	admitted := make(chan struct{}, config.MaxConcurrent)
	var handlers sync.WaitGroup
	for {
		connection, err := listener.AcceptUnix()
		if err != nil {
			acceptErr := acceptError(ctx, err)
			cancel()
			return errors.Join(acceptErr, waitForHandlers(&handlers, serverShutdownLimit))
		}
		if err := admitConnection(connection, config.ExpectedPeerUID, admitted); err != nil {
			config.report(err)
			continue
		}
		handlers.Add(1)
		go serveConnection(ctx, connection, config, admitted, &handlers)
	}
}

func waitForHandlers(handlers *sync.WaitGroup, limit time.Duration) error {
	done := make(chan struct{})
	go func() { handlers.Wait(); close(done) }()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return fmt.Errorf("apt broker handler shutdown exceeded limit")
	}
}

func acceptError(ctx context.Context, err error) error {
	if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func admitConnection(connection *net.UnixConn, expectedUID uint32, admitted chan<- struct{}) error {
	uid, err := peerUID(connection)
	if err := validatePeerUID(uid, err, expectedUID); err != nil {
		_ = connection.Close()
		return err
	}
	if !reserveAdmission(admitted) {
		_ = connection.Close()
		return fmt.Errorf("concurrent request limit reached")
	}
	return nil
}

func validatePeerUID(uid uint32, peerErr error, expectedUID uint32) error {
	if peerErr != nil {
		return fmt.Errorf("inspect peer credentials: %w", peerErr)
	}
	if uid == 0 || uid != expectedUID {
		return fmt.Errorf("reject peer uid %d", uid)
	}
	return nil
}

func reserveAdmission(admitted chan<- struct{}) bool {
	select {
	case admitted <- struct{}{}:
		return true
	default:
		return false
	}
}

func serveConnection(ctx context.Context, connection *net.UnixConn, config ServerConfig, admitted <-chan struct{}, handlers *sync.WaitGroup) {
	defer handlers.Done()
	defer func() { <-admitted }()
	defer connection.Close()
	finished := make(chan struct{})
	defer close(finished)
	go closeConnectionOnCancel(ctx, connection, finished)
	config.report(handleConnection(ctx, connection, config))
}

func handleConnection(ctx context.Context, connection *net.UnixConn, config ServerConfig) error {
	request, err := readRequest(connection)
	if err != nil {
		return err
	}
	response, err := runOwnedHelper(ctx, connection, request, config.helperConfig())
	if err != nil {
		return fmt.Errorf("run helper: %w", err)
	}
	return writeResponse(connection, response)
}

func readRequest(connection *net.UnixConn) (Request, error) {
	if err := connection.SetReadDeadline(time.Now().Add(requestReadTimeout)); err != nil {
		return Request{}, fmt.Errorf("set request deadline: %w", err)
	}
	body, err := readFrame(connection, requestMagic, MaxRequestBytes)
	if err != nil {
		return Request{}, fmt.Errorf("read request: %w", err)
	}
	request, err := decodeRequest(body)
	if err != nil {
		return Request{}, fmt.Errorf("decode request: %w", err)
	}
	if err := connection.SetReadDeadline(time.Time{}); err != nil {
		return Request{}, fmt.Errorf("clear request deadline: %w", err)
	}
	return request, nil
}

func writeResponse(connection *net.UnixConn, response Response) error {
	frame, err := responseFrame(response)
	if err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	if err := connection.SetWriteDeadline(time.Now().Add(responseWriteTimeout)); err != nil {
		return fmt.Errorf("set response deadline: %w", err)
	}
	if _, err := connection.Write(frame); err != nil {
		return fmt.Errorf("write response: %w", err)
	}
	return nil
}

func closeConnectionOnCancel(ctx context.Context, connection *net.UnixConn, finished <-chan struct{}) {
	select {
	case <-ctx.Done():
		_ = connection.Close()
	case <-finished:
	}
}

func listenSocket(path string, requireRoot bool) (*net.UnixListener, error) {
	if err := validateSocketParent(filepath.Dir(path), requireRoot); err != nil {
		return nil, err
	}
	if err := prepareSocketPath(path); err != nil {
		return nil, err
	}
	listener, err := bindSocket(path)
	if err != nil {
		return nil, err
	}
	if err := validateSocket(path, requireRoot); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return listener, nil
}

// The socket carries its final mode out of the bind itself. Applying the mode
// to the pathname afterwards lets anyone who can write the parent directory
// swap in a symlink between the two steps and redirect a privileged chmod onto
// a file of their choosing. The bind also already owns the socket as the server
// uid, so an explicit chown would reopen that same window for an ownership the
// kernel has given us anyway; validateSocket confirms it instead. Close must
// not unlink either, or it would delete whatever holds the pathname at shutdown
// before removeSocket can check that it is still our socket.
//
// ponytail: umask is process-global, which is safe here only because Serve
// binds once during startup. Bind through a parent directory descriptor if this
// ever has to run beside other file creation.
func bindSocket(path string) (*net.UnixListener, error) {
	previous := syscall.Umask(0o111)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	syscall.Umask(previous)
	if err != nil {
		return nil, err
	}
	listener.SetUnlinkOnClose(false)
	return listener, nil
}

func validateSocketParent(path string, requireRoot bool) error {
	parent, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !isTrustedSocketDirectory(parent) {
		return fmt.Errorf("apt broker socket parent is not trusted")
	}
	if requireRoot && fileUID(parent) != 0 {
		return fmt.Errorf("apt broker socket parent is not root-owned")
	}
	if requireRoot && parent.Mode().Perm() != 0o755 {
		return fmt.Errorf("apt broker socket parent mode is not 0755")
	}
	return nil
}

func isTrustedSocketDirectory(info os.FileInfo) bool {
	return info.IsDir() && info.Mode().Perm()&0o022 == 0
}

func prepareSocketPath(path string) error {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("apt broker socket already exists")
}

func validateSocket(path string, requireRoot bool) error {
	info, err := os.Lstat(path)
	if !isSocketFile(info, err) {
		return fmt.Errorf("apt broker socket verification failed")
	}
	if requireRoot && fileUID(info) != 0 {
		return fmt.Errorf("apt broker socket is not root-owned")
	}
	if info.Mode().Perm() != 0o666 {
		return fmt.Errorf("apt broker socket mode is not 0666")
	}
	return nil
}

func isSocketFile(info os.FileInfo, err error) bool {
	return err == nil && info.Mode()&os.ModeSymlink == 0 && info.Mode()&os.ModeSocket != 0
}

func removeSocket(path string, listener *net.UnixListener) {
	_ = listener.Close()
	if isSocketFile(os.Lstat(path)) {
		_ = os.Remove(path)
	}
}

func (c ServerConfig) helperConfig() helperConfig {
	return helperConfig{path: c.HelperPath, timeout: c.Timeout, report: c.report}
}
