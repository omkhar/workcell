package testkit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRoot(tb testing.TB) string {
	tb.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatal("unable to determine repo root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func shellCommand(body, root string) string {
	return "/bin/sh -c " + shellQuote(body) + " sh " + shellQuote(root)
}

func TestRunCommandCapturesExitCodeAndStreams(t *testing.T) {
	t.Parallel()

	result, err := RunCommand(context.Background(), CommandSpec{
		Path: "/bin/sh",
		Args: []string{"-c", "printf out; printf err >&2; exit 7"},
	})
	if err != nil {
		t.Fatalf("RunCommand returned error: %v", err)
	}
	if result.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", result.ExitCode)
	}
	if string(result.Stdout) != "out" {
		t.Fatalf("stdout = %q, want %q", result.Stdout, "out")
	}
	if string(result.Stderr) != "err" {
		t.Fatalf("stderr = %q, want %q", result.Stderr, "err")
	}
}

func TestSnapshotTreeCapturesModesAndSymlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dirPath := filepath.Join(root, "dir")
	if err := os.Mkdir(dirPath, 0o750); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(dirPath, "file.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(root, "link.txt")
	if err := os.Symlink(filepath.Join("dir", "file.txt"), linkPath); err != nil {
		t.Fatal(err)
	}

	entries, err := SnapshotTree(root)
	if err != nil {
		t.Fatalf("SnapshotTree returned error: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}
	if entries[0].Path != "dir" || entries[0].Kind != "dir" {
		t.Fatalf("dir entry = %#v", entries[0])
	}
	if entries[1].Path != filepath.ToSlash(filepath.Join("dir", "file.txt")) || entries[1].Kind != "file" {
		t.Fatalf("file entry = %#v", entries[1])
	}
	sum := sha256.Sum256([]byte("hello"))
	if entries[1].SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("file sha256 = %q, want %q", entries[1].SHA256, hex.EncodeToString(sum[:]))
	}
	if entries[2].Path != "link.txt" || entries[2].Kind != "symlink" {
		t.Fatalf("symlink entry = %#v", entries[2])
	}
	if entries[2].LinkTarget != filepath.Join("dir", "file.txt") {
		t.Fatalf("symlink target = %q, want %q", entries[2].LinkTarget, filepath.Join("dir", "file.txt"))
	}
}

func TestCompareDirectoryTreesDetectsMismatch(t *testing.T) {
	t.Parallel()

	left := t.TempDir()
	right := t.TempDir()
	if err := os.WriteFile(filepath.Join(left, "value.txt"), []byte("left"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(right, "value.txt"), []byte("right"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := CompareDirectoryTrees(left, right)
	if err == nil {
		t.Fatal("CompareDirectoryTrees returned nil error")
	}
	if !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("error %q does not mention sha256 mismatch", err)
	}
}

func TestRunParityCaseComparesOutputsAndTrees(t *testing.T) {
	t.Parallel()

	leftRoot := t.TempDir()
	rightRoot := t.TempDir()
	script := `printf "same\n"; printf "same\n" >&2; mkdir -p "$1/out"; printf "payload\n" > "$1/out/data.txt"`
	caseSpec := ParityCase{
		Name: "demo",
		Left: CommandSpec{
			Path: "/bin/sh",
			Args: []string{"-c", script, "sh", leftRoot},
		},
		Right: CommandSpec{
			Path: "/bin/sh",
			Args: []string{"-c", script, "sh", rightRoot},
		},
		TreePairs: []TreePair{{LeftRoot: leftRoot, RightRoot: rightRoot}},
	}

	if err := RunParityCase(context.Background(), caseSpec); err != nil {
		t.Fatalf("RunParityCase returned error: %v", err)
	}
}

func TestParityScriptDetectsTreeMismatch(t *testing.T) {
	t.Parallel()

	leftRoot := t.TempDir()
	rightRoot := t.TempDir()
	script := `mkdir -p "$1/out"; printf "same\n"; printf "same\n" >&2; printf "left\n" > "$1/out/data.txt"`
	leftCmd := shellCommand(script, leftRoot)
	rightCmd := shellCommand(`mkdir -p "$1/out"; printf "same\n"; printf "same\n" >&2; printf "right\n" > "$1/out/data.txt"`, rightRoot)
	scriptPath := filepath.Join(repoRoot(t), "scripts", "verify-go-python-parity.sh")

	cmd := exec.Command(
		"bash",
		scriptPath,
		"--name",
		"tree-mismatch",
		"--python-cmd",
		leftCmd,
		"--go-cmd",
		rightCmd,
		"--compare-root",
		leftRoot+":"+rightRoot,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script succeeded unexpectedly: %s", out)
	}
	if !strings.Contains(string(out), "tree mismatch") {
		t.Fatalf("script output %q does not mention tree mismatch", out)
	}
}

func TestParityScriptPassesForMatchingCommandOutputs(t *testing.T) {
	t.Parallel()

	leftRoot := t.TempDir()
	rightRoot := t.TempDir()
	script := `printf "same\n"; printf "same\n" >&2; mkdir -p "$1/out"; printf "payload\n" > "$1/out/data.txt"`
	leftCmd := shellCommand(script, leftRoot)
	rightCmd := shellCommand(script, rightRoot)
	scriptPath := filepath.Join(repoRoot(t), "scripts", "verify-go-python-parity.sh")

	cmd := exec.Command(
		"bash",
		scriptPath,
		"--name",
		"parity-smoke",
		"--python-cmd",
		leftCmd,
		"--go-cmd",
		rightCmd,
		"--compare-root",
		leftRoot+":"+rightRoot,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "parity passed") {
		t.Fatalf("script output %q does not mention success", out)
	}
}
