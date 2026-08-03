// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package c3certify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/omkhar/workcell/internal/host/sessions"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestWriteReportReturnsWriterError(t *testing.T) {
	item := &evidence{}
	if err := writeReport(failingWriter{}, snapshot{}, item, item, time.Time{}); err == nil {
		t.Fatal("writeReport hid writer failure")
	}
}

func TestParseStartSessionID(t *testing.T) {
	got, err := parseStartSessionID([]byte("session_id=session-a\nstatus=running\n"))
	if err != nil || got != "session-a" {
		t.Fatalf("parseStartSessionID = %q, %v", got, err)
	}
	for _, output := range []string{
		"status=running\n", "session_id=a\nsession_id=b\n", "session_id=--all\n",
	} {
		if _, err := parseStartSessionID([]byte(output)); err == nil {
			t.Fatalf("parseStartSessionID accepted %q", output)
		}
	}
}

func TestValidatePairPinsStrictIsolationIdentity(t *testing.T) {
	workspace, commit := newGitRepo(t)
	a := newIsolatedRecord(t, workspace, commit, "session-a", "workcell-a")
	b := newIsolatedRecord(t, workspace, commit, "session-b", "workcell-b")
	c := testCertifier(t, workspace)
	if err := c.validatePair(context.Background(), a, b, commit); err != nil {
		t.Fatalf("validatePair error = %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*sessions.SessionRecord, *sessions.SessionRecord)
		want   string
	}{
		{"provider", func(a, _ *sessions.SessionRecord) { a.TargetProvider = "docker-desktop" }, "target_provider"},
		{"target assurance", func(_, b *sessions.SessionRecord) { b.TargetAssuranceClass = "compat" }, "target_assurance_class"},
		{"effective assurance", func(a, _ *sessions.SessionRecord) { a.CurrentAssurance = "lower-assurance-package-mutation" }, "assurance"},
		{"mode", func(a, _ *sessions.SessionRecord) { a.Mode = "development" }, "mode"},
		{"transport", func(a, _ *sessions.SessionRecord) { a.WorkspaceTransport = "virtiofs" }, "workspace_transport"},
		{"execution path", func(_, b *sessions.SessionRecord) { b.ExecutionPath = "managed-tier1" }, "execution_path"},
		{"shared container", func(a, b *sessions.SessionRecord) { b.ContainerName = a.ContainerName }, "containers"},
		{"shared worktree", func(a, b *sessions.SessionRecord) { b.WorktreePath = a.WorktreePath }, "recorded worktree"},
		{"shared branch", func(a, b *sessions.SessionRecord) { b.GitBranch = a.GitBranch }, "isolated branch"},
		{"different profile", func(_, b *sessions.SessionRecord) { b.Profile = "other" }, "profile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotA, gotB := a, b
			tc.mutate(&gotA, &gotB)
			err := c.validatePair(context.Background(), gotA, gotB, commit)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validatePair error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestStartTimeoutAdoptsOwnedRecord(t *testing.T) {
	workspace, commit := newGitRepo(t)
	c := testCertifier(t, workspace)
	c.workcell = "/fake/workcell"
	c.options.CommandTimeout = time.Millisecond
	c.deps.command = func(startCtx context.Context, env []string, name string, args ...string) ([]byte, error) {
		if name == c.git {
			return runCommand(startCtx, env, name, args...)
		}
		<-startCtx.Done()
		c.options.CommandTimeout = 10 * time.Second
		record := newIsolatedRecord(t, workspace, commit, "session-a", "workcell-a")
		writeTestRecord(t, c, record)
		return nil, startCtx.Err()
	}
	if _, err := c.start(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("start error = %v, want deadline", err)
	}
	if len(c.sessions) != 1 || c.sessions[0].record.SessionID != "session-a" {
		t.Fatalf("adopted sessions = %+v, want timed-out launch evidence", c.sessions)
	}
}

func TestCaptureSnapshotPinsPrecommitTree(t *testing.T) {
	control, _ := newGitRepo(t)
	workload, _ := newGitRepo(t)
	c := testCertifier(t, workload)
	c.options.Root, c.workcell, c.docker = control, filepath.Join(control, "tracked"), filepath.Join(control, "tracked")
	mustNoError(t, os.WriteFile(c.workcell, []byte("indexed\n"), 0o600))
	runGit(t, control, "add", "tracked")
	c.options.PrecommitControlTree = strings.TrimSpace(string(runGit(t, control, "write-tree")))
	if _, err := c.captureSnapshot(context.Background()); err != nil {
		t.Fatalf("captureSnapshot error = %v", err)
	}
	c.options.PrecommitControlTree = strings.Repeat("0", 40)
	if _, err := c.captureSnapshot(context.Background()); err == nil {
		t.Fatal("captureSnapshot accepted mismatched tree")
	}
	c.options.PrecommitControlTree = strings.TrimSpace(string(runGit(t, control, "write-tree")))
	mustNoError(t, os.WriteFile(filepath.Join(control, "untracked"), nil, 0o600))
	if _, err := c.captureSnapshot(context.Background()); err == nil {
		t.Fatal("captureSnapshot accepted untracked residue")
	}
	mustNoError(t, os.Remove(filepath.Join(control, "untracked")))
	mustNoError(t, os.WriteFile(c.workcell, []byte("dirty\n"), 0o600))
	if _, err := c.captureSnapshot(context.Background()); err == nil {
		t.Fatal("captureSnapshot accepted tracked worktree changes")
	}
}

func TestExecuteJoinsPrimaryAndCleanupErrors(t *testing.T) {
	c := testCertifier(t, t.TempDir())
	c.dockerConfig = filepath.Join("/dev/null", "child")
	c.deps.command = func(context.Context, []string, string, ...string) ([]byte, error) {
		return nil, errors.New("primary failure")
	}
	err := c.execute(context.Background(), new(strings.Builder))
	if err == nil || !strings.Contains(err.Error(), "primary failure") ||
		!strings.Contains(err.Error(), "cleanup verification failed") {
		t.Fatalf("execute error = %v, want joined primary and cleanup failures", err)
	}
	if _, err := os.Stat(c.scratchRoot); err != nil {
		t.Fatalf("cleanup failure removed owned evidence: %v", err)
	}
}

func TestExecuteProvesAndCleansTwoSessions(t *testing.T) {
	control, _ := newGitRepo(t)
	workload, commit := newGitRepo(t)
	docker := filepath.Join(control, "docker")
	mustNoError(t, os.WriteFile(docker, []byte("fixture\n"), 0o700))
	runGit(t, control, "add", "docker")
	runGit(t, control, "-c", "user.name=Workcell Test", "-c", "user.email=workcell-test@example.invalid", "commit", "--quiet", "-m", "docker fixture")
	c := testCertifier(t, workload)
	c.options.Root, c.workcell, c.docker, c.colima = control, filepath.Join(control, "tracked"), docker, "/fake/colima"
	infoExclude := filepath.Join(control, strings.TrimSpace(string(runGit(t, control, "rev-parse", "--git-path", "info/exclude"))))
	runGit(t, control, "rm", "--cached", "tracked")
	mustNoError(t, os.WriteFile(infoExclude, []byte("tracked\n"), 0o600))
	c.options.PrecommitControlTree = strings.TrimSpace(string(runGit(t, control, "write-tree")))
	if _, err := c.captureSnapshot(context.Background()); err == nil {
		t.Fatal("pre-commit snapshot accepted info-excluded launcher replacement")
	}
	mustNoError(t, os.Remove(infoExclude))
	runGit(t, control, "add", "tracked")
	c.options.PrecommitControlTree = ""
	c.baseEnv = append(c.baseEnv, "WORKCELL_STATE_ROOT="+c.stateRoot)
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
	var markerCommands []string
	var reapedProfile string
	c.deps.reapProfileProcesses = func(_ context.Context, profile string) error { reapedProfile = profile; return nil }
	c.deps.command = func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		if name == c.git {
			return runCommand(ctx, env, name, args...)
		}
		if name == "/bin/bash" && args[0] == c.workcell {
			args = args[1:]
			if !strings.Contains(strings.Join(env, "\n"), "WORKCELL_STATE_ROOT="+c.stateRoot) {
				return nil, errors.New("state-root override was not forwarded")
			}
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
				markerCommands = append(markerCommands, strings.Join(args, "\x00"))
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
	wantMarkerCommands := []string{
		strings.Join([]string{"exec", "workcell-session-1", "/bin/sh", "-lc",
			`printf "%s\n" "$1" >"/workspace/$2"`, "sh", "session-a-only", ".workcell-c3-session-1"}, "\x00"),
		strings.Join([]string{"exec", "workcell-session-2", "/bin/sh", "-lc",
			`test ! -e "/workspace/$1"`, "sh", ".workcell-c3-session-1"}, "\x00"),
		strings.Join([]string{"exec", "workcell-session-2", "/bin/sh", "-lc",
			`printf "%s\n" "$1" >"/workspace/$2"`, "sh", "session-b-only", ".workcell-c3-session-2"}, "\x00"),
		strings.Join([]string{"exec", "workcell-session-1", "/bin/sh", "-lc",
			`test ! -e "/workspace/$1"`, "sh", ".workcell-c3-session-2"}, "\x00"),
	}
	if !slices.Equal(markerCommands, wantMarkerCommands) {
		t.Fatalf("marker commands = %q, want symmetric A/B proof %q", markerCommands, wantMarkerCommands)
	}
	for _, path := range ownedPaths {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("owned profile residue %s: %v", path, err)
		}
	}
}

func TestProveMarkerRejectsWrongContainerMount(t *testing.T) {
	c := testCertifier(t, t.TempDir())
	c.docker = "/fake/docker"
	present := sessions.SessionRecord{
		SessionID: "session-b", Profile: c.profile, ContainerName: "workcell-b", WorktreePath: t.TempDir(),
	}
	absent := sessions.SessionRecord{
		SessionID: "session-a", Profile: c.profile, ContainerName: "workcell-a", WorktreePath: t.TempDir(),
	}
	wrongMount := t.TempDir()
	c.deps.command = func(_ context.Context, _ []string, _ string, args ...string) ([]byte, error) {
		if args[0] == "exec" && strings.Contains(args[4], "printf") {
			mustNoError(t, os.WriteFile(filepath.Join(wrongMount, args[len(args)-1]),
				[]byte(args[len(args)-2]+"\n"), 0o600))
		}
		return nil, nil
	}
	if err := c.proveMarker(context.Background(), present, absent, ".marker-b", "session-b-only"); err == nil {
		t.Fatal("proveMarker accepted a container mounted to an unrelated empty directory")
	}
}

func TestCleanupDoesNotStopUnvalidatedSessionID(t *testing.T) {
	for _, test := range []struct {
		name  string
		write bool
	}{
		{name: "absent record"},
		{name: "mismatched record", write: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace, commit := newGitRepo(t)
			c := testCertifier(t, workspace)
			c.workcell = "/fake/workcell"
			c.sessions = []*evidence{{record: sessions.SessionRecord{SessionID: "foreign"}}}
			if test.write {
				record := newIsolatedRecord(t, workspace, commit, "foreign", "workcell-foreign")
				record.Profile, record.TargetID = "other-profile", "other-profile"
				writeTestRecord(t, c, record)
			}
			var workcellCalls int
			c.deps.command = func(_ context.Context, _ []string, name string, _ ...string) ([]byte, error) {
				if name == c.workcell {
					workcellCalls++
				}
				return nil, errors.New("unexpected command")
			}
			_ = c.cleanup(context.Background())
			if workcellCalls != 0 {
				t.Fatalf("cleanup issued %d Workcell calls for an unvalidated id", workcellCalls)
			}
		})
	}
}

func TestDockerCommandRejectsSymlinkedSocketPaths(t *testing.T) {
	t.Run("profile parent", func(t *testing.T) {
		c := testCertifier(t, t.TempDir())
		c.docker = "/fake/docker"
		mustNoError(t, os.Symlink(t.TempDir(), filepath.Join(c.colimaRoot, c.profile)))
		if _, err := c.dockerCommand(context.Background(), c.profile, "ps"); err == nil {
			t.Fatal("dockerCommand accepted a symlinked profile directory")
		}
	})
	t.Run("socket", func(t *testing.T) {
		c := testCertifier(t, t.TempDir())
		c.docker = "/fake/docker"
		profileRoot := filepath.Join(c.colimaRoot, c.profile)
		mustNoError(t, os.Mkdir(profileRoot, 0o700))
		socketRoot, err := os.MkdirTemp("/tmp", "c3sock.")
		mustNoError(t, err)
		t.Cleanup(func() { _ = os.RemoveAll(socketRoot) })
		socketTarget := filepath.Join(socketRoot, "docker.sock")
		listener, err := net.Listen("unix", socketTarget)
		mustNoError(t, err)
		t.Cleanup(func() { _ = listener.Close() })
		requireSocket := func(path string) error {
			info, err := os.Lstat(path)
			if err == nil && info.Mode()&os.ModeSocket == 0 {
				return errors.New("not a socket")
			}
			return err
		}
		mustNoError(t, requireSocket(socketTarget))
		mustNoError(t, os.Symlink(socketTarget, filepath.Join(profileRoot, "docker.sock")))
		c.deps.socketExists = requireSocket
		if _, err := c.dockerCommand(context.Background(), c.profile, "ps"); err == nil {
			t.Fatal("dockerCommand accepted a symlinked socket")
		}
	})
}

func TestRuntimeImageCachePathsTreatsStateRootLiterally(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state[meta]*?")
	cacheRoot := filepath.Join(stateRoot, "runtime-image-cache", "local_vm", "colima")
	mustNoError(t, os.MkdirAll(cacheRoot, 0o700))
	profile := "wcl-c3-test"
	tempCache := filepath.Join(cacheRoot, profile+".pending.tar")
	unrelated := filepath.Join(cacheRoot, "other.pending.tar")
	mustNoError(t, os.WriteFile(tempCache, nil, 0o600))
	mustNoError(t, os.WriteFile(unrelated, nil, 0o600))
	paths, err := runtimeImageCachePaths(stateRoot, profile)
	mustNoError(t, err)
	if !slices.Contains(paths, tempCache) || slices.Contains(paths, unrelated) {
		t.Fatalf("runtime cache paths = %q", paths)
	}
}

func TestNewCertifierCreatesMissingColimaRoot(t *testing.T) {
	home, root, workspace := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WORKCELL_STATE_ROOT", t.TempDir())
	mustNoError(t, os.MkdirAll(filepath.Join(root, "scripts"), 0o700))
	mustNoError(t, os.WriteFile(filepath.Join(root, "scripts", "workcell"), nil, 0o700))
	c, err := newCertifier(Options{Root: root, Workspace: workspace}, dependencies{})
	if err == nil {
		t.Cleanup(func() {
			_ = os.RemoveAll(c.dockerConfig)
			_ = os.RemoveAll(c.scratchRoot)
		})
	}
	info, err := os.Lstat(filepath.Join(home, ".colima"))
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("created Colima root = %v, %v", info, err)
	}
}

func TestRunRejectsInvalidOptions(t *testing.T) {
	if err := Run(context.Background(), Options{PollAttempts: -1}, io.Discard); err == nil {
		t.Fatal("Run accepted invalid polling options")
	}
}

func TestCleanupPathProofsFailClosed(t *testing.T) {
	workspace, commit := newGitRepo(t)
	record := newIsolatedRecord(t, workspace, commit, "session-a", "workcell-a")
	c := testCertifier(t, workspace)
	record.WorktreePath = ""
	if err := c.validateCleanupRecord(context.Background(), record); err == nil {
		t.Fatal("cleanup accepted empty recorded path")
	}
	record = newIsolatedRecord(t, workspace, commit, "session-b", "workcell-b")
	parent := filepath.Dir(filepath.Dir(record.WorktreePath))
	realParent := parent + "-real"
	mustNoError(t, os.Rename(parent, realParent))
	mustNoError(t, os.Symlink(realParent, parent))
	if err := c.validateCleanupRecord(context.Background(), record); err == nil {
		t.Fatal("cleanup accepted symlinked ancestor")
	}
}

func TestRecordAbsenceRequiresDirectFilesystemProof(t *testing.T) {
	workspace, commit := newGitRepo(t)
	c := testCertifier(t, workspace)
	record := newIsolatedRecord(t, workspace, commit, "session-a", "workcell-a")
	item := &evidence{record: record, recordPath: writeTestRecord(t, c, record)}
	if err := c.proveRecordAbsent(item); err == nil {
		t.Fatal("proveRecordAbsent accepted an existing durable record")
	}
	mustNoError(t, os.Remove(item.recordPath))
	if err := c.proveRecordAbsent(item); err != nil {
		t.Fatalf("proveRecordAbsent error = %v", err)
	}
}

func TestKeepaliveHandlesSignals(t *testing.T) {
	for _, signal := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			bin := t.TempDir()
			ready := filepath.Join(bin, "ready")
			sleep := filepath.Join(bin, "sleep")
			mustNoError(t, os.WriteFile(sleep,
				[]byte("#!/bin/sh\nprintf ready >\"$READY\"\nexec /bin/sleep \"$@\"\n"), 0o700))
			cmd := exec.Command("/bin/sh", "-c", keepalive)
			cmd.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin", "READY="+ready)
			mustNoError(t, cmd.Start())
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			waited := false
			t.Cleanup(func() {
				if waited {
					return
				}
				_ = cmd.Process.Kill()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
				}
			})
			deadline := time.Now().Add(3 * time.Second)
			for {
				if output, err := os.ReadFile(ready); err == nil && string(output) == "ready" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("keepalive did not report readiness")
				}
				time.Sleep(10 * time.Millisecond)
			}
			mustNoError(t, cmd.Process.Signal(signal))
			select {
			case err := <-done:
				waited = true
				mustNoError(t, err)
			case <-time.After(3 * time.Second):
				_ = cmd.Process.Kill()
				<-done
				waited = true
				t.Fatalf("keepalive did not handle %s", signal)
			}
		})
	}
}

func TestRunCommandReapsCancelledProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := t.TempDir()
	ready, marker := filepath.Join(root, "ready"), filepath.Join(root, "cleanup")
	script := `trap 'printf done >"$2"; exit 0' TERM; /bin/sh -c 'trap "" TERM; while :; do sleep 1; done' & printf '%s\n' "$$" >"$1"; wait`
	type result struct {
		output []byte
		err    error
	}
	resultCh := make(chan result, 1)
	resultReceived := false
	var group int
	go func() {
		output, err := runCommandWithDelay(ctx, os.Environ(), 2*time.Second,
			"/bin/sh", "-c", script, "sh", ready, marker)
		resultCh <- result{output: output, err: err}
	}()
	t.Cleanup(func() {
		cancel()
		if group > 0 {
			_ = syscall.Kill(-group, syscall.SIGKILL)
		}
		if !resultReceived {
			select {
			case <-resultCh:
			case <-time.After(3 * time.Second):
			}
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	var lastReadyErr error
	for {
		output, err := os.ReadFile(ready)
		if err == nil {
			var candidate int
			if _, scanErr := fmt.Sscan(string(output), &candidate); scanErr == nil &&
				candidate > 0 && strings.HasSuffix(string(output), "\n") {
				group = candidate
				break
			}
			lastReadyErr = fmt.Errorf("parse process group %q", output)
		} else {
			lastReadyErr = err
		}
		if time.Now().After(deadline) {
			t.Fatalf("command did not report readiness: %v", lastReadyErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	var commandResult result
	select {
	case commandResult = <-resultCh:
		resultReceived = true
	case <-time.After(4 * time.Second):
		t.Fatal("cancelled command did not return")
	}
	if commandResult.err == nil {
		t.Fatal("runCommandWithDelay succeeded after timeout")
	}
	if markerOutput, err := os.ReadFile(marker); err != nil || string(markerOutput) != "done" {
		t.Fatalf("graceful cleanup = %q, %v", markerOutput, err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for syscall.Kill(-group, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(-group, syscall.SIGKILL)
			t.Fatalf("cancelled process group %d survived: %v", group, commandResult.err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestGitCommandDisablesRepositoryExecution(t *testing.T) {
	workspace, _ := newGitRepo(t)
	marker, hook := filepath.Join(t.TempDir(), "hook-ran"), filepath.Join(t.TempDir(), "hook.sh")
	mustNoError(t, os.WriteFile(hook, []byte("#!/bin/sh\nprintf tracked >\""+marker+"\"\ncat\n"), 0o700))
	mustNoError(t, os.WriteFile(filepath.Join(workspace, ".gitattributes"), []byte("tracked filter=host\n"), 0o600))
	runGit(t, workspace, "add", ".gitattributes")
	runGit(t, workspace, "-c", "user.name=Workcell Test", "-c", "user.email=workcell-test@example.invalid", "commit", "--quiet", "-m", "attributes")
	runGit(t, workspace, "config", "core.fsmonitor", hook)
	runGit(t, workspace, "config", "filter.host.clean", hook)
	runGit(t, workspace, "config", "filter.host.required", "true")
	runGit(t, workspace, "config", "status.showUntrackedFiles", "no")
	mustNoError(t, os.WriteFile(filepath.Join(workspace, "tracked"), []byte("changed\n"), 0o600))
	runGit(t, workspace, "-c", "core.fileMode=false", "-c", "core.symlinks=false", "status", "--porcelain=v1")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("malicious Git fixture did not execute under raw Git: %v", err)
	}
	mustNoError(t, errors.Join(os.Remove(marker), os.WriteFile(filepath.Join(workspace, "tracked"), []byte("base\n"), 0o600), os.WriteFile(filepath.Join(workspace, "untracked"), []byte("hidden\n"), 0o600)))
	decoy, _ := newGitRepo(t)
	runGit(t, workspace, "config", "core.worktree", decoy)
	status, err := testCertifier(t, workspace).gitCommand(context.Background(), workspace, "status", "--porcelain=v1", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil || !strings.Contains(string(status), "?? untracked") {
		t.Fatalf("gitCommand status error=%v output=%q", err, status)
	}
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repository-local Git hook executed on the host: %v", err)
	}
	runGit(t, workspace, "update-index", "--assume-unchanged", "--skip-worktree", "tracked")
	_, statusErr := testCertifier(t, workspace).gitCommand(context.Background(), workspace, "status", "--porcelain=v1")
	if _, diffErr := testCertifier(t, workspace).gitCommand(context.Background(), workspace, "diff-files", "--quiet", "--ignore-submodules=none", "--"); statusErr == nil || diffErr == nil {
		t.Fatal("gitCommand accepted hidden index state")
	}
}

func TestGitCommandRejectsForgedTrackedState(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{"forged index stat", func(t *testing.T, root string) {
			path := filepath.Join(root, "cached")
			mustNoError(t, errors.Join(os.WriteFile(path, []byte("evil\n"), 0o600), os.Chtimes(path, time.Unix(978310860, 0), time.Unix(978310860, 0))))
			runGit(t, root, "-c", "core.trustctime=false", "-c", "core.checkStat=minimal", "update-index", "--refresh", "--", "cached")
		}},
		{"executable mode", func(t *testing.T, root string) { mustNoError(t, os.Chmod(filepath.Join(root, "tracked"), 0o700)) }},
		{"symlink type", func(t *testing.T, root string) {
			path := filepath.Join(root, "link")
			mustNoError(t, errors.Join(os.Rename(path, filepath.Join(t.TempDir(), "link")), os.WriteFile(path, []byte("tracked"), 0o600)))
		}},
		{"line endings", func(t *testing.T, root string) {
			mustNoError(t, os.Rename(filepath.Join(root, "eol"), filepath.Join(t.TempDir(), "eol")))
			runGit(t, root, "-c", "core.autocrlf=true", "-c", "core.eol=crlf", "checkout", "--", "eol")
		}},
		{"external attributes", func(t *testing.T, root string) {
			attributes := filepath.Join(t.TempDir(), "attributes")
			mustNoError(t, errors.Join(os.WriteFile(attributes, []byte("* text eol=crlf\n"), 0o600), os.Remove(filepath.Join(root, "eol"))))
			runGit(t, root, "config", "core.attributesFile", attributes)
			runGit(t, root, "checkout", "--", "eol")
			if status := runGit(t, root, "status", "--porcelain=v1"); strings.TrimSpace(string(status)) != "" {
				t.Fatalf("external attributes fixture is not hidden from raw Git: %q", status)
			}
		}},
		{"repository info attributes", func(t *testing.T, root string) {
			path := strings.TrimSpace(string(runGit(t, root, "rev-parse", "--git-path", "info/attributes")))
			mustNoError(t, errors.Join(os.WriteFile(filepath.Join(root, path), []byte("* text eol=crlf\n"), 0o600), os.Remove(filepath.Join(root, "eol"))))
			runGit(t, root, "checkout", "--", "eol")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workspace, _ := newGitRepo(t)
			tc.mutate(t, workspace)
			_, err := testCertifier(t, workspace).gitCommand(context.Background(), workspace, "status", "--porcelain=v1")
			if err == nil || !strings.Contains(err.Error(), "tracked worktree content differs") {
				t.Fatalf("gitCommand accepted %s: %v", tc.name, err)
			}
		})
	}
}

func testCertifier(t *testing.T, workspace string) *certifier {
	git, err := exec.LookPath("git")
	mustNoError(t, err)
	return &certifier{
		options: Options{Root: workspace, Workspace: workspace, PollAttempts: 2, PollInterval: time.Millisecond, CommandTimeout: 10 * time.Second},
		deps: dependencies{
			command: runCommand, now: func() time.Time { return time.Unix(0, 0) },
			sleep:                func(context.Context, time.Duration) error { return nil },
			socketExists:         func(string) error { return nil },
			reapProfileProcesses: func(context.Context, string) error { return nil },
		},
		git: git, colima: "/fake/colima",
		stateRoot: t.TempDir(), colimaRoot: t.TempDir(),
		scratchRoot: workspace,
		launchRoot:  workspace,
		profile:     "wcl-profile",
		baseEnv:     append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_COUNT=6", "GIT_CONFIG_KEY_0=core.fileMode", "GIT_CONFIG_VALUE_0=false", "GIT_CONFIG_KEY_1=core.symlinks", "GIT_CONFIG_VALUE_1=false", "GIT_CONFIG_KEY_2=core.trustctime", "GIT_CONFIG_VALUE_2=false", "GIT_CONFIG_KEY_3=core.checkStat", "GIT_CONFIG_VALUE_3=minimal", "GIT_CONFIG_KEY_4=core.autocrlf", "GIT_CONFIG_VALUE_4=true", "GIT_CONFIG_KEY_5=core.eol", "GIT_CONFIG_VALUE_5=crlf"),
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
	statCacheMtime := time.Unix(978310860, 0)
	mustNoError(t, errors.Join(os.WriteFile(filepath.Join(root, "tracked"), []byte("base\n"), 0o600), os.WriteFile(filepath.Join(root, "cached"), []byte("base\n"), 0o600), os.Chtimes(filepath.Join(root, "cached"), statCacheMtime, statCacheMtime), os.WriteFile(filepath.Join(root, "eol"), []byte("base\n"), 0o600), os.Symlink("tracked", filepath.Join(root, "link"))))
	runGit(t, root, "add", "tracked", "cached", "eol", "link")
	runGit(t, root, "-c", "user.name=Workcell Test", "-c", "user.email=workcell-test@example.invalid", "commit", "--quiet", "-m", "base")
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
	for _, setting := range os.Environ() {
		if !strings.HasPrefix(setting, "GIT_CONFIG_") {
			cmd.Env = append(cmd.Env, setting)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return output
}
