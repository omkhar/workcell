// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

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

func runValidationSnapshotCommand(tb testing.TB, repo string, mode string, command string, extraEnv map[string]string) (int, string) {
	tb.Helper()

	script := filepath.Join(repoRoot(tb), "scripts", "with-validation-snapshot.sh")
	cmd := exec.Command(
		script,
		"--repo", repo,
		"--mode", mode,
		"--",
		"/bin/sh", "-c",
		command,
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

func runValidationSnapshot(tb testing.TB, repo string, extraEnv map[string]string) (int, string) {
	tb.Helper()
	return runValidationSnapshotCommand(
		tb,
		repo,
		"head",
		`pwd; printf '%s\n' "$WORKCELL_VALIDATION_SNAPSHOT_DIR"; if [ -d .git ]; then echo True; else echo False; fi`,
		extraEnv,
	)
}

func TestValidationSnapshotDefaultsToRepoParent(t *testing.T) {
	t.Parallel()

	tempRoot := t.TempDir()
	repo := createSnapshotFixtureRepo(t, tempRoot)

	code, stdout := runValidationSnapshot(t, repo, nil)
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

	code, stdout := runValidationSnapshot(t, repo, map[string]string{
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

func TestValidationSnapshotPreservesTrackedSymlinks(t *testing.T) {
	t.Parallel()

	tempRoot := t.TempDir()
	repo := createSnapshotFixtureRepo(t, tempRoot)
	linkPath := filepath.Join(repo, "link.txt")
	if err := os.Symlink("README.md", linkPath); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", repo, "add", "link.txt")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add link.txt failed: %v\n%s", err, output)
	}
	cmd = exec.Command("git", "-C", repo, "commit", "-q", "-m", "add symlink")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit add symlink failed: %v\n%s", err, output)
	}

	for _, mode := range []string{"head", "index", "worktree"} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			code, output := runValidationSnapshotCommand(
				t,
				repo,
				mode,
				`if [ -L link.txt ]; then printf 'target=%s\n' "$(readlink link.txt)"; else echo missing; fi`,
				nil,
			)
			if code != 0 {
				t.Fatalf("snapshot exit code = %d output=%q", code, output)
			}
			if strings.TrimSpace(output) != "target=README.md" {
				t.Fatalf("snapshot output = %q want target=README.md", output)
			}
		})
	}
}

func TestValidationSnapshotFailsWhenTrackedBlobIsUnreadable(t *testing.T) {
	t.Parallel()

	tempRoot := t.TempDir()
	repo := createSnapshotFixtureRepo(t, tempRoot)
	cmd := exec.Command("git", "-C", repo, "rev-parse", "HEAD:README.md")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse README blob failed: %v\n%s", err, output)
	}
	oid := strings.TrimSpace(string(output))
	objectPath := filepath.Join(repo, ".git", "objects", oid[:2], oid[2:])
	if err := os.Remove(objectPath); err != nil {
		t.Fatalf("remove blob object: %v", err)
	}

	code, snapshotOutput := runValidationSnapshotCommand(t, repo, "head", `echo should-not-run`, nil)
	if code == 0 {
		t.Fatalf("snapshot unexpectedly succeeded: %q", snapshotOutput)
	}
	if !strings.Contains(snapshotOutput, "failed to read blob") {
		t.Fatalf("snapshot output = %q want failed to read blob", snapshotOutput)
	}
}

func TestValidationSnapshotPreservesGitIndexState(t *testing.T) {
	t.Parallel()

	tempRoot := t.TempDir()
	repo := createSnapshotFixtureRepo(t, tempRoot)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("index\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", repo, "add", "README.md")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add README.md failed: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("worktree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		mode          string
		expectedIndex string
		expectedFile  string
	}{
		{mode: "head", expectedIndex: "fixture", expectedFile: "fixture"},
		{mode: "index", expectedIndex: "index", expectedFile: "index"},
		{mode: "worktree", expectedIndex: "index", expectedFile: "worktree"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.mode, func(t *testing.T) {
			t.Parallel()

			code, output := runValidationSnapshotCommand(
				t,
				repo,
				tc.mode,
				`printf 'index=%s\n' "$(git show :README.md | tr -d '\n')"; printf 'worktree=%s\n' "$(tr -d '\n' < README.md)"`,
				nil,
			)
			if code != 0 {
				t.Fatalf("snapshot exit code = %d output=%q", code, output)
			}
			expected := "index=" + tc.expectedIndex + "\nworktree=" + tc.expectedFile + "\n"
			if output != expected {
				t.Fatalf("snapshot output = %q want %q", output, expected)
			}
		})
	}
}

func TestValidationSnapshotPreservesIntentToAddState(t *testing.T) {
	t.Parallel()

	tempRoot := t.TempDir()
	repo := createSnapshotFixtureRepo(t, tempRoot)
	if err := os.WriteFile(filepath.Join(repo, "planned.txt"), []byte("planned\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", repo, "add", "-N", "planned.txt")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add -N planned.txt failed: %v\n%s", err, output)
	}

	cases := []struct {
		mode          string
		expectedIndex string
		expectedDiff  string
		expectedFile  string
	}{
		{mode: "index", expectedIndex: "present", expectedDiff: "", expectedFile: "missing"},
		{mode: "worktree", expectedIndex: "present", expectedDiff: "", expectedFile: "present"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.mode, func(t *testing.T) {
			t.Parallel()

			code, output := runValidationSnapshotCommand(
				t,
				repo,
				tc.mode,
				`if git ls-files --error-unmatch planned.txt >/dev/null 2>&1; then echo index=present; else echo index=missing; fi; printf 'cached=%s\n' "$(git diff --cached --name-status -- planned.txt | tr -d '\n')"; if [ -e planned.txt ]; then echo file=present; else echo file=missing; fi`,
				nil,
			)
			if code != 0 {
				t.Fatalf("snapshot exit code = %d output=%q", code, output)
			}
			expected := "index=" + tc.expectedIndex + "\ncached=" + tc.expectedDiff + "\nfile=" + tc.expectedFile + "\n"
			if output != expected {
				t.Fatalf("snapshot output = %q want %q", output, expected)
			}
		})
	}
}

func TestValidationSnapshotPreservesIntentToAddWithoutWorktreeCopy(t *testing.T) {
	t.Parallel()

	tempRoot := t.TempDir()
	repo := createSnapshotFixtureRepo(t, tempRoot)
	if err := os.WriteFile(filepath.Join(repo, "planned.txt"), []byte("planned\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", repo, "add", "-N", "planned.txt")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add -N planned.txt failed: %v\n%s", err, output)
	}
	if err := os.Remove(filepath.Join(repo, "planned.txt")); err != nil {
		t.Fatal(err)
	}

	code, output := runValidationSnapshotCommand(
		t,
		repo,
		"index",
		`if git ls-files --error-unmatch planned.txt >/dev/null 2>&1; then echo index=present; else echo index=missing; fi; printf 'cached=%s\n' "$(git diff --cached --name-status -- planned.txt | tr -d '\n')"; if [ -e planned.txt ]; then echo file=present; else echo file=missing; fi`,
		nil,
	)
	if code != 0 {
		t.Fatalf("snapshot exit code = %d output=%q", code, output)
	}
	expected := "index=present\ncached=\nfile=missing\n"
	if output != expected {
		t.Fatalf("snapshot output = %q want %q", output, expected)
	}
}

func TestValidationSnapshotPreservesIgnoredIntentToAddState(t *testing.T) {
	t.Parallel()

	tempRoot := t.TempDir()
	repo := createSnapshotFixtureRepo(t, tempRoot)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("planned.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", repo, "add", ".gitignore")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add .gitignore failed: %v\n%s", err, output)
	}
	cmd = exec.Command("git", "-C", repo, "commit", "-q", "-m", "add ignore")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit add ignore failed: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(repo, "planned.txt"), []byte("planned\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("git", "-C", repo, "add", "-N", "-f", "planned.txt")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add -N -f planned.txt failed: %v\n%s", err, output)
	}

	code, output := runValidationSnapshotCommand(
		t,
		repo,
		"index",
		`if git ls-files --error-unmatch planned.txt >/dev/null 2>&1; then echo index=present; else echo index=missing; fi; printf 'cached=%s\n' "$(git diff --cached --name-status -- planned.txt | tr -d '\n')"; if [ -e planned.txt ]; then echo file=present; else echo file=missing; fi`,
		nil,
	)
	if code != 0 {
		t.Fatalf("snapshot exit code = %d output=%q", code, output)
	}
	expected := "index=present\ncached=\nfile=missing\n"
	if output != expected {
		t.Fatalf("snapshot output = %q want %q", output, expected)
	}
}
