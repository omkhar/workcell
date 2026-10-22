// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/rootio"
	"github.com/omkhar/workcell/internal/shellproto"
)

// snapshotNamePattern limits a session or snapshot id to one safe ref
// component. Both ids reach a git ref name and a store path.
var snapshotNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// snapshotOriginHashPattern matches the sha256 hex that SnapshotMain emits.
var snapshotOriginHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type snapshotCaptureOptions struct {
	GitBin     string
	StoreRoot  string
	Workspace  string
	GitHead    string
	OriginHash string
	SessionID  string
	SnapshotID string
}

// SnapshotCaptureMain is the capture half of `workcell session snapshot`. It
// builds the snapshot commit in a scratch git dir and publishes it into the
// host-owned store with anchored, fsynced writes. Output: tree= and commit=.
func SnapshotCaptureMain(args []string) error {
	return snapshotCaptureMain(args, os.Stdout)
}

func snapshotCaptureMain(args []string, stdout io.Writer) error {
	opts := snapshotCaptureOptions{}
	targets := map[string]*string{
		"--git-bin": &opts.GitBin, "--store-root": &opts.StoreRoot, "--workspace": &opts.Workspace,
		"--git-head": &opts.GitHead, "--origin-hash": &opts.OriginHash,
		"--session-id": &opts.SessionID, "--snapshot-id": &opts.SnapshotID,
	}
	for i := 0; i < len(args); i++ {
		target, ok := targets[args[i]]
		if !ok {
			return unsupportedOption("session snapshot capture", args[i])
		}
		v, ni, err := optionValueOrErrorStrict(args, i, args[i])
		if err != nil {
			return err
		}
		*target = v
		i = ni
	}
	tree, commit, err := snapshotCapture(opts)
	if err != nil {
		return &cliexit.ExitCodeError{Code: 1, Message: fmt.Sprintf("session snapshot failed: %v", err)}
	}
	return shellproto.WriteFields(stdout, []shellproto.Field{
		{Key: "tree", Value: tree},
		{Key: "commit", Value: commit},
	})
}

func snapshotCapture(opts snapshotCaptureOptions) (tree, commit string, err error) {
	switch {
	case !filepath.IsAbs(opts.GitBin), !filepath.IsAbs(opts.StoreRoot), !filepath.IsAbs(opts.Workspace):
		return "", "", errors.New("git, store, and workspace paths must be absolute")
	case !gitObjectIDPattern.MatchString(opts.GitHead):
		return "", "", errors.New("the recorded git head is not an object id")
	case !snapshotOriginHashPattern.MatchString(opts.OriginHash):
		return "", "", errors.New("the origin hash is not a sha256 digest")
	case !snapshotNamePattern.MatchString(opts.SessionID), !snapshotNamePattern.MatchString(opts.SnapshotID):
		return "", "", errors.New("the session or snapshot id is not a safe name")
	}
	gitDir := filepath.Join(opts.Workspace, ".git")
	objectsInfo, objErr := os.Lstat(filepath.Join(gitDir, "objects"))
	gitDirInfo, gitErr := os.Lstat(gitDir)
	if objErr != nil || gitErr != nil || !objectsInfo.IsDir() || gitDirInfo.Mode()&os.ModeSymlink != 0 {
		return "", "", fmt.Errorf("session snapshot requires a self-contained git workspace: %s", opts.Workspace)
	}
	if err := rejectSpecialAlternates(gitDir); err != nil {
		return "", "", err
	}

	err = withScratchGitDir("workcell-snapshot.", func(scratch string) error {
		tree, commit, err = captureIntoScratch(opts, gitDir, scratch)
		return err
	})
	return tree, commit, err
}

// captureIntoScratch writes the snapshot of the workspace through the private
// git dir scratch and publishes it to the store.
func captureIntoScratch(opts snapshotCaptureOptions, gitDir, scratch string) (tree, commit string, err error) {
	// The scratch dir is the git dir: its index is the temporary index and its
	// config is the only config git reads, so the workspace .git/config and
	// .git/index are untouched. The workspace objects are a read-only
	// alternate; the store fetch rehashes them, so a forged object fails closed.
	scratchGit := func(args ...string) (string, error) {
		return runSnapshotGit(opts.GitBin, scratch, append([]string{"--git-dir=" + scratch, "--work-tree=" + opts.Workspace}, args...)...)
	}
	if _, err := runSnapshotGit(opts.GitBin, scratch, snapshotInitArgs(opts.GitHead, scratch)...); err != nil {
		return "", "", err
	}
	if err := writeSnapshotAlternates(scratch, filepath.Join(gitDir, "objects")); err != nil {
		return "", "", err
	}
	for _, step := range [][]string{{"read-tree", opts.GitHead}, {"add", "-A"}} {
		if _, err := scratchGit(step...); err != nil {
			return "", "", err
		}
	}
	if tree, err = scratchGit("write-tree"); err != nil {
		return "", "", err
	}
	if commit, err = scratchGit("-c", "user.name=Workcell", "-c", "user.email=workcell@localhost",
		"commit-tree", "-p", opts.GitHead, "-m", "workcell session snapshot "+opts.SnapshotID, tree); err != nil {
		return "", "", err
	}
	if _, err := scratchGit("update-ref", "refs/workcell/snapshot", commit); err != nil {
		return "", "", err
	}

	if err := publishSnapshot(opts, scratch); err != nil {
		return "", "", err
	}
	return tree, commit, nil
}

// withScratchGitDir runs fn with a fresh owner-only directory that serves as a
// private git dir, and removes the directory afterwards. Snapshot capture and
// restore share it, so git never reads the session clone .git.
func withScratchGitDir(prefix string, fn func(scratch string) error) error {
	scratch, err := os.MkdirTemp("", prefix) // hardened-fs-exempt: this creates a fresh owner-only directory with O_EXCL semantics
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(scratch) }() // hardened-fs-exempt: this removes the directory created above, and RemoveAll does not follow symlinks
	return fn(scratch)
}

// rejectSpecialAlternates fails when objects/info/alternates in the workspace
// git dir is present but is not a regular file. The agent can write that file,
// and git opens it with a blocking read, so a FIFO there hangs every git run
// that uses the workspace objects as an alternate. This check is only a fast
// failure: git opens the object store by path, so a writer can swap a FIFO in
// after it. The process-group timeout of runSnapshotGit bounds that hang.
func rejectSpecialAlternates(gitDir string) error {
	objects, _, err := rootio.OpenParentDirectoryNoFollow(filepath.Join(gitDir, "objects", "anchor"))
	if err != nil {
		return fmt.Errorf("open the workspace objects: %w", err)
	}
	defer func() { _ = objects.Close() }()
	info, err := rootio.LstatAtNoFollow(objects, "info/alternates")
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil || info.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("the workspace objects/info/alternates is not a regular file: %s", gitDir)
	}
	return nil
}

// writeSnapshotAlternates makes the workspace object store a read-only
// alternate of the scratch git dir.
func writeSnapshotAlternates(scratch, objects string) error {
	infoFile, _, err := rootio.OpenParentDirectoryNoFollow(filepath.Join(scratch, "objects", "info", "alternates"))
	if err != nil {
		return err
	}
	defer func() { _ = infoFile.Close() }()
	return rootio.StageAndPublishAt(infoFile, "alternates", []byte(objects+"\n"), 0o600, ".alternates-")
}

// publishSnapshot fetches the scratch ref into the host-owned store through a
// no-follow descriptor chain. Git runs only while the store path still names
// the opened directory, and everything new is fsynced before the caller records
// the snapshot.
func publishSnapshot(opts snapshotCaptureOptions, scratch string) error {
	relative := filepath.Join(filepath.Base(opts.StoreRoot), opts.OriginHash+".git")
	rootParent, _, err := rootio.OpenParentDirectoryNoFollow(opts.StoreRoot)
	if err != nil {
		return fmt.Errorf("open the snapshot store parent: %w", err)
	}
	defer func() { _ = rootParent.Close() }()
	if err := rootio.MkdirAllSyncedAt(rootParent, relative, 0o700); err != nil {
		return err
	}
	storeFile, _, err := rootio.OpenParentDirectoryNoFollow(
		filepath.Join(rootParent.Name(), relative, "anchor"))
	if err != nil {
		return fmt.Errorf("open the snapshot store: %w", err)
	}
	defer func() { _ = storeFile.Close() }()
	storePath := storeFile.Name()

	check := func() error { return requireSameDirectory(storeFile, storePath, "snapshot store") }
	if err := check(); err != nil {
		return err
	}
	if _, err := runSnapshotGit(opts.GitBin, storePath, snapshotInitArgs(opts.GitHead, storePath)...); err != nil {
		return err
	}
	if err := check(); err != nil {
		return err
	}
	ref := "refs/workcell/snapshots/" + opts.SessionID + "/" + opts.SnapshotID
	if _, err := runSnapshotGit(opts.GitBin, storePath, "--git-dir="+storePath,
		"-c", "core.fsync=all", "-c", "core.fsyncMethod=fsync",
		"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", scratch,
		"refs/workcell/snapshot:"+ref); err != nil {
		return err
	}
	if err := check(); err != nil {
		return err
	}
	if err := rootio.SyncDirAt(storeFile, "refs/workcell/snapshots/"+opts.SessionID); err != nil {
		return err
	}
	if err := unix.Fsync(int(storeFile.Fd())); err != nil {
		return fmt.Errorf("sync the snapshot store: %w", err)
	}
	return nil
}

// snapshotInitArgs initializes a bare repository with the object format the
// recorded head implies, so a SHA-256 workspace gets a SHA-256 scratch
// repository and store.
func snapshotInitArgs(gitHead, dir string) []string {
	format := "sha1"
	if len(gitHead) == 64 {
		format = "sha256"
	}
	return []string{"init", "--quiet", "--bare", "--template=", "--object-format=" + format, dir}
}

// requireSameDirectory fails when path no longer names the directory the
// descriptor holds, so a component of the snapshot store or the session
// workspace that was swapped for a symlink or another directory during a git
// run is detected before the result is trusted. label names the directory in
// the error.
func requireSameDirectory(dir *os.File, path, label string) error {
	held, err := dir.Stat()
	if err != nil {
		return err
	}
	now, err := os.Stat(path) // hardened-fs-exempt: this stats the path by name on purpose, to prove it still names the directory the descriptor holds
	if err != nil {
		return fmt.Errorf("the %s path changed: %w", label, err)
	}
	if !os.SameFile(held, now) {
		return fmt.Errorf("the %s path changed while it was in use: %s", label, path)
	}
	return nil
}

// snapshotGitTimeout bounds one snapshot git run. The agent can still make git
// block, for example with a FIFO as a loose object, a pack index, or a chained
// alternate, so the bound turns that hang into an error.
var snapshotGitTimeout = 10 * time.Minute

// runSnapshotGit runs git with a scrubbed environment and no hooks, system or
// global config, fsmonitor, or external diff. A timeout or an interrupt sends
// SIGTERM to the git process group, because a fetch runs upload-pack as a
// child, and the Go runtime kills git if it is still running 5s later.
func runSnapshotGit(gitBin, dir string, args ...string) (string, error) {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/"
	}
	full := append([]string{
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.fsmonitor=false",
		"-c", "diff.external=",
		"-c", "color.ui=false",
	}, args...)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, snapshotGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, gitBin, full...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return unix.Kill(-cmd.Process.Pid, unix.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = []string{
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin:" + filepath.Dir(gitBin),
		"HOME=" + home,
		"LC_ALL=C",
		"LANG=C",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_GLOBAL=/dev/null",
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil && cmd.Process != nil {
			_ = unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
			return "", fmt.Errorf("git was stopped (%w): %s", context.Cause(ctx), strings.Join(args, " "))
		}
		return "", fmt.Errorf("git: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
