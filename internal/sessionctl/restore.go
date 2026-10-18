// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/host/sessions"
	"github.com/omkhar/workcell/internal/host/stateroot"
	"github.com/omkhar/workcell/internal/shellproto"
)

type restoreArgs struct {
	sessionID, snapshotID, signingDir string
	showHelp                          bool
}

// RestoreMain implements the plan half of
// `workcell session restore --id SESSION_ID --snapshot SNAPSHOT_ID`.
//
// The bash shim (session_restore_main in scripts/workcell) snapshots the
// current worktree, calls RestoreApplyMain, and appends the session_restore
// audit record. RestoreMain reads the snapshot commit and tree only from audit
// records that the host seal covers, never from a ref in the snapshot store.
// It refuses a session without a signed exit record, and a session that the
// signed launch record shows on a direct workspace.
func RestoreMain(args []string) error {
	return restoreMain(args, os.Stdout, os.Stderr)
}

func restoreMain(args []string, stdout, stderr io.Writer) error {
	roots, rest := stateroot.ConsumeRootArgs(args)
	opts, err := parseRestoreArgs(rest)
	if err != nil {
		return err
	}
	if opts.showHelp {
		fmt.Fprint(stderr, UsageText())
		return nil
	}
	if err := validateRestoreArgs(opts); err != nil {
		return err
	}
	roots, err = rootsOrLookup(roots)
	if err != nil {
		return err
	}
	record, recordPath, err := sessions.FindSessionRecordWithPathInRoots(roots, opts.sessionID)
	if err != nil {
		return err
	}
	if !sessionIsDetached(record) || record.Profile == "" {
		return fmt.Errorf("session restore only works for detached sessions started with 'workcell session start': %s", opts.sessionID)
	}
	launch, snapshot, records, err := sealedSnapshotRecords("session restore", opts.signingDir, opts.sessionID, opts.snapshotID, record, recordPath)
	if err != nil {
		return err
	}
	// Only a signed exit record proves that the container no longer writes the
	// worktree. The unsigned record status is not enough.
	if !slices.ContainsFunc(records, func(values map[string]string) bool { return values["event"] == "exit" }) {
		return fmt.Errorf("session restore only works for a stopped session with a signed exit record: %s", opts.sessionID)
	}
	// An isolated session works in its own clone, so the signed workspace
	// differs from the signed origin. A direct session works in the origin.
	if launch["workspace"] == "" || launch["workspace_origin"] == "" || launch["workspace"] == launch["workspace_origin"] {
		return fmt.Errorf("session restore only works for an isolated session workspace: %s", opts.sessionID)
	}
	// The shim snapshots record.Workspace before the restore, so it must be
	// the signed workspace.
	if !recordMatchesSignedLaunch(record, launch) {
		return fmt.Errorf("session restore: the session record workspace or git head does not match the signed launch record: %s", opts.sessionID)
	}
	if !snapshotMatchesSignedLaunch(snapshot, launch) {
		return fmt.Errorf("session restore: the snapshot did not capture the signed launch workspace and head: %s", opts.sessionID)
	}
	originHash := sha256.Sum256([]byte(launch["workspace_origin"]))
	return shellproto.WriteFields(stdout, []shellproto.Field{
		{Key: "session_id", Value: record.SessionID},
		{Key: "profile", Value: record.Profile},
		{Key: "workspace", Value: launch["workspace"]},
		{Key: "origin_hash", Value: hex.EncodeToString(originHash[:])},
		{Key: "launch_head", Value: launch["workspace_head"]},
		{Key: "snapshot_id", Value: opts.snapshotID},
		{Key: "commit", Value: snapshot["commit"]},
		{Key: "tree", Value: snapshot["tree"]},
	})
}

func parseRestoreArgs(args []string) (restoreArgs, error) {
	opts := restoreArgs{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--id" || arg == "--snapshot":
			v, ni, err := optionValueOrErrorStrict(args, i, arg)
			if err != nil {
				return opts, err
			}
			if arg == "--id" {
				opts.sessionID = v
			} else {
				opts.snapshotID = v
			}
			i = ni
		case strings.HasPrefix(arg, "--signing-dir="):
			if opts.signingDir != "" {
				return opts, &cliexit.ExitCodeError{Code: 2, Message: "workcell session restore accepts --signing-dir only once."}
			}
			opts.signingDir = strings.TrimPrefix(arg, "--signing-dir=")
		case arg == "-h" || arg == "--help":
			opts.showHelp = true
		default:
			return opts, unsupportedOption("session restore", arg)
		}
	}
	return opts, nil
}

func validateRestoreArgs(opts restoreArgs) error {
	if opts.sessionID == "" {
		return &cliexit.ExitCodeError{Code: 2, Message: "workcell session restore requires --id."}
	}
	if opts.snapshotID == "" {
		return &cliexit.ExitCodeError{Code: 2, Message: "workcell session restore requires --snapshot."}
	}
	if opts.signingDir == "" {
		return &cliexit.ExitCodeError{Code: 2, Message: "workcell session restore requires --signing-dir."}
	}
	if err := rejectControlChars("session restore", "--id", opts.sessionID); err != nil {
		return err
	}
	if !snapshotNamePattern.MatchString(opts.snapshotID) {
		return &cliexit.ExitCodeError{Code: 2, Message: "workcell session restore --snapshot is not a snapshot id."}
	}
	return nil
}

type restoreApplyOptions struct {
	GitBin, StoreRoot, Workspace, OriginHash, Commit, Tree, Head string
}

// RestoreApplyMain is the apply half of `workcell session restore`. It fetches
// the signed snapshot commit by object id from the host-owned store into a
// scratch git dir, checks its tree and parent against the signed values, and
// then makes the worktree match that tree. Git never reads the session clone
// .git directory, which the agent could change. Output: tree= and commit=.
func RestoreApplyMain(args []string) error {
	return restoreApplyMain(args, os.Stdout)
}

func restoreApplyMain(args []string, stdout io.Writer) error {
	opts := restoreApplyOptions{}
	targets := map[string]*string{
		"--git-bin": &opts.GitBin, "--store-root": &opts.StoreRoot, "--workspace": &opts.Workspace,
		"--origin-hash": &opts.OriginHash, "--commit": &opts.Commit, "--tree": &opts.Tree, "--head": &opts.Head,
	}
	for i := 0; i < len(args); i++ {
		target, ok := targets[args[i]]
		if !ok {
			return unsupportedOption("session restore apply", args[i])
		}
		v, ni, err := optionValueOrErrorStrict(args, i, args[i])
		if err != nil {
			return err
		}
		*target = v
		i = ni
	}
	if err := restoreApply(opts); err != nil {
		return &cliexit.ExitCodeError{Code: 1, Message: fmt.Sprintf("session restore failed: %v", err)}
	}
	return shellproto.WriteFields(stdout, []shellproto.Field{
		{Key: "tree", Value: opts.Tree},
		{Key: "commit", Value: opts.Commit},
	})
}

func restoreApply(opts restoreApplyOptions) error {
	switch {
	case !filepath.IsAbs(opts.GitBin), !filepath.IsAbs(opts.StoreRoot), !filepath.IsAbs(opts.Workspace):
		return errors.New("git, store, and workspace paths must be absolute")
	case !gitObjectIDPattern.MatchString(opts.Commit), !gitObjectIDPattern.MatchString(opts.Tree), !gitObjectIDPattern.MatchString(opts.Head):
		return errors.New("the signed commit, tree, or head is not an object id")
	case !snapshotOriginHashPattern.MatchString(opts.OriginHash):
		return errors.New("the origin hash is not a sha256 digest")
	}
	info, err := os.Lstat(opts.Workspace)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("the session workspace is not a directory: %s", opts.Workspace)
	}

	return withScratchGitDir("workcell-restore.", func(scratch string) error {
		return restoreThroughScratch(opts, scratch)
	})
}

// restoreThroughScratch fetches the signed snapshot commit into the private
// git dir scratch, checks it, and applies its tree to the workspace.
func restoreThroughScratch(opts restoreApplyOptions, scratch string) error {
	// The scratch dir is the git dir, so git reads no config, index, or object
	// from the session clone .git. The fetch rehashes every object it gets.
	git := func(args ...string) (string, error) {
		return runSnapshotGit(opts.GitBin, scratch, append([]string{"--git-dir=" + scratch}, args...)...)
	}
	if _, err := runSnapshotGit(opts.GitBin, scratch, snapshotInitArgs(opts.Head, scratch)...); err != nil {
		return err
	}
	// Fetch by the signed object id, never by a store ref, which is not signed.
	store := filepath.Join(opts.StoreRoot, opts.OriginHash+".git")
	if _, err := git("fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--depth=1", store, opts.Commit); err != nil {
		return err
	}
	body, err := git("cat-file", "commit", opts.Commit)
	if err != nil {
		return err
	}
	if tree, parent := commitTreeAndParent(body); tree != opts.Tree || parent != opts.Head {
		return fmt.Errorf("the snapshot commit does not have the signed tree and launch head: %s", opts.Commit)
	}
	// The scratch index first takes the current worktree, so the reset then
	// removes the files that the snapshot tree does not have.
	worktree := "--work-tree=" + opts.Workspace
	for _, step := range [][]string{{worktree, "add", "-A"}, {worktree, "read-tree", "-u", "--reset", opts.Tree}} {
		if _, err := git(step...); err != nil {
			return err
		}
	}
	return nil
}

// commitTreeAndParent returns the tree and the first parent from the header of
// a raw commit object.
func commitTreeAndParent(body string) (tree, parent string) {
	for _, line := range strings.Split(body, "\n") {
		if line == "" {
			break
		}
		key, value, _ := strings.Cut(line, " ")
		switch {
		case key == "tree" && tree == "":
			tree = value
		case key == "parent" && parent == "":
			parent = value
		}
	}
	return tree, parent
}
