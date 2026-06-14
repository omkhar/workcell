// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package transcript

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/omkhar/workcell/internal/cliexit"
)

type ReadFunc func(fd int) ([]byte, error)

type SpawnFunc func(command []string, stdin, stdout *os.File, stdinRead, masterRead ReadFunc) (int, error)

type transcriptLog interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

type transcriptPersistenceError struct {
	err error
}

func (e transcriptPersistenceError) Error() string {
	return e.err.Error()
}

func (e transcriptPersistenceError) Unwrap() error {
	return e.err
}

var (
	isTerminal  = isInteractiveTerminal
	openLogFile = func(path string) (transcriptLog, error) {
		return openLog(path)
	}
	spawnPTY  SpawnFunc = spawnPTYReal
	readAtFD            = readAtFDReal
	writeAtFD           = writeAtFDReal
)

// Run is the Go translation of the bash workcell-pty-transcript entry
// point.  Diagnostics land on stderr exactly as the original did; the
// returned error is either nil (exit 0) or a *cliexit.ExitCodeError
// whose Code carries the bash exit-code contract.  The hostutil wrapper
// recovers Code via errors.As and propagates it to os.Exit.
func Run(program string, stdin, stdout *os.File, stderr io.Writer, args []string) error {
	fs := flag.NewFlagSet(program, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var logPath string
	fs.StringVar(&logPath, "log", "", "")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: %s --log PATH -- command...\n", program)
	}

	if err := fs.Parse(args); err != nil {
		fs.Usage()
		return &cliexit.ExitCodeError{Code: 2}
	}
	if logPath == "" {
		fs.Usage()
		fmt.Fprintln(stderr, "--log is required")
		return &cliexit.ExitCodeError{Code: 2}
	}

	command := fs.Args()
	if len(command) > 0 && command[0] == "--" {
		command = command[1:]
	}
	if len(command) == 0 {
		fmt.Fprintf(stderr, "%s requires a command after --\n", program)
		return &cliexit.ExitCodeError{Code: 2}
	}
	if !isTerminal(stdin) || !isTerminal(stdout) {
		fmt.Fprintf(stderr, "%s requires an interactive terminal\n", program)
		return &cliexit.ExitCodeError{Code: 2}
	}

	logFile, err := openLogFile(logPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return &cliexit.ExitCodeError{Code: 1}
	}
	defer logFile.Close()

	var logMu sync.Mutex
	var persistErr error
	writeLog := func(data []byte) error {
		if len(data) == 0 {
			return nil
		}
		logMu.Lock()
		defer logMu.Unlock()
		if persistErr != nil {
			return transcriptPersistenceError{err: persistErr}
		}
		if _, err := logFile.Write(data); err != nil {
			persistErr = err
			return transcriptPersistenceError{err: err}
		}
		if err := logFile.Sync(); err != nil {
			persistErr = err
			return transcriptPersistenceError{err: err}
		}
		return nil
	}
	reportPersistErr := func() error {
		if persistErr == nil {
			return nil
		}
		fmt.Fprintf(stderr, "%s failed to persist transcript: %s\n", program, persistErr)
		return &cliexit.ExitCodeError{Code: 1}
	}

	startedAt := pythonTimestamp(time.Now())
	if err := writeLog([]byte("# workcell-transcript-v1 start=" + startedAt + "\n")); err != nil {
		return reportPersistErr()
	}

	stdinRead := func(fd int) ([]byte, error) {
		data, err := readAtFD(fd)
		if writeErr := writeLog(data); writeErr != nil {
			return nil, writeErr
		}
		return data, err
	}
	masterRead := func(fd int) ([]byte, error) {
		data, err := readAtFD(fd)
		if writeErr := writeLog(data); writeErr != nil {
			return data, writeErr
		}
		return data, err
	}

	waitStatus, spawnErr := spawnPTY(command, stdin, stdout, stdinRead, masterRead)
	exitCode := exitCodeFromWaitStatus(waitStatus)
	if spawnErr != nil {
		if !isTranscriptPersistenceError(spawnErr) {
			exitCode = exitCodeFromSpawnError(spawnErr)
			fmt.Fprintf(stderr, "%s failed to exec %s: %s\n", program, command[0], spawnErrorText(spawnErr))
		}
	}

	finishedAt := pythonTimestamp(time.Now())
	_ = writeLog(renderFooter(finishedAt, exitCode, waitStatus, spawnErr))
	if err := reportPersistErr(); err != nil {
		return err
	}
	if exitCode == 0 {
		return nil
	}
	return &cliexit.ExitCodeError{Code: exitCode}
}

func isInteractiveTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	return isTerminalFD(file.Fd())
}

func isTranscriptPersistenceError(err error) bool {
	var persistErr transcriptPersistenceError
	return errors.As(err, &persistErr)
}

// openLog opens the transcript log at path for write-truncate.  The
// transcript records stdin and PTY output verbatim — including anything
// the agent typed — so the path MUST resist symlink-swap attacks from
// a co-tenant on the host:
//
//   - O_NOFOLLOW refuses to follow a symlink planted at the leaf.  A
//     swap that races the open after a stat/touch check will fail to
//     open rather than overwrite the symlink target.
//   - The parent directory is Lstat-checked to refuse a symlink parent
//     (which would let an attacker redirect transcripts wholesale).
//
// This does NOT walk the full parent chain — the operator selects the
// transcript path and is expected to choose a safe location.  The
// hardening here is defense-in-depth against a co-tenant racing a
// known parent, not a general anti-traversal.
func openLog(path string) (*os.File, error) {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(parent); err != nil {
		return nil, err
	} else if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("transcript parent directory is a symlink: %s", parent)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func exitCodeFromWaitStatus(status int) int {
	waitStatus := syscall.WaitStatus(status)
	if waitStatus.Exited() {
		return waitStatus.ExitStatus()
	}
	if waitStatus.Signaled() {
		return 128 + int(waitStatus.Signal())
	}
	return 1
}

func isNotFoundError(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, exec.ErrNotFound)
}

func exitCodeFromSpawnError(err error) int {
	if isNotFoundError(err) {
		return 127
	}
	return 126
}

func spawnErrorText(err error) string {
	if isNotFoundError(err) {
		return "No such file or directory"
	}
	return err.Error()
}

func renderFooter(finishedAt string, exitCode int, waitStatus int, spawnErr error) []byte {
	statusField := "wait_status=spawn-error"
	if spawnErr == nil {
		statusField = fmt.Sprintf("wait_status=%d", waitStatus)
	}
	errnoField := ""
	if spawnErr != nil {
		errnoField = fmt.Sprintf(" spawn_errno=%d", spawnErrno(spawnErr))
	}
	return []byte(
		fmt.Sprintf("\n# workcell-transcript-v1 end=%s %s%s exit_code=%d\n",
			finishedAt, statusField, errnoField, exitCode),
	)
}

func spawnErrno(err error) int {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return int(errno)
	}
	if isNotFoundError(err) {
		return int(syscall.ENOENT)
	}
	return 1
}

func pythonTimestamp(t time.Time) string {
	t = t.UTC()
	frac := t.Nanosecond() / 1000
	if frac == 0 {
		return t.Format("2006-01-02T15:04:05") + "+00:00"
	}
	fracText := fmt.Sprintf("%06d", frac)
	for len(fracText) > 0 && fracText[len(fracText)-1] == '0' {
		fracText = fracText[:len(fracText)-1]
	}
	return t.Format("2006-01-02T15:04:05") + "." + fracText + "+00:00"
}

func isRetryableIOError(err error) bool {
	return errors.Is(err, syscall.EINTR)
}

func readAtFDReal(fd int) ([]byte, error) {
	for {
		buf := make([]byte, 1024)
		n, err := syscall.Read(fd, buf)
		if err != nil && isRetryableIOError(err) {
			continue
		}
		if n > 0 {
			return buf[:n], err
		}
		return nil, err
	}
}

func writeAtFDReal(fd int, data []byte) error {
	for len(data) > 0 {
		n, err := syscall.Write(fd, data)
		if err != nil {
			if isRetryableIOError(err) {
				continue
			}
			return err
		}
		data = data[n:]
	}
	return nil
}
