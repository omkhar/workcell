// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package c3certify proves concurrent strict Colima sessions have isolated boundaries.
package c3certify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/omkhar/workcell/internal/host/launcher"
	"github.com/omkhar/workcell/internal/host/sessions"
)

var keepalive, safeID, gitFilterSetting, gitHiddenIndexState = `trap 'exit 0' TERM INT; while :; do sleep 1; done`, regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`), regexp.MustCompile(`^filter\..+\.(clean|smudge|process|required)$`), regexp.MustCompile(`(^|\x00)(S|[a-z]) `)

type Options struct {
	Root, Workspace, PrecommitControlTree string
	PollAttempts                          int
	PollInterval, CommandTimeout          time.Duration
}
type dependencies struct {
	command              func(context.Context, []string, string, ...string) ([]byte, error)
	now                  func() time.Time
	sleep                func(context.Context, time.Duration) error
	socketExists         func(string) error
	reapProfileProcesses func(context.Context, string) error
}
type evidence struct {
	record     sessions.SessionRecord
	recordPath string
}
type snapshot struct{ controlCommit, controlTree, launcherHash, dockerHash, workloadCommit string }
type certifier struct {
	options                                        Options
	deps                                           dependencies
	workcell, docker, colima, git                  string
	stateRoot, colimaRoot                          string
	scratchRoot, launchRoot, dockerConfig, profile string
	baseEnv                                        []string
	sessions                                       []*evidence
	cleanupTried, profileTried                     bool
}

func Run(ctx context.Context, options Options, stdout io.Writer) error {
	if options.PollAttempts == 0 {
		options.PollAttempts = 120
	}
	if options.PollInterval == 0 {
		options.PollInterval = time.Second
	}
	if options.CommandTimeout == 0 {
		options.CommandTimeout = 10 * time.Minute
	}
	if options.PollAttempts < 1 || options.PollInterval < 0 || options.CommandTimeout <= 0 {
		return errors.New("certify-c3: polling and command timeout values must be positive")
	}
	c, err := newCertifier(options, dependencies{
		command: runCommand, now: time.Now, reapProfileProcesses: launcher.ReapColimaProfileProcesses,
		sleep: func(ctx context.Context, delay time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
				return nil
			}
		},
		socketExists: func(path string) error {
			info, err := os.Lstat(path)
			if err == nil && info.Mode()&os.ModeSocket == 0 {
				return errors.New("not a socket")
			}
			return err
		},
	})
	if err != nil {
		return err
	}
	return c.execute(ctx, stdout)
}
func (c *certifier) execute(ctx context.Context, stdout io.Writer) (runErr error) {
	defer func() {
		if cleanupErr := c.cleanupBounded(); cleanupErr != nil {
			runErr = errors.Join(runErr, cleanupErr)
		}
	}()
	before, err := c.captureSnapshot(ctx)
	if err != nil {
		return err
	}
	if err := c.prepareLaunchWorkspace(ctx, before.workloadCommit); err != nil {
		return err
	}
	first, err := c.start(ctx)
	if err != nil {
		return err
	}
	second, err := c.start(ctx)
	if err != nil {
		return err
	}
	if err := c.validatePair(ctx, first.record, second.record, before.workloadCommit); err != nil {
		return err
	}
	if err := c.requireRunning(ctx, first.record); err != nil {
		return err
	}
	if err := c.requireRunning(ctx, second.record); err != nil {
		return err
	}
	if err := c.proveMarker(ctx, first.record, second.record,
		".workcell-c3-"+first.record.SessionID, "session-a-only"); err != nil {
		return err
	}
	if err := c.proveMarker(ctx, second.record, first.record,
		".workcell-c3-"+second.record.SessionID, "session-b-only"); err != nil {
		return err
	}
	if _, err := c.workcellCommand(ctx, "session", "stop", "--id", first.record.SessionID); err != nil {
		return fmt.Errorf("certify-c3: stop session A: %w", err)
	}
	if err := c.waitStopped(ctx, first.record); err != nil {
		return err
	}
	if err := c.requireRunning(ctx, second.record); err != nil {
		return fmt.Errorf("certify-c3: session B did not remain running after A stopped: %w", err)
	}
	if err := c.cleanupBounded(); err != nil {
		return err
	}
	after, err := c.captureSnapshot(ctx)
	if err != nil {
		return err
	}
	if before != after {
		return errors.New("certify-c3: recorded control-plane or workload identity changed during certification")
	}
	return writeReport(stdout, before, first, second, c.deps.now())
}
func writeReport(stdout io.Writer, before snapshot, first, second *evidence, now time.Time) error {
	const format = "# C3 strict-target isolation certification\n\n- date (UTC): %s\n- Workcell control-plane commit: %s\n- Workcell control-plane tree: %s\n- Workcell launcher SHA-256: %s\n- Docker client SHA-256: %s\n- workload commit: %s\n- target: local_vm/colima/strict\n- profile: %s\n- session A: %s (%s, %s, %s)\n- session B: %s (%s, %s, %s)\n- sessions overlapped: true\n- containers distinct: true\n- worktrees distinct: true\n- branches distinct: true\n- marker visible in session A: true\n- marker absent from session B: true\n- marker visible in session B: true\n- marker absent from session A: true\n- container workspaces matched recorded host worktrees: true\n- session A stopped independently: true\n- session B remained running after A stopped: true\n- session records, containers, and isolated worktrees removed: true\n- certifier-owned Colima profile, target state, and runtime image cache removed: true\n- keepalive scope: acknowledged arbitrary command; provider interaction is not certified\n"
	_, err := fmt.Fprintf(stdout, format, now.UTC().Format(time.RFC3339), before.controlCommit, before.controlTree,
		before.launcherHash, before.dockerHash, before.workloadCommit, first.record.Profile,
		first.record.SessionID, first.record.ContainerName, first.record.WorktreePath, first.record.GitBranch,
		second.record.SessionID, second.record.ContainerName, second.record.WorktreePath, second.record.GitBranch)
	return err
}
func newCertifier(options Options, deps dependencies) (_ *certifier, resultErr error) {
	root, rootErr := canonicalDirectory(options.Root, false)
	workspace, workspaceErr := canonicalDirectory(options.Workspace, false)
	if err := errors.Join(rootErr, workspaceErr); err != nil {
		return nil, err
	}
	options.Root, options.Workspace = root, workspace
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("certify-c3: resolve operator home: %w", err)
	}
	home, err = canonicalDirectory(home, false)
	if err != nil {
		return nil, err
	}
	colimaRoot, err := canonicalDirectory(filepath.Join(home, ".colima"), true)
	if err != nil {
		return nil, err
	}
	stateRoot := os.Getenv("WORKCELL_STATE_ROOT")
	if stateRoot == "" {
		stateRoot = filepath.Join(os.Getenv("XDG_STATE_HOME"), "workcell")
		if !filepath.IsAbs(stateRoot) {
			stateRoot = filepath.Join(home, ".local", "state", "workcell")
		}
	}
	stateRoot, err = canonicalDirectory(stateRoot, true)
	if err != nil {
		return nil, err
	}
	workcell, workcellErr := resolveExecutable(filepath.Join(root, "scripts", "workcell"))
	docker, dockerErr := resolveExecutable("/opt/homebrew/bin/docker", "/usr/local/bin/docker", "/Applications/Docker.app/Contents/Resources/bin/docker")
	colima, colimaErr := resolveExecutable("/opt/homebrew/bin/colima", "/usr/local/bin/colima")
	git, gitErr := resolveExecutable("/usr/bin/git", "/opt/homebrew/bin/git", "/usr/local/bin/git")
	if err := errors.Join(workcellErr, dockerErr, colimaErr, gitErr); err != nil {
		return nil, fmt.Errorf("certify-c3: trusted host tools: %w", err)
	}
	dockerConfig, dockerConfigErr := os.MkdirTemp("", "workcell-c3-docker-config.")
	scratchRoot, scratchRootErr := os.MkdirTemp("", "workcell-c3-workload.")
	if err := errors.Join(dockerConfigErr, scratchRootErr); err != nil {
		_ = os.RemoveAll(dockerConfig)
		_ = os.RemoveAll(scratchRoot)
		return nil, fmt.Errorf("certify-c3: create scratch state: %w", err)
	}
	defer func() {
		if resultErr != nil {
			_ = os.RemoveAll(dockerConfig)
			_ = os.RemoveAll(scratchRoot)
		}
	}()
	scratchRoot, err = canonicalDirectory(scratchRoot, false)
	if err != nil {
		return nil, err
	}
	profile := "wcl-c3-" + strings.TrimPrefix(filepath.Ext(scratchRoot), ".")
	cachePaths, err := runtimeImageCachePaths(stateRoot, profile)
	if err != nil {
		return nil, fmt.Errorf("certify-c3: inspect runtime image cache: %w", err)
	}
	if err := requireOwnedPathsAbsent(colimaRoot, colimaProfilePaths(colimaRoot, profile)...); err != nil {
		return nil, err
	}
	statePaths := append(cachePaths, filepath.Join(stateRoot, "targets", "local_vm", "colima", profile))
	if err := requireOwnedPathsAbsent(stateRoot, statePaths...); err != nil {
		return nil, err
	}
	env := []string{
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin:/usr/local/bin", "HOME=" + home,
		"TMPDIR=" + os.TempDir(), "LC_ALL=C", "LANG=C", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1", "WORKCELL_STATE_ROOT=" + stateRoot,
	}
	return &certifier{
		options: options, deps: deps, workcell: workcell, docker: docker, colima: colima, git: git,
		stateRoot: stateRoot, colimaRoot: colimaRoot, scratchRoot: scratchRoot,
		launchRoot: filepath.Join(scratchRoot, "repo"), dockerConfig: dockerConfig, profile: profile, baseEnv: env,
	}, nil
}
func canonicalDirectory(path string, create bool) (string, error) {
	path, err := filepath.Abs(path)
	if err == nil && create {
		err = os.MkdirAll(path, 0o700)
	}
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(path)
}
func requireOwnedPathsAbsent(root string, paths ...string) error {
	for _, path := range paths {
		if err := requirePlainDirectoryChain(root, filepath.Dir(path)); err != nil {
			return fmt.Errorf("certify-c3: unsafe owned profile parent: %w", err)
		}
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("certify-c3: owned profile state already exists: %s", path)
		}
	}
	return nil
}
func (c *certifier) prepareLaunchWorkspace(ctx context.Context, commit string) error {
	if _, err := c.gitCommand(ctx, c.options.Workspace, "clone", "--quiet", "--no-hardlinks",
		c.options.Workspace, c.launchRoot); err != nil {
		return fmt.Errorf("certify-c3: clone owned workload: %w", err)
	}
	if _, err := c.gitCommand(ctx, c.launchRoot, "checkout", "--quiet", "--detach", commit); err != nil {
		return fmt.Errorf("certify-c3: pin owned workload: %w", err)
	}
	return nil
}
func (c *certifier) adoptOwnedRecords(ctx context.Context) error {
	locations, err := sessions.ListSessionRecordLocationsInRoots(
		[]string{c.stateRoot}, sessions.SessionListOptions{Workspace: c.launchRoot})
	if err != nil {
		return fmt.Errorf("certify-c3: reconcile owned session records: %w", err)
	}
	for _, location := range locations {
		if err := c.validateCleanupRecord(ctx, location.Record); err != nil {
			return err
		}
		found := false
		for _, item := range c.sessions {
			if item.record.SessionID == location.Record.SessionID {
				item.record, item.recordPath, found = location.Record, location.Path, true
				break
			}
		}
		if !found {
			c.sessions = append(c.sessions, &evidence{record: location.Record, recordPath: location.Path})
		}
	}
	return nil
}
func (c *certifier) findOwnedRecord(ctx context.Context, id string) (sessions.SessionRecord, string, error) {
	record, path, err := sessions.FindSessionRecordWithPathInRoots([]string{c.stateRoot}, id)
	if err != nil {
		return sessions.SessionRecord{}, "", err
	}
	if err := c.validateCleanupRecord(ctx, record); err != nil {
		return sessions.SessionRecord{}, "", err
	}
	return record, path, nil
}
func (c *certifier) start(ctx context.Context) (*evidence, error) {
	ack := c.deps.now().UTC().Format("2006-01-02")
	args := []string{
		"session", "start", "--agent", "codex", "--target", "colima", "--mode", "strict",
		"--workspace", c.launchRoot, "--session-workspace", "isolated", "--colima-profile", c.profile,
		"--no-default-injection-policy", "--allow-arbitrary-command",
		"--ack-arbitrary-command=" + ack, "--", "/bin/sh", "-lc", keepalive,
	}
	c.profileTried = true
	output, err := c.workcellCommand(ctx, args...)
	settleCtx, cancel := context.WithTimeout(context.Background(), c.options.CommandTimeout)
	defer cancel()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("certify-c3: start session: %w", err), c.adoptOwnedRecords(settleCtx))
	}
	id, err := parseStartSessionID(output)
	if err != nil {
		return nil, errors.Join(err, c.adoptOwnedRecords(settleCtx))
	}
	item := &evidence{record: sessions.SessionRecord{SessionID: id}}
	c.sessions = append(c.sessions, item)
	record, recordPath, err := c.waitRunning(settleCtx, id)
	if err != nil {
		return nil, err
	}
	item.record, item.recordPath = record, recordPath
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return item, nil
}
func (c *certifier) waitRunning(ctx context.Context, id string) (sessions.SessionRecord, string, error) {
	for attempt := 0; attempt < c.options.PollAttempts; attempt++ {
		record, path, err := c.findOwnedRecord(ctx, id)
		if err == nil && record.LiveStatus == "running" {
			return record, path, nil
		}
		if err := c.deps.sleep(ctx, c.options.PollInterval); err != nil {
			return sessions.SessionRecord{}, "", err
		}
	}
	return sessions.SessionRecord{}, "", fmt.Errorf("certify-c3: session did not become live: %s", id)
}
func (c *certifier) validatePair(ctx context.Context, a, b sessions.SessionRecord, workloadCommit string) error {
	for _, record := range []sessions.SessionRecord{a, b} {
		if !safeID.MatchString(record.SessionID) {
			return fmt.Errorf("certify-c3: invalid session id: %q", record.SessionID)
		}
		for _, check := range []struct{ name, got, want string }{
			{"target_kind", record.TargetKind, "local_vm"},
			{"target_provider", record.TargetProvider, "colima"},
			{"target_assurance_class", record.TargetAssuranceClass, "strict"},
			{"profile", record.Profile, c.profile},
			{"mode", record.Mode, "strict"},
			{"workspace_transport", record.WorkspaceTransport, "isolated-worktree-mount"},
			{"assurance", sessions.SessionAssuranceSummary(record), "managed-mutable"},
			{"execution_path", record.ExecutionPath, "lower-assurance-debug-command"},
			{"workspace_origin", record.WorkspaceOrigin, c.launchRoot},
		} {
			if check.got != check.want {
				return fmt.Errorf("certify-c3: session %s %s = %q, want %q",
					record.SessionID, check.name, check.got, check.want)
			}
		}
		if err := c.validateGitIdentity(ctx, record, workloadCommit); err != nil {
			return err
		}
	}
	for _, check := range []struct{ name, a, b string }{
		{"session ids", a.SessionID, b.SessionID},
		{"containers", a.ContainerName, b.ContainerName},
		{"worktrees", a.WorktreePath, b.WorktreePath},
		{"branches", a.GitBranch, b.GitBranch},
	} {
		if check.a == "" || check.b == "" || check.a == check.b {
			return fmt.Errorf("certify-c3: %s are missing or not distinct", check.name)
		}
	}
	return nil
}
func (c *certifier) validateGitIdentity(ctx context.Context, record sessions.SessionRecord, commit string) error {
	expected, err := c.expectedWorktree(ctx, record.SessionID)
	if err != nil {
		return err
	}
	recorded, err := filepath.EvalSymlinks(record.WorktreePath)
	if err != nil || recorded != expected {
		return fmt.Errorf("certify-c3: recorded worktree does not match session-owned path: %s", record.SessionID)
	}
	info, err := os.Stat(filepath.Join(expected, ".git"))
	if err != nil || !info.IsDir() {
		return fmt.Errorf("certify-c3: isolated worktree is not a self-contained clone: %s", record.SessionID)
	}
	if record.GitBranch != "workcell/session-"+record.SessionID {
		return fmt.Errorf("certify-c3: isolated branch does not match session id: %s", record.SessionID)
	}
	branch, err := c.gitCommand(ctx, expected, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || strings.TrimSpace(string(branch)) != record.GitBranch {
		return fmt.Errorf("certify-c3: isolated branch does not match recorded metadata: %s", record.SessionID)
	}
	head, err := c.gitCommand(ctx, expected, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(string(head)) != commit {
		return fmt.Errorf("certify-c3: isolated worktree does not match workload commit: %s", record.SessionID)
	}
	status, err := c.gitCommand(ctx, expected, "status", "--porcelain=v1", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil || len(bytes.TrimSpace(status)) != 0 {
		return fmt.Errorf("certify-c3: isolated worktree is not clean: %s", record.SessionID)
	}
	return nil
}
func (c *certifier) proveMarker(
	ctx context.Context,
	present, absent sessions.SessionRecord,
	marker, content string,
) error {
	if _, err := c.dockerCommand(ctx, present.Profile, "exec", present.ContainerName,
		"/bin/sh", "-lc", `printf "%s\n" "$1" >"/workspace/$2"`, "sh", content, marker); err != nil {
		return fmt.Errorf("certify-c3: write marker in session %s: %w", present.SessionID, err)
	}
	if err := proveHostMarkerPair(present.WorktreePath, absent.WorktreePath, marker, content+"\n"); err != nil {
		return err
	}
	if _, err := c.dockerCommand(ctx, absent.Profile, "exec", absent.ContainerName,
		"/bin/sh", "-lc", `test ! -e "/workspace/$1"`, "sh", marker); err != nil {
		return fmt.Errorf("certify-c3: marker crossed from session %s to %s: %w",
			present.SessionID, absent.SessionID, err)
	}
	return nil
}
func proveHostMarkerPair(presentWorktree, absentWorktree, marker, content string) error {
	presentMarker := filepath.Join(presentWorktree, marker)
	info, err := os.Lstat(presentMarker)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("certify-c3: marker was not a regular file in its recorded host worktree")
	}
	data, err := os.ReadFile(presentMarker)
	if err != nil || string(data) != content {
		return errors.New("certify-c3: marker content did not match its recorded host worktree")
	}
	if _, err := os.Lstat(filepath.Join(absentWorktree, marker)); !errors.Is(err, os.ErrNotExist) {
		return errors.New("certify-c3: marker appeared in the other recorded host worktree")
	}
	return nil
}
func (c *certifier) requireRunning(ctx context.Context, record sessions.SessionRecord) error {
	out, err := c.dockerCommand(ctx, record.Profile, "inspect", "-f", "{{.State.Running}}", record.ContainerName)
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return fmt.Errorf("certify-c3: session container is not running: %s", record.ContainerName)
	}
	return nil
}
func (c *certifier) waitStopped(ctx context.Context, record sessions.SessionRecord) error {
	for attempt := 0; attempt < c.options.PollAttempts; attempt++ {
		out, err := c.dockerCommand(ctx, record.Profile, "inspect", "-f", "{{.State.Running}}", record.ContainerName)
		if err == nil && strings.TrimSpace(string(out)) == "false" {
			return nil
		}
		if c.proveContainerAbsent(ctx, record.Profile, record.ContainerName) == nil {
			return nil
		}
		if err := c.deps.sleep(ctx, c.options.PollInterval); err != nil {
			return err
		}
	}
	return fmt.Errorf("certify-c3: session container did not stop independently: %s", record.ContainerName)
}
func (c *certifier) cleanup(ctx context.Context) error {
	if c.cleanupTried {
		return nil
	}
	c.cleanupTried = true
	var problems []error
	if err := c.adoptOwnedRecords(ctx); err != nil {
		problems = append(problems, err)
	}
	for index := len(c.sessions) - 1; index >= 0; index-- {
		item := c.sessions[index]
		if item.record.SessionID == "" {
			continue
		}
		record, path, err := c.findOwnedRecord(ctx, item.record.SessionID)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				problems = append(problems, err)
			}
			continue
		}
		if item.recordPath != "" && item.recordPath != path {
			problems = append(problems, fmt.Errorf("certify-c3: session record path changed: %s", item.record.SessionID))
			continue
		}
		item.record, item.recordPath = record, path
		_, _ = c.workcellCommand(ctx, "session", "stop", "--id", item.record.SessionID, "--force")
		if err := c.waitTerminal(ctx, item); err != nil {
			problems = append(problems, err)
			continue
		}
		if _, err := c.workcellCommand(ctx, "session", "delete", "--id", item.record.SessionID); err != nil {
			problems = append(problems, fmt.Errorf("delete session %s: %w", item.record.SessionID, err))
			continue
		}
		if err := c.proveRecordAbsent(item); err != nil {
			problems = append(problems, err)
		}
		if item.record.Profile != "" && item.record.ContainerName != "" {
			if err := c.proveContainerAbsent(ctx, item.record.Profile, item.record.ContainerName); err != nil {
				problems = append(problems, fmt.Errorf("container cleanup was not proved: %s: %w",
					item.record.ContainerName, err))
			}
		}
	}
	if c.profileTried {
		if _, err := c.colimaCommand(ctx, "delete", "--profile", c.profile, "--force"); err != nil {
			problems = append(problems, fmt.Errorf("delete owned Colima profile: %w", err))
		}
		output, err := c.colimaCommand(ctx, "list", "--json")
		if err != nil {
			problems = append(problems, fmt.Errorf("list Colima profiles: %w", err))
		} else if status, statusErr := launcher.ColimaProfileStatus(output, c.profile); !launcher.IsNoMatch(statusErr) {
			problems = append(problems, fmt.Errorf(
				"certify-c3: owned Colima profile absence was not proved (status=%q): %v", status, statusErr))
		} else if err := c.deps.reapProfileProcesses(ctx, c.profile); err != nil {
			problems = append(problems, err)
		} else {
			for _, path := range colimaProfilePaths(c.colimaRoot, c.profile) {
				if err := removeOwnedPath(c.colimaRoot, path); err != nil {
					problems = append(problems, err)
				}
			}
		}
	}
	if err := os.RemoveAll(c.dockerConfig); err != nil {
		problems = append(problems, err)
	}
	if c.profileTried && len(problems) == 0 {
		profileState := filepath.Join(c.stateRoot, "targets", "local_vm", "colima", c.profile)
		cachePaths, err := runtimeImageCachePaths(c.stateRoot, c.profile)
		if err != nil {
			problems = append(problems, err)
		} else {
			for _, path := range append(cachePaths, profileState) {
				if err := removeOwnedPath(c.stateRoot, path); err != nil {
					problems = append(problems, err)
				}
			}
		}
	}
	if len(problems) == 0 {
		if err := requirePlainDirectoryChain(c.scratchRoot, c.scratchRoot); err != nil {
			problems = append(problems, err)
		} else if err := os.RemoveAll(c.scratchRoot); err != nil {
			problems = append(problems, err)
		} else if _, err := os.Lstat(c.scratchRoot); !errors.Is(err, os.ErrNotExist) {
			problems = append(problems, errors.New("certify-c3: owned workload residue remains"))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("certify-c3: cleanup verification failed: %w", errors.Join(problems...))
	}
	return nil
}
func (c *certifier) cleanupBounded() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	return c.cleanup(ctx)
}
func (c *certifier) waitTerminal(ctx context.Context, item *evidence) error {
	for attempt := 0; attempt < c.options.PollAttempts; attempt++ {
		record, path, err := c.findOwnedRecord(ctx, item.record.SessionID)
		if err == nil {
			if item.recordPath != "" && item.recordPath != path {
				return fmt.Errorf("certify-c3: session record path changed: %s", item.record.SessionID)
			}
			item.record, item.recordPath = record, path
			if sessions.IsTerminalSessionStatus(record.Status) || record.LiveStatus == "stopped" {
				return nil
			}
		}
		if err := c.deps.sleep(ctx, c.options.PollInterval); err != nil {
			return err
		}
	}
	return fmt.Errorf("certify-c3: session did not reach a terminal state: %s", item.record.SessionID)
}
func (c *certifier) expectedWorktree(ctx context.Context, id string) (string, error) {
	if !safeID.MatchString(id) {
		return "", fmt.Errorf("certify-c3: invalid session id: %q", id)
	}
	output, err := c.gitCommand(ctx, c.launchRoot, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", fmt.Errorf("certify-c3: resolve workspace Git directory: %w", err)
	}
	gitDir, err := filepath.EvalSymlinks(strings.TrimSpace(string(output)))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(gitDir)
	if err != nil || !info.IsDir() {
		return "", errors.New("certify-c3: workspace Git directory is unavailable")
	}
	if err := requirePlainDirectoryChain(c.scratchRoot, gitDir); err != nil {
		return "", fmt.Errorf("certify-c3: unsafe workspace Git directory: %w", err)
	}
	return filepath.Join(gitDir, "workcell-sessions", id, "repo"), nil
}
func (c *certifier) validateCleanupRecord(ctx context.Context, record sessions.SessionRecord) error {
	if !safeID.MatchString(record.SessionID) || !safeID.MatchString(record.Profile) ||
		sessions.ProveContainerAbsentForDelete(record.ContainerName, "") != nil ||
		record.TargetKind != "local_vm" || record.TargetProvider != "colima" ||
		record.Profile != c.profile || record.TargetID != c.profile ||
		record.WorkspaceOrigin != c.launchRoot {
		return fmt.Errorf("certify-c3: refusing unsupported cleanup metadata for session %q", record.SessionID)
	}
	expected, err := c.expectedWorktree(ctx, record.SessionID)
	if err != nil {
		return err
	}
	if record.WorktreePath == "" || filepath.Clean(record.WorktreePath) != expected {
		return fmt.Errorf("certify-c3: refusing mismatched cleanup worktree: %s", record.SessionID)
	}
	if err := requirePlainDirectoryChain(c.scratchRoot, filepath.Dir(expected)); err != nil {
		return fmt.Errorf("certify-c3: refusing unsafe cleanup worktree: %w", err)
	}
	return nil
}
func (c *certifier) proveRecordAbsent(item *evidence) error {
	if item.recordPath == "" {
		return fmt.Errorf("certify-c3: durable record path was not captured: %s", item.record.SessionID)
	}
	if err := requirePlainDirectoryChain(c.stateRoot, filepath.Dir(item.recordPath)); err != nil {
		return fmt.Errorf("certify-c3: durable record parent is unsafe: %w", err)
	}
	if _, err := os.Lstat(item.recordPath); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("certify-c3: durable session record remains: %s", item.record.SessionID)
	}
	_, _, err := sessions.FindSessionRecordWithPathInRoots([]string{c.stateRoot}, item.record.SessionID)
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("certify-c3: session record absence was not proved: %s: %w",
			item.record.SessionID, err)
	}
	return nil
}
func requirePlainDirectoryChain(root, target string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes trusted root: %s", target)
	}
	current := root
	for _, part := range append([]string{"."}, strings.Split(relative, string(filepath.Separator))...) {
		if part != "." {
			current = filepath.Join(current, part)
		}
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path component is not a plain directory: %s", current)
		}
	}
	return nil
}
func removeOwnedPath(root, path string) error {
	if err := requirePlainDirectoryChain(root, filepath.Dir(path)); err != nil {
		return fmt.Errorf("certify-c3: unsafe owned profile path: %w", err)
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("certify-c3: owned profile residue remains: %s", path)
	}
	return nil
}
func colimaProfilePaths(root, profile string) []string {
	return []string{
		filepath.Join(root, profile),
		filepath.Join(root, "_lima", "colima-"+profile),
		filepath.Join(root, "_lima", "_disks", "colima-"+profile),
		filepath.Join(root, "_store", "colima-"+profile+".json"),
	}
}
func runtimeImageCachePaths(stateRoot, profile string) ([]string, error) {
	if !safeID.MatchString(profile) {
		return nil, fmt.Errorf("invalid runtime image cache profile: %q", profile)
	}
	root := filepath.Join(stateRoot, "runtime-image-cache", "local_vm", "colima")
	if err := requirePlainDirectoryChain(stateRoot, root); err != nil {
		return nil, err
	}
	paths := []string{filepath.Join(root, profile+".tar"), filepath.Join(root, profile+".env")}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return paths, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		name := entry.Name()
		stable := name == profile+".tar" || name == profile+".env"
		if !stable && strings.HasPrefix(name, profile+".") &&
			(strings.HasSuffix(name, ".tar") || strings.HasSuffix(name, ".env")) {
			paths = append(paths, filepath.Join(root, name))
		}
	}
	return paths, nil
}
func (c *certifier) proveContainerAbsent(ctx context.Context, profile, container string) error {
	output, err := c.dockerCommand(ctx, profile, "ps", "-a", "--format", "{{.Names}}")
	if err != nil {
		return err
	}
	return sessions.ProveContainerAbsentForDelete(container, string(output))
}
func (c *certifier) dockerCommand(ctx context.Context, profile string, args ...string) ([]byte, error) {
	if !safeID.MatchString(profile) {
		return nil, fmt.Errorf("certify-c3: invalid Colima profile: %q", profile)
	}
	socket := filepath.Join(c.colimaRoot, profile, "docker.sock")
	if err := requirePlainDirectoryChain(c.colimaRoot, filepath.Dir(socket)); err != nil {
		return nil, fmt.Errorf("certify-c3: unsafe Colima socket parent: %w", err)
	}
	if err := c.deps.socketExists(socket); err != nil {
		return nil, fmt.Errorf("certify-c3: Workcell-owned Colima socket is unavailable: %s", socket)
	}
	env := append([]string{}, c.baseEnv...)
	env = append(env, "DOCKER_CONFIG="+c.dockerConfig, "DOCKER_HOST=unix://"+socket)
	return c.command(ctx, env, c.docker, args...)
}
func (c *certifier) colimaCommand(ctx context.Context, args ...string) ([]byte, error) {
	env := append([]string{}, c.baseEnv...)
	env = append(env, "COLIMA_HOME="+c.colimaRoot)
	return c.command(ctx, env, c.colima, args...)
}
func (c *certifier) workcellCommand(ctx context.Context, args ...string) ([]byte, error) {
	return c.command(ctx, c.baseEnv, "/bin/bash", append([]string{c.workcell}, args...)...)
}
func (c *certifier) gitCommand(ctx context.Context, dir string, args ...string) ([]byte, error) {
	safeArgs := []string{"--no-replace-objects", "-c", "core.hooksPath=/dev/null", "-c", "core.attributesFile=/dev/null", "-c", "core.fsmonitor=false", "-c", "core.fileMode=true", "-c", "core.symlinks=true", "-c", "core.trustctime=true", "-c", "core.checkStat=default", "-c", "core.autocrlf=false", "-c", "core.eol=lf", "-c", "diff.external=", "-c", "color.ui=false"}
	if len(args) == 0 || args[0] != "clone" {
		safeArgs = append(safeArgs, "--work-tree="+dir)
	}
	safeArgs = append(safeArgs, "-C", dir)
	settings, err := c.command(ctx, c.baseEnv, c.git,
		append(safeArgs, "config", "--null", "--name-only", "--includes", "--list")...)
	if err != nil {
		return nil, fmt.Errorf("certify-c3: inspect repository Git filters: %w", err)
	}
	for _, setting := range bytes.Split(settings, []byte{0}) {
		name := string(setting)
		match := gitFilterSetting.FindStringSubmatch(strings.ToLower(name))
		if match == nil {
			continue
		}
		safeArgs = append(safeArgs, "-c", name+"=", "-c", strings.TrimSuffix(name, match[1])+"required=false")
	}
	if len(args) > 0 && (args[0] == "status" || args[0] == "diff-files") {
		infoAttributes, err := c.command(ctx, c.baseEnv, c.git, append(safeArgs, "rev-parse", "--path-format=absolute", "--git-path", "info/attributes")...)
		if path := strings.TrimSpace(string(infoAttributes)); err != nil || !filepath.IsAbs(path) {
			return nil, errors.New("certify-c3: cannot resolve repository info attributes")
		} else if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("certify-c3: tracked worktree content differs from clone semantics")
		}
		if flags, err := c.command(ctx, c.baseEnv, c.git, append(safeArgs, "ls-files", "-v", "-z")...); err != nil || gitHiddenIndexState.Match(flags) {
			return nil, errors.New("certify-c3: repository index hides tracked worktree state")
		}
		if err := c.verifyTrackedWorktree(ctx, safeArgs); err != nil {
			return nil, err
		}
	}
	return c.command(ctx, c.baseEnv, c.git, append(safeArgs, args...)...)
}
func (c *certifier) verifyTrackedWorktree(ctx context.Context, safeArgs []string) error {
	indexDir, err := os.MkdirTemp(c.stateRoot, ".c3-index-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(indexDir)
	tree, err := c.command(ctx, c.baseEnv, c.git, append(safeArgs, "write-tree")...)
	if err != nil {
		return errors.New("certify-c3: cannot materialize indexed tree")
	}
	env := append([]string{}, c.baseEnv...)
	env = append(env, "GIT_INDEX_FILE="+filepath.Join(indexDir, "index"))
	if _, err := c.command(ctx, env, c.git, append(safeArgs, "read-tree", strings.TrimSpace(string(tree)))...); err != nil {
		return errors.New("certify-c3: cannot build independent index")
	}
	if _, err := c.command(ctx, env, c.git, append(safeArgs, "update-index", "--really-refresh", "-q")...); err != nil {
		return errors.New("certify-c3: tracked worktree content differs from the index")
	}
	return nil
}
func (c *certifier) command(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.options.CommandTimeout)
	defer cancel()
	return c.deps.command(callCtx, env, name, args...)
}
func (c *certifier) captureSnapshot(ctx context.Context) (snapshot, error) {
	controlCommit := "PENDING"
	controlTree, err := c.gitText(ctx, c.options.Root, "write-tree")
	if err != nil {
		return snapshot{}, err
	}
	if c.options.PrecommitControlTree == "" {
		status, err := c.gitCommand(ctx, c.options.Root, "status", "--porcelain=v1", "--untracked-files=all", "--ignore-submodules=none")
		if err != nil || len(bytes.TrimSpace(status)) != 0 {
			return snapshot{}, errors.New("certify-c3: control-plane tree must be clean")
		}
		controlCommit, err = c.gitText(ctx, c.options.Root, "rev-parse", "HEAD")
		if err != nil {
			return snapshot{}, err
		}
	} else if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(c.options.PrecommitControlTree) ||
		controlTree != c.options.PrecommitControlTree {
		return snapshot{}, errors.New("certify-c3: indexed control tree does not match --precommit-control-tree")
	} else {
		headTree, err := c.gitText(ctx, c.options.Root, "rev-parse", "HEAD^{tree}")
		if err != nil {
			return snapshot{}, err
		}
		if headTree == controlTree {
			return snapshot{}, errors.New("certify-c3: pre-commit control tree must differ from HEAD")
		}
		if _, err := c.gitCommand(ctx, c.options.Root, "diff-files", "--quiet", "--ignore-submodules=none", "--"); err != nil {
			return snapshot{}, errors.New("certify-c3: pre-commit control snapshot has tracked worktree changes")
		}
		untracked, err := c.gitCommand(ctx, c.options.Root, "ls-files", "--others", "--exclude-standard", "--directory")
		if err != nil || len(bytes.TrimSpace(untracked)) != 0 {
			return snapshot{}, errors.New("certify-c3: pre-commit control snapshot has untracked residue")
		}
	}
	workloadStatus, err := c.gitCommand(ctx, c.options.Workspace, "status", "--porcelain=v1", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil || len(bytes.TrimSpace(workloadStatus)) != 0 {
		return snapshot{}, errors.New("certify-c3: workload tree must be clean")
	}
	workloadCommit, err := c.gitText(ctx, c.options.Workspace, "rev-parse", "HEAD")
	if err != nil {
		return snapshot{}, err
	}
	launcherHash, err := fileHash(c.workcell)
	if err != nil {
		return snapshot{}, err
	}
	dockerHash, err := fileHash(c.docker)
	if err != nil {
		return snapshot{}, err
	}
	return snapshot{
		controlCommit: controlCommit, controlTree: controlTree,
		launcherHash: launcherHash, dockerHash: dockerHash,
		workloadCommit: workloadCommit,
	}, nil
}
func (c *certifier) gitText(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := c.gitCommand(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
func parseStartSessionID(output []byte) (string, error) {
	var id string
	for _, line := range strings.Split(string(output), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key != "session_id" {
			continue
		}
		if id != "" || !safeID.MatchString(value) {
			return "", errors.New("certify-c3: session start returned an invalid session id")
		}
		id = value
	}
	if id == "" {
		return "", errors.New("certify-c3: session start returned no session id")
	}
	return id, nil
}
func resolveExecutable(candidates ...string) (string, error) {
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no executable found among %s", strings.Join(candidates, ", "))
}
func fileHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}
func runCommand(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	return runCommandWithDelay(ctx, env, 40*time.Second, name, args...)
}
func runCommandWithDelay(ctx context.Context, env []string, waitDelay time.Duration, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = waitDelay
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	if err != nil {
		return output, fmt.Errorf("%s %s: %w: %s", filepath.Base(name),
			strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}
