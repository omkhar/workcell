package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func resolvedPath(tb testing.TB, path string) string {
	tb.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}

func createSnapshotFixtureRepo(tb testing.TB, root string) string {
	tb.Helper()

	repo := filepath.Join(root, "fixture-repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		tb.Fatal(err)
	}

	run := func(args ...string) {
		tb.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			tb.Fatalf("%v failed: %v\n%s", args, err, output)
		}
	}

	run("git", "init", "-q", repo)
	run("git", "-C", repo, "config", "user.name", "Workcell Tests")
	run("git", "-C", repo, "config", "user.email", "workcell-tests@example.com")
	run("git", "-C", repo, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		tb.Fatal(err)
	}
	run("git", "-C", repo, "add", "README.md")
	run("git", "-C", repo, "commit", "-q", "-m", "init")
	return repo
}

func runValidationSnapshot(tb testing.TB, repo string, mode string, command string, extraEnv map[string]string) (int, string) {
	tb.Helper()

	script := filepath.Join(repoRoot(tb), "scripts", "with-validation-snapshot.sh")
	cmd := exec.Command(
		script,
		"--repo", repo,
		"--mode", mode,
		"--",
		"/bin/sh", "-c", command,
	)
	cmd.Env = os.Environ()
	for key, value := range extraEnv {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	output, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(output)
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), string(output)
	}
	tb.Fatalf("snapshot command failed: %v", err)
	return 0, ""
}

func TestValidationSnapshotDefaultsToRepoParent(t *testing.T) {
	t.Parallel()

	tempRoot := t.TempDir()
	repo := createSnapshotFixtureRepo(t, tempRoot)

	code, stdout := runValidationSnapshot(
		t,
		repo,
		"head",
		`pwd; printf '%s\n' "$WORKCELL_VALIDATION_SNAPSHOT_DIR"; if [ -d .git ]; then echo True; else echo False; fi`,
		nil,
	)
	if code != 0 {
		t.Fatalf("snapshot exit code = %d stdout=%q", code, stdout)
	}

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("unexpected snapshot output: %q", stdout)
	}
	cwd := lines[0]
	snapshotDir := lines[1]
	gitDir := lines[2]

	if cwd != snapshotDir {
		t.Fatalf("cwd = %q want snapshotDir %q", cwd, snapshotDir)
	}
	if resolvedPath(t, filepath.Dir(snapshotDir)) != resolvedPath(t, filepath.Dir(repo)) {
		t.Fatalf("snapshot parent = %q want %q", filepath.Dir(snapshotDir), filepath.Dir(repo))
	}
	if gitDir != "True" {
		t.Fatalf("git dir flag = %q want True", gitDir)
	}
}

func TestValidationSnapshotParentOverrideIsHonored(t *testing.T) {
	t.Parallel()

	tempRoot := t.TempDir()
	repo := createSnapshotFixtureRepo(t, tempRoot)
	overrideParent := filepath.Join(tempRoot, "snapshots")
	if err := os.Mkdir(overrideParent, 0o755); err != nil {
		t.Fatal(err)
	}

	code, stdout := runValidationSnapshot(t, repo, "head", `pwd; printf '%s\n' "$WORKCELL_VALIDATION_SNAPSHOT_DIR"; if [ -d .git ]; then echo True; else echo False; fi`, map[string]string{
		"WORKCELL_VALIDATION_SNAPSHOT_PARENT": overrideParent,
	})
	if code != 0 {
		t.Fatalf("snapshot exit code = %d stdout=%q", code, stdout)
	}

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("unexpected snapshot output: %q", stdout)
	}
	snapshotDir := lines[1]
	gitDir := lines[2]

	if resolvedPath(t, filepath.Dir(snapshotDir)) != resolvedPath(t, overrideParent) {
		t.Fatalf("snapshot parent = %q want %q", filepath.Dir(snapshotDir), overrideParent)
	}
	if gitDir != "True" {
		t.Fatalf("git dir flag = %q want True", gitDir)
	}
}

func TestValidationSnapshotRejectsTrackedSymlink(t *testing.T) {
	t.Parallel()

	tempRoot := t.TempDir()
	repo := createSnapshotFixtureRepo(t, tempRoot)
	targetPath := filepath.Join(repo, "README-link.md")
	if err := os.Symlink("README.md", targetPath); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v failed: %v\n%s", args, err, output)
		}
	}
	run("git", "-C", repo, "add", "README-link.md")
	run("git", "-C", repo, "commit", "-q", "-m", "add symlink")

	code, output := runValidationSnapshot(t, repo, "head", `echo should-not-run`, nil)
	if code == 0 {
		t.Fatalf("snapshot unexpectedly succeeded: %q", output)
	}
	if !strings.Contains(output, "tracked symlinks are unsupported in HEAD snapshots") {
		t.Fatalf("snapshot output %q did not mention tracked symlink rejection", output)
	}
}

func TestValidationSnapshotRejectsUnreadableTrackedBlob(t *testing.T) {
	t.Parallel()

	tempRoot := t.TempDir()
	repo := createSnapshotFixtureRepo(t, tempRoot)
	broken := filepath.Join(repo, "broken.txt")
	if err := os.WriteFile(broken, []byte("broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v failed: %v\n%s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}

	run("git", "-C", repo, "add", "broken.txt")
	run("git", "-C", repo, "commit", "-q", "-m", "add broken file")
	oid := run("git", "-C", repo, "rev-parse", "HEAD:broken.txt")
	objectPath := filepath.Join(repo, ".git", "objects", oid[:2], oid[2:])
	if err := os.Remove(objectPath); err != nil {
		t.Fatal(err)
	}

	code, output := runValidationSnapshot(t, repo, "head", `echo should-not-run`, nil)
	if code == 0 {
		t.Fatalf("snapshot unexpectedly succeeded: %q", output)
	}
	if !strings.Contains(output, "failed to read blob") {
		t.Fatalf("snapshot output %q did not mention unreadable blob rejection", output)
	}
}
