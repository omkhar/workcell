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
)

type ReadFunc func(fd int) ([]byte, error)

type SpawnFunc func(command []string, stdin, stdout *os.File, stdinRead, masterRead ReadFunc) (int, error)

var (
	isTerminal = func(file *os.File) bool {
		info, err := file.Stat()
		return err == nil && info.Mode()&os.ModeCharDevice != 0
	}
	spawnPTY  SpawnFunc = spawnPTYReal
	readAtFD            = readAtFDReal
	writeAtFD           = writeAtFDReal
)

func Run(program string, stdin, stdout *os.File, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet(program, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var logPath string
	fs.StringVar(&logPath, "log", "", "")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: %s --log PATH -- command...\n", program)
	}

	if err := fs.Parse(args); err != nil {
		fs.Usage()
		return 2
	}
	if logPath == "" {
		fs.Usage()
		fmt.Fprintln(stderr, "--log is required")
		return 2
	}

	command := fs.Args()
	if len(command) > 0 && command[0] == "--" {
		command = command[1:]
	}
	if len(command) == 0 {
		fmt.Fprintf(stderr, "%s requires a command after --\n", program)
		return 2
	}
	if !isTerminal(stdin) || !isTerminal(stdout) {
		fmt.Fprintf(stderr, "%s requires an interactive terminal\n", program)
		return 2
	}

	logFile, err := openLog(logPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer logFile.Close()

	var logMu sync.Mutex
	writeLog := func(data []byte) {
		if len(data) == 0 {
			return
		}
		logMu.Lock()
		_, _ = logFile.Write(data)
		_ = logFile.Sync()
		logMu.Unlock()
	}

	startedAt := pythonTimestamp(time.Now())
	writeLog([]byte("# workcell-transcript-v1 start=" + startedAt + "\n"))

	stdinRead := func(fd int) ([]byte, error) {
		data, err := readAtFD(fd)
		writeLog(data)
		return data, err
	}
	masterRead := func(fd int) ([]byte, error) {
		data, err := readAtFD(fd)
		writeLog(data)
		return data, err
	}

	waitStatus, spawnErr := spawnPTY(command, stdin, stdout, stdinRead, masterRead)
	exitCode := exitCodeFromWaitStatus(waitStatus)
	if spawnErr != nil {
		exitCode = exitCodeFromSpawnError(spawnErr)
		fmt.Fprintf(stderr, "%s failed to exec %s: %s\n", program, command[0], spawnErrorText(spawnErr))
	}

	finishedAt := pythonTimestamp(time.Now())
	writeLog(renderFooter(finishedAt, exitCode, waitStatus, spawnErr))
	return exitCode
}

func openLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
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

func exitCodeFromSpawnError(err error) int {
	if errors.Is(err, syscall.ENOENT) || errors.Is(err, exec.ErrNotFound) {
		return 127
	}
	return 126
}

func spawnErrorText(err error) string {
	if errors.Is(err, syscall.ENOENT) || errors.Is(err, exec.ErrNotFound) {
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
	if errors.Is(err, syscall.ENOENT) || errors.Is(err, exec.ErrNotFound) {
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

func readAtFDReal(fd int) ([]byte, error) {
	buf := make([]byte, 1024)
	n, err := syscall.Read(fd, buf)
	if n > 0 {
		return buf[:n], err
	}
	return nil, err
}

func writeAtFDReal(fd int, data []byte) error {
	for len(data) > 0 {
		n, err := syscall.Write(fd, data)
		if err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}
