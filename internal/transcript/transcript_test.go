package transcript

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestExitCodeFromWaitStatus(t *testing.T) {
	t.Parallel()

	if got := exitCodeFromWaitStatus(7 << 8); got != 7 {
		t.Fatalf("exitCodeFromWaitStatus(exit) = %d", got)
	}
	if got := exitCodeFromWaitStatus(int(syscall.SIGTERM)); got != 128+int(syscall.SIGTERM) {
		t.Fatalf("exitCodeFromWaitStatus(signal) = %d", got)
	}
	if got := exitCodeFromWaitStatus(0x7F); got != 1 {
		t.Fatalf("exitCodeFromWaitStatus(other) = %d", got)
	}
}

func TestRenderFooterMatchesPythonShape(t *testing.T) {
	t.Parallel()

	got := string(renderFooter("2026-04-02T10:20:30+00:00", 7, 1792, nil))
	if !strings.HasPrefix(got, "\n# workcell-transcript-v1 end=2026-04-02T10:20:30+00:00 wait_status=1792 exit_code=7\n") {
		t.Fatalf("unexpected footer: %q", got)
	}
	got = string(renderFooter("2026-04-02T10:20:30+00:00", 127, 0, syscall.ENOENT))
	if !strings.Contains(got, "wait_status=spawn-error spawn_errno=2 exit_code=127") {
		t.Fatalf("unexpected spawn footer: %q", got)
	}
}

func TestRunStripsSeparatorRecordsTranscriptAndReturnsExitCode(t *testing.T) {
	oldIsTerminal := isTerminal
	oldSpawn := spawnPTY
	oldRead := readAtFD
	defer func() {
		isTerminal = oldIsTerminal
		spawnPTY = oldSpawn
		readAtFD = oldRead
	}()

	isTerminal = func(*os.File) bool { return true }

	var reads [][]byte
	readAtFD = func(fd int) ([]byte, error) {
		switch fd {
		case 10:
			return []byte("user input\n"), nil
		case 11:
			return []byte("child output\n"), nil
		default:
			return nil, io.EOF
		}
	}

	spawnPTY = func(command []string, stdin, stdout *os.File, stdinRead, masterRead ReadFunc) (int, error) {
		if len(command) != 2 || command[0] != "fake-agent" || command[1] != "--version" {
			t.Fatalf("unexpected command: %#v", command)
		}
		if data, err := stdinRead(10); err != nil || string(data) != "user input\n" {
			t.Fatalf("stdinRead = %q, %v", data, err)
		}
		if data, err := masterRead(11); err != nil || string(data) != "child output\n" {
			t.Fatalf("masterRead = %q, %v", data, err)
		}
		reads = append(reads, []byte("called"))
		return 7 << 8, nil
	}

	logPath := filepath.Join(t.TempDir(), "transcript.log")
	var stderr bytes.Buffer
	code := Run("pty_transcript", os.Stdin, os.Stdout, &stderr, []string{"--log", logPath, "--", "fake-agent", "--version"})
	if code != 7 {
		t.Fatalf("Run() = %d, want 7", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
	if len(reads) != 1 {
		t.Fatalf("spawn was not called")
	}

	transcript := readFile(t, logPath)
	if !bytes.Contains(transcript, []byte("# workcell-transcript-v1 start=")) {
		t.Fatalf("missing start header: %q", transcript)
	}
	if !bytes.Contains(transcript, []byte("user input\nchild output\n")) {
		t.Fatalf("missing transcript bytes: %q", transcript)
	}
	if !bytes.Contains(transcript, []byte("wait_status=1792")) {
		t.Fatalf("missing wait status: %q", transcript)
	}
	if !bytes.Contains(transcript, []byte("exit_code=7")) {
		t.Fatalf("missing exit code: %q", transcript)
	}
	assertMode(t, logPath, 0o600)
}

func TestRunRequiresCommandAfterSeparator(t *testing.T) {
	oldIsTerminal := isTerminal
	defer func() { isTerminal = oldIsTerminal }()
	isTerminal = func(*os.File) bool { return true }

	var stderr bytes.Buffer
	code := Run("pty_transcript", os.Stdin, os.Stdout, &stderr, []string{"--log", filepath.Join(t.TempDir(), "transcript.log"), "--"})
	if code != 2 {
		t.Fatalf("Run() = %d, want 2", code)
	}
	if got := stderr.String(); got != "pty_transcript requires a command after --\n" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestRunRequiresInteractiveTerminal(t *testing.T) {
	oldIsTerminal := isTerminal
	defer func() { isTerminal = oldIsTerminal }()
	isTerminal = func(*os.File) bool { return false }

	var stderr bytes.Buffer
	code := Run("pty_transcript", os.Stdin, os.Stdout, &stderr, []string{"--log", filepath.Join(t.TempDir(), "transcript.log"), "echo"})
	if code != 2 {
		t.Fatalf("Run() = %d, want 2", code)
	}
	if got := stderr.String(); got != "pty_transcript requires an interactive terminal\n" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestRunHandlesSpawnErrorsWithoutTracebacks(t *testing.T) {
	oldIsTerminal := isTerminal
	oldSpawn := spawnPTY
	oldRead := readAtFD
	defer func() {
		isTerminal = oldIsTerminal
		spawnPTY = oldSpawn
		readAtFD = oldRead
	}()

	isTerminal = func(*os.File) bool { return true }
	readAtFD = func(int) ([]byte, error) { return nil, io.EOF }
	spawnPTY = func(command []string, stdin, stdout *os.File, stdinRead, masterRead ReadFunc) (int, error) {
		return 0, syscall.ENOENT
	}

	var stderr bytes.Buffer
	code := Run("pty_transcript", os.Stdin, os.Stdout, &stderr, []string{"--log", filepath.Join(t.TempDir(), "transcript.log"), "missing-agent"})
	if code != 127 {
		t.Fatalf("Run() = %d, want 127", code)
	}
	if !strings.Contains(stderr.String(), "failed to exec missing-agent") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestOpenPTYReturnsSlavePath(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("pty backend only covered on unix")
	}

	master, slaveName, err := openPTY()
	if err != nil {
		t.Fatalf("openPTY() error = %v", err)
	}
	defer master.Close()

	if slaveName == "" {
		t.Fatal("openPTY() returned empty slave name")
	}
	if _, err := os.Stat(slaveName); err != nil {
		t.Fatalf("slave path %q not accessible: %v", slaveName, err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode mismatch for %s: got %04o want %04o", path, got, want)
	}
}
