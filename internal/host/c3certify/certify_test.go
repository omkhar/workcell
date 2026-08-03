// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package c3certify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omkhar/workcell/internal/host/sessions"
)

func TestExecuteProvesAndCleansTwoSessions(t *testing.T) {
	control, _ := newGitRepo(t)
	workload, commit := newGitRepo(t)
	docker := filepath.Join(control, "docker")
	mustNoError(t, os.WriteFile(docker, []byte("fixture\n"), 0o700))
	runGit(t, control, "add", "docker")
	runGit(t, control, "commit", "--quiet", "-m", "docker fixture")
	c := testCertifier(t, workload)
	c.options.Root, c.workcell, c.docker, c.colima = control, filepath.Join(control, "tracked"), docker, "/fake/colima"
	c.scratchRoot, _ = newGitRepo(t)
	c.launchRoot, c.dockerConfig = filepath.Join(c.scratchRoot, "repo"), t.TempDir()
	ownedPaths := append(colimaProfilePaths(c.colimaRoot, c.profile), filepath.Join(c.stateRoot, "targets", "local_vm", "colima", c.profile))
	for _, path := range ownedPaths {
		mustNoError(t, os.MkdirAll(path, 0o700))
	}
	cachePaths, err := runtimeImageCachePaths(c.stateRoot, c.profile)
	mustNoError(t, err)
	for _, path := range append(cachePaths, filepath.Join(filepath.Dir(cachePaths[0]), c.profile+".pending.tar")) {
		mustNoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		mustNoError(t, os.WriteFile(path, []byte("owned cache"), 0o600))
		ownedPaths = append(ownedPaths, path)
	}
	records, paths := map[string]sessions.SessionRecord{}, map[string]string{}
	stopped, deleted := map[string]bool{}, map[string]bool{}
	var reapedProfile string
	c.deps.reapProfileProcesses = func(_ context.Context, profile string) error { reapedProfile = profile; return nil }
	c.deps.command = func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		if name == c.git {
			return runCommand(ctx, env, name, args...)
		}
		if name == c.workcell {
			switch args[1] {
			case "start":
				id := fmt.Sprintf("session-%d", len(records)+1)
				record := newIsolatedRecord(t, c.launchRoot, commit, id, "workcell-"+id)
				records[id] = record
				paths[id] = writeTestRecord(t, c, record)
				return []byte("session_id=" + id + "\n"), nil
			case "stop":
				id := args[3]
				record := records[id]
				record.Status, record.LiveStatus, record.FinishedAt, record.ExitStatus = "exited", "stopped", "now", "0"
				record.FinalAssurance, stopped[id] = "managed-mutable", true
				records[id] = record
				paths[id] = writeTestRecord(t, c, record)
				return nil, nil
			case "delete":
				id := args[3]
				deleted[id] = true
				return nil, os.Remove(paths[id])
			}
		}
		if name == c.docker {
			if args[0] == "exec" {
				if strings.Contains(args[4], "printf") {
					for _, record := range records {
						if record.ContainerName == args[1] {
							mustNoError(t, os.WriteFile(filepath.Join(record.WorktreePath, args[len(args)-1]),
								[]byte(args[len(args)-2]+"\n"), 0o600))
						}
					}
				}
				return nil, nil
			}
			if args[0] == "inspect" {
				for id, record := range records {
					if record.ContainerName == args[len(args)-1] && !deleted[id] {
						return []byte(map[bool]string{true: "false\n", false: "true\n"}[stopped[id]]), nil
					}
				}
				return nil, errors.New("not found")
			}
			if args[0] == "ps" {
				var names []string
				for id, record := range records {
					if !deleted[id] {
						names = append(names, record.ContainerName)
					}
				}
				return []byte(strings.Join(names, "\n")), nil
			}
		}
		if name == c.colima {
			if args[0] == "list" {
				return []byte("{\"name\":\"other\",\"status\":\"Running\"}\n"), nil
			}
			return nil, nil
		}
		return nil, errors.New("unexpected command")
	}
	if err := c.execute(context.Background(), new(strings.Builder)); err != nil {
		t.Fatalf("execute error = %v", err)
	}
	if len(records) != 2 || !deleted["session-1"] || !deleted["session-2"] || reapedProfile != c.profile {
		t.Fatalf("incomplete certification: deleted=%v reaped=%s", deleted, reapedProfile)
	}
	for _, path := range ownedPaths {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("owned profile residue %s: %v", path, err)
		}
	}
}

func TestGitCommandDisablesRepositoryExecutionHooks(t *testing.T) {
	workspace, _ := newGitRepo(t)
	marker, fsmonitor := filepath.Join(t.TempDir(), "fsmonitor-ran"), filepath.Join(t.TempDir(), "fsmonitor.sh")
	mustNoError(t, os.WriteFile(fsmonitor, []byte("#!/bin/sh\ntouch \""+marker+"\"\n"), 0o700))
	runGit(t, workspace, "config", "core.fsmonitor", fsmonitor)
	if _, err := testCertifier(t, workspace).gitCommand(context.Background(), workspace, "status", "--porcelain=v1"); err != nil {
		t.Fatalf("gitCommand status error = %v", err)
	}
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repository-local fsmonitor executed on the host: %v", err)
	}
}

func testCertifier(t *testing.T, workspace string) *certifier {
	git, err := exec.LookPath("git")
	mustNoError(t, err)
	return &certifier{
		options: Options{Root: workspace, Workspace: workspace, PollAttempts: 2,
			PollInterval: time.Millisecond, CommandTimeout: 10 * time.Second},
		deps: dependencies{
			command: runCommand, now: func() time.Time { return time.Unix(0, 0) },
			sleep:                func(context.Context, time.Duration) error { return nil },
			socketExists:         func(string) error { return nil },
			reapProfileProcesses: func(context.Context, string) error { return nil },
		},
		git: git, colima: "/fake/colima",
		stateRoot:   t.TempDir(),
		colimaRoot:  t.TempDir(),
		scratchRoot: workspace,
		launchRoot:  workspace,
		profile:     "wcl-profile",
		baseEnv:     append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1"),
	}
}
func mustNoError(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}
func newGitRepo(t *testing.T) (string, string) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	mustNoError(t, err)
	runGit(t, root, "init", "--quiet")
	runGit(t, root, "config", "user.name", "Workcell Test")
	runGit(t, root, "config", "user.email", "workcell-test@example.invalid")
	mustNoError(t, os.WriteFile(filepath.Join(root, "tracked"), []byte("base\n"), 0o600))
	runGit(t, root, "add", "tracked")
	runGit(t, root, "commit", "--quiet", "-m", "base")
	return root, strings.TrimSpace(string(runGit(t, root, "rev-parse", "HEAD")))
}
func newIsolatedRecord(t *testing.T, workspace, commit, id, container string) sessions.SessionRecord {
	gitDir := strings.TrimSpace(string(runGit(t, workspace, "rev-parse", "--absolute-git-dir")))
	worktree := filepath.Join(gitDir, "workcell-sessions", id, "repo")
	mustNoError(t, os.MkdirAll(filepath.Dir(worktree), 0o755))
	runGit(t, workspace, "clone", "--quiet", "--no-hardlinks", workspace, worktree)
	branch := "workcell/session-" + id
	runGit(t, worktree, "checkout", "--quiet", "-B", branch, commit)
	return sessions.SessionRecord{
		Version: 1, SessionID: id, Profile: "wcl-profile", TargetKind: "local_vm",
		TargetProvider: "colima", TargetID: "wcl-profile", TargetAssuranceClass: "strict",
		RuntimeAPI: "docker", WorkspaceTransport: "isolated-worktree-mount", Agent: "codex", Mode: "strict",
		Status: "running", LiveStatus: "running", StartedAt: "1970-01-01T00:00:00Z",
		ExecutionPath: "lower-assurance-debug-command", Workspace: worktree, WorkspaceOrigin: workspace,
		WorktreePath: worktree, GitBranch: branch, ContainerName: container, CurrentAssurance: "managed-mutable",
	}
}

func writeTestRecord(t *testing.T, c *certifier, record sessions.SessionRecord) string {
	path := filepath.Join(c.stateRoot, "targets", "local_vm", "colima",
		record.Profile, "sessions", record.SessionID+".json")
	mustNoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	data, err := json.Marshal(record)
	mustNoError(t, err)
	mustNoError(t, os.WriteFile(path, data, 0o600))
	return path
}
func runGit(t *testing.T, dir string, args ...string) []byte {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return output
}
