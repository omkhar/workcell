// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

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

// SnapshotCaptureMain implements the capture half of
// `workcell session snapshot`: it builds the snapshot commit in a scratch git
// dir, then publishes it into the host-owned store with descriptor-anchored,
// fsynced writes. The bash shim only pauses the container around this call and
// appends the audit record after it succeeds.
//
// Output:
//
//	tree=<object id>
//	commit=<object id>
func SnapshotCaptureMain(args []string) error {
	return snapshotCaptureMain(args, os.Stdout)
}

func snapshotCaptureMain(args []string, stdout io.Writer) error {
	opts := snapshotCaptureOptions{}
	for i := 0; i < len(args); i++ {
		var target *string
		switch args[i] {
		case "--git-bin":
			target = &opts.GitBin
		case "--store-root":
			target = &opts.StoreRoot
		case "--workspace":
			target = &opts.Workspace
		case "--git-head":
			target = &opts.GitHead
		case "--origin-hash":
			target = &opts.OriginHash
		case "--session-id":
			target = &opts.SessionID
		case "--snapshot-id":
			target = &opts.SnapshotID
		default:
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
	if info, statErr := os.Lstat(filepath.Join(gitDir, "objects")); statErr != nil || !info.IsDir() {
		return "", "", fmt.Errorf("session snapshot requires a self-contained git workspace: %s", opts.Workspace)
	}
	if info, statErr := os.Lstat(gitDir); statErr != nil || info.Mode()&os.ModeSymlink != 0 {
		return "", "", fmt.Errorf("session snapshot requires a self-contained git workspace: %s", opts.Workspace)
	}

	scratch, err := os.MkdirTemp("", "workcell-snapshot.")
	if err != nil {
		return "", "", err
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	// The scratch dir is the git dir, so its index is the temporary index and
	// its config is the only repository config git reads. The workspace
	// .git/config and .git/index are never read or written. The workspace
	// object store is an alternate for reads only; the store fetch below
	// rehashes every object, so a forged workspace object fails closed.
	if err := os.MkdirAll(filepath.Join(scratch, "objects", "info"), 0o700); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(filepath.Join(scratch, "objects", "info", "alternates"),
		[]byte(filepath.Join(gitDir, "objects")+"\n"), 0o600); err != nil {
		return "", "", err
	}
	scratchGit := func(args ...string) (string, error) {
		return runSnapshotGit(opts, scratch, append([]string{"--git-dir=" + scratch, "--work-tree=" + opts.Workspace}, args...)...)
	}
	if _, err := runSnapshotGit(opts, scratch, "init", "--quiet", "--bare", "--template=", scratch); err != nil {
		return "", "", err
	}
	if _, err := scratchGit("read-tree", opts.GitHead); err != nil {
		return "", "", err
	}
	if _, err := scratchGit("add", "-A"); err != nil {
		return "", "", err
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

// publishSnapshot fetches the scratch ref into the host-owned store. The
// store directory is opened through a no-follow descriptor chain, git runs
// only while that path still names the same directory, and the new objects,
// ref, and every directory that gained an entry are fsynced before the caller
// may record the snapshot.
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

	check := func() error { return requireSameDirectory(storeFile, storePath) }
	if err := check(); err != nil {
		return err
	}
	if _, err := runSnapshotGit(opts, storePath, "init", "--quiet", "--bare", "--template=", storePath); err != nil {
		return err
	}
	if err := check(); err != nil {
		return err
	}
	ref := "refs/workcell/snapshots/" + opts.SessionID + "/" + opts.SnapshotID
	if _, err := runSnapshotGit(opts, storePath, "--git-dir="+storePath,
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

// requireSameDirectory fails when path no longer names the directory the
// descriptor holds, so a store component swapped for a symlink or another
// directory during a git run is detected before the result is trusted.
func requireSameDirectory(dir *os.File, path string) error {
	held, err := dir.Stat()
	if err != nil {
		return err
	}
	now, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("the snapshot store path changed: %w", err)
	}
	if !os.SameFile(held, now) {
		return fmt.Errorf("the snapshot store path changed while it was in use: %s", path)
	}
	return nil
}

// runSnapshotGit runs git with a scrubbed environment: no system or global
// config, no hooks, no fsmonitor, no external diff, and a pinned PATH, HOME,
// and locale.
func runSnapshotGit(opts snapshotCaptureOptions, dir string, args ...string) (string, error) {
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
	cmd := exec.Command(opts.GitBin, full...)
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin:" + filepath.Dir(opts.GitBin),
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
		return "", fmt.Errorf("git %s: %w: %s", firstGitVerb(args), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func firstGitVerb(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") && !strings.Contains(arg, "=") {
			return arg
		}
	}
	return "command"
}
