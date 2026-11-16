// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain lets a test run this binary as the hostutil program, as
// clone-session-workspace runs itself for run-in-directory-fd.
func TestMain(m *testing.M) {
	if os.Getenv("WORKCELL_HOSTUTIL_TEST_RUN_MAIN") == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestCloneSessionWorkspaceRunsGitInThePinnedDirectory clones a real
// repository through the helper: git runs in the session directory that the
// child reached with fchdir, and the clone holds the source commit.
func TestCloneSessionWorkspaceRunsGitInThePinnedDirectory(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("WORKCELL_HOSTUTIL_TEST_RUN_MAIN", "1")
	root := t.TempDir()
	source := filepath.Join(root, "source")
	for _, args := range [][]string{
		{"init", "-q", source},
		{"-C", source, "-c", "user.email=probe@example.com", "-c", "user.name=probe", "commit", "-q", "--allow-empty", "-m", "seed"},
	} {
		if output, err := exec.Command(gitBin, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	parent := filepath.Join(source, ".git", "workcell-sessions")
	if err := runHelper([]string{"clone-session-workspace", parent, "s1", gitBin, source}); err != nil {
		t.Fatal(err)
	}
	want, err := exec.Command(gitBin, "-C", source, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	got, err := exec.Command(gitBin, "-C", filepath.Join(parent, "s1", "repo"), "rev-parse", "HEAD").Output()
	if err != nil || string(got) != string(want) {
		t.Fatalf("clone HEAD = %q, %v; want %q", got, err, want)
	}
	if err := runHelper([]string{"clone-session-workspace", parent, "s2", "git", source}); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("a relative git path error = %v", err)
	}
	if err := runHelper([]string{"clone-session-workspace", parent, "s2", gitBin, "source"}); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("a relative source path error = %v", err)
	}
}

// TestRunInDirectoryFDRefusesWithoutADirectory covers the child side: with no
// directory on descriptor 3, or a relative program, it runs nothing.
func TestRunInDirectoryFDRefusesWithoutADirectory(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(t.TempDir(), "file"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for name, extra := range map[string][]*os.File{"no descriptor": nil, "a file descriptor": {file}} {
		cmd := exec.Command(self, "helper", "run-in-directory-fd", "/bin/echo", "ran")
		cmd.Env = append(os.Environ(), "WORKCELL_HOSTUTIL_TEST_RUN_MAIN=1")
		cmd.ExtraFiles = extra
		output, err := cmd.CombinedOutput()
		if err == nil || strings.Contains(string(output), "ran\n") {
			t.Errorf("%s: err %v, output %q", name, err, output)
		}
	}
	cmd := exec.Command(self, "helper", "run-in-directory-fd", "echo", "ran")
	cmd.Env = append(os.Environ(), "WORKCELL_HOSTUTIL_TEST_RUN_MAIN=1")
	cmd.ExtraFiles = []*os.File{file}
	if output, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), "absolute") {
		t.Errorf("relative program: err %v, output %q", err, output)
	}
}
