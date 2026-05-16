// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package publishpr

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/shellproto"
)

// PublishPRMain is the in-process entry point invoked by the launcher
// subcommand publish-pr-cli. It mirrors scripts/workcell publish_pr_main
// end-to-end: parse args, resolve the workspace + git/gh binaries, run
// the validators + preflight, probe the snapshot/worktree state, then
// either emit the dry-run command list or drive the actual git+gh
// sequence. The bash shim forwards the legacy globals as --bash-* flags
// so the trusted-tool resolver knows which paths are workspace-owned
// (untrusted) versus host-tool (trusted).
func PublishPRMain(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	ctx, rest := parseBashContextFlags(args)

	opts, err := ParseArgs(rest)
	if err != nil {
		// publish_pr_main prints usage to stderr only on the `*)`
		// branch (unsupported option); ParseArgs error messages start
		// with "Unsupported publish-pr option:" in that case, so we
		// gate the usage echo on the prefix to stay byte-identical.
		if ec, ok := cliexit.IsExitCodeError(err); ok && strings.HasPrefix(ec.Message, "Unsupported publish-pr option:") {
			WriteUsage(stderr)
		}
		return err
	}
	if opts.HelpRequested {
		WriteUsage(stdout)
		return nil
	}

	resolvedWorkspace, err := resolveExistingDirectoryOrDie(ctx, opts.Workspace)
	if err != nil {
		return err
	}

	if ctx.HostGitBin != "" {
		ctx.HostGitBin, err = ResolveExistingExecutableOrDie(ctx, ctx.HostGitBin, "HOST_GIT_BIN")
	} else {
		ctx.HostGitBin, err = ResolveHostTool(ctx, "git", true, []string{"/usr/bin/git", "/opt/homebrew/bin/git", "/usr/local/bin/git"})
	}
	if err != nil {
		return err
	}

	// Validators run after git resolves because validate_publish_branch_name
	// shells out to `${HOST_GIT_BIN} check-ref-format`.
	preflight, err := Preflight(opts, checkRefFormatHook(ctx), nil)
	if err != nil {
		return err
	}

	// gh resolution precedence mirrors bash: --gh-bin flag → HOST_GH_BIN
	// env → resolve_host_tool (or _optional under --dry-run falling back
	// to a bare `gh`).
	switch {
	case opts.GhBin != "":
		ctx.HostGhBin, err = ResolveExistingExecutableOrDie(ctx, opts.GhBin, "gh-bin")
	case ctx.HostGhBin != "":
		ctx.HostGhBin, err = ResolveExistingExecutableOrDie(ctx, ctx.HostGhBin, "HOST_GH_BIN")
	case opts.DryRun:
		ctx.HostGhBin, err = ResolveHostTool(ctx, "gh", false, []string{"/opt/homebrew/bin/gh", "/usr/local/bin/gh", "/usr/bin/gh"})
		if err == nil && ctx.HostGhBin == "" {
			ctx.HostGhBin = "gh"
		}
	default:
		ctx.HostGhBin, err = ResolveHostTool(ctx, "gh", true, []string{"/opt/homebrew/bin/gh", "/usr/local/bin/gh", "/usr/bin/gh"})
	}
	if err != nil {
		return err
	}

	if !workspaceIsGitWorkTree(ctx, resolvedWorkspace) {
		return &cliexit.ExitCodeError{Code: 2, Message: fmt.Sprintf("publish-pr requires a git worktree: %s", resolvedWorkspace)}
	}
	if !hasRemoteOrigin(ctx, resolvedWorkspace) {
		return &cliexit.ExitCodeError{Code: 2, Message: fmt.Sprintf("publish-pr requires an origin remote in %s.", resolvedWorkspace)}
	}

	for _, line := range preflight.LowerAssuranceNotice {
		fmt.Fprintln(stderr, line)
	}

	current := currentBranch(ctx, resolvedWorkspace)
	publishExistingCommits := 0
	hasChanges := func() bool {
		if opts.Snapshot == "worktree" {
			return hasWorktreeChanges(ctx, resolvedWorkspace)
		}
		return hasStagedChanges(ctx, resolvedWorkspace)
	}
	if !hasChanges() {
		if current == opts.Branch || branchExists(ctx, resolvedWorkspace, opts.Branch) {
			publishExistingCommits = 1
		} else {
			missing := "workspace"
			if opts.Snapshot != "worktree" {
				missing = "staged"
			}
			return &cliexit.ExitCodeError{Code: 2, Message: fmt.Sprintf("publish-pr found no %s changes to publish in %s.", missing, resolvedWorkspace)}
		}
	}

	// Resolve or stage the commit-message file. Bash armed a RETURN
	// trap; Go's defer plays the same role.
	resolvedCommitMessageFile, cleanup, err := resolveOrStageCommitMessage(ctx, opts, preflight)
	if err != nil {
		return err
	}
	defer cleanup()

	publishGitCmd := []string{ctx.HostGitBin, "-c", "core.hooksPath=/dev/null", "-C", resolvedWorkspace}
	clone := func(extra ...string) []string {
		return slices.Concat(publishGitCmd, extra)
	}

	var branchCmd []string
	if current == opts.Branch || branchExists(ctx, resolvedWorkspace, opts.Branch) {
		branchCmd = clone("switch", "--no-guess", opts.Branch)
	} else {
		branchCmd = clone("switch", "--no-guess", "-c", opts.Branch)
	}
	addCmd := clone("add", "-A")
	commitCmd := clone("commit", "--no-verify", "-S", "-F", resolvedCommitMessageFile)
	shapeBaseRef := "refs/remotes/origin/" + opts.Base
	fetchBaseCmd := clone("fetch", "--no-tags", "--prune", "origin", "+refs/heads/"+opts.Base+":"+shapeBaseRef)
	signatureCmd := []string{
		"/bin/bash",
		filepath.Join(ctx.RootDir, "scripts", "check-publish-commit-signatures.sh"),
		"--repo-root", resolvedWorkspace,
		"--base-ref", shapeBaseRef,
		"--head-ref", "HEAD",
		"--git-bin", ctx.HostGitBin,
	}
	shapeCmd := []string{
		"/bin/bash",
		filepath.Join(ctx.RootDir, "scripts", "check-pr-shape.sh"),
		"--repo-root", resolvedWorkspace,
		"--base-ref", shapeBaseRef,
		"--head-ref", "HEAD",
		"--max-files", "25",
		"--max-lines", "1200",
		"--max-areas", "8",
		"--max-binaries", "0",
	}
	pushCmd := clone("push", "--no-verify", "-u", "origin", opts.Branch)

	prCmd := []string{ctx.HostGhBin, "pr", "create", "--base", opts.Base, "--head", opts.Branch, "--title", preflight.TitleText}
	draft := !preflight.Ready
	if draft {
		prCmd = append(prCmd, "--draft")
	}
	if opts.BodyFile != "" {
		resolvedBodyFile, bodyErr := resolveExistingFileOrDie(ctx, opts.BodyFile, "body")
		if bodyErr != nil {
			return bodyErr
		}
		prCmd = append(prCmd, "--body-file", resolvedBodyFile)
	} else {
		prCmd = append(prCmd, "--body", preflight.BodyText)
	}

	if opts.DryRun {
		if err := emitDryRunHeader(stdout, opts, preflight, resolvedWorkspace, publishExistingCommits, draft); err != nil {
			return err
		}
		EmitCommand(stdout, branchCmd)
		if opts.Snapshot == "worktree" && publishExistingCommits == 0 {
			EmitCommand(stdout, addCmd)
		}
		if publishExistingCommits == 0 {
			EmitCommand(stdout, commitCmd)
		}
		EmitCommand(stdout, fetchBaseCmd)
		EmitCommand(stdout, signatureCmd)
		EmitCommand(stdout, shapeCmd)
		EmitCommand(stdout, pushCmd)
		EmitCommand(stdout, prCmd)
		return nil
	}

	env := &PublishEnv{Path: ctx.TrustedHostPath, Home: ctx.RealHome}
	run := func(args []string, out io.Writer) error {
		if out == nil {
			out = stdout
		}
		return RunPublishHostCommandInDir(resolvedWorkspace, env, args, stdin, out, stderr)
	}
	if err := run(branchCmd, nil); err != nil {
		return err
	}
	if publishExistingCommits == 1 {
		if hasWorktreeChanges(ctx, resolvedWorkspace) {
			return &cliexit.ExitCodeError{Code: 2, Message: fmt.Sprintf("publish-pr existing-branch mode requires a clean worktree in %s.", resolvedWorkspace)}
		}
	} else {
		if opts.Snapshot == "worktree" {
			if err := run(addCmd, nil); err != nil {
				return err
			}
		}
		if !hasStagedChanges(ctx, resolvedWorkspace) {
			return &cliexit.ExitCodeError{Code: 2, Message: fmt.Sprintf("publish-pr found no staged changes to commit in %s.", resolvedWorkspace)}
		}
		if err := run(commitCmd, nil); err != nil {
			return err
		}
	}
	for _, step := range [][]string{fetchBaseCmd, signatureCmd, shapeCmd, pushCmd} {
		if err := run(step, nil); err != nil {
			return err
		}
	}
	var prOut strings.Builder
	if err := run(prCmd, &prOut); err != nil {
		return err
	}
	prURL := strings.TrimRight(prOut.String(), "\n")
	// Fail-closed: render through a buffer first so a forbidden control
	// character in any field aborts the whole plan emission rather than
	// leaving the bash shim with a half-imported plan. Mirrors the
	// pattern FormatBundleResultForShell uses in internal/injection.
	var buf bytes.Buffer
	if err := shellproto.WriteFields(&buf, []shellproto.Field{
		{Key: "publish_branch", Value: opts.Branch},
		{Key: "publish_base", Value: opts.Base},
		{Key: "publish_pr_url", Value: prURL},
		{Key: "publish_snapshot", Value: opts.Snapshot},
	}); err != nil {
		return err
	}
	_, writeErr := stdout.Write(buf.Bytes())
	return writeErr
}

// emitDryRunHeader writes the publish-pr dry-run KEY=VALUE plan header.
// Fail-closed at the shellproto boundary: every field is rendered into
// an in-memory buffer first; if any value contains a forbidden control
// character, shellproto.WriteFields returns an error before anything
// reaches stdout, so the bash shim never sees a partially-written
// plan. Every value here originates from a tightly validated CLI flag
// or a constant, so this should be impossible in practice — but the
// buffered-then-flushed shape is the right defense for any future
// regression that smuggles a newline through.
func emitDryRunHeader(stdout io.Writer, opts *Options, preflight *PreflightInputs, resolvedWorkspace string, publishExistingCommits int, draft bool) error {
	draftFlag := "0"
	if draft {
		draftFlag = "1"
	}
	var buf bytes.Buffer
	if err := shellproto.WriteFields(&buf, []shellproto.Field{
		{Key: "publish_workspace", Value: resolvedWorkspace},
		{Key: "publish_snapshot", Value: opts.Snapshot},
		{Key: "publish_branch", Value: opts.Branch},
		{Key: "publish_base", Value: opts.Base},
		{Key: "publish_base_mode", Value: preflight.PublishBaseMode},
		{Key: "publish_existing_commits", Value: strconv.Itoa(publishExistingCommits)},
		{Key: "publish_repo_owned_pr_checks_expected", Value: preflight.RepoOwnedPRChecksExpected},
		{Key: "publish_draft", Value: draftFlag},
	}); err != nil {
		return err
	}
	_, writeErr := stdout.Write(buf.Bytes())
	return writeErr
}

func resolveOrStageCommitMessage(ctx *BashContext, opts *Options, preflight *PreflightInputs) (string, func(), error) {
	noop := func() {}
	if opts.CommitMessageFile != "" {
		resolved, err := resolveExistingFileOrDie(ctx, opts.CommitMessageFile, "commit-message")
		return resolved, noop, err
	}
	tmpDir := os.Getenv("TMPDIR")
	if tmpDir == "" {
		tmpDir = "/tmp"
	}
	tmp, mkErr := os.CreateTemp(tmpDir, "workcell-publish-commit.*")
	if mkErr != nil {
		return "", noop, &cliexit.ExitCodeError{Code: 1, Message: fmt.Sprintf("publish-pr could not allocate a commit-message temp file: %v", mkErr)}
	}
	if _, wErr := tmp.WriteString(preflight.CommitMessageText); wErr != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", noop, &cliexit.ExitCodeError{Code: 1, Message: fmt.Sprintf("publish-pr could not write commit message: %v", wErr)}
	}
	if cErr := tmp.Close(); cErr != nil {
		_ = os.Remove(tmp.Name())
		return "", noop, &cliexit.ExitCodeError{Code: 1, Message: fmt.Sprintf("publish-pr could not close commit message temp file: %v", cErr)}
	}
	_ = os.Chmod(tmp.Name(), 0o600)
	name := tmp.Name()
	return name, func() { _ = os.Remove(name) }, nil
}

// parseBashContextFlags strips the --bash-* flags off the head of args
// and returns the BashContext plus the remaining args. The flags are
// `--key=value` pairs (bash's `printf %q` keeps each as a single argv
// slot).
func parseBashContextFlags(args []string) (*BashContext, []string) {
	ctx := &BashContext{}
	for len(args) > 0 {
		key, value, ok := strings.Cut(args[0], "=")
		if !ok {
			break
		}
		switch key {
		case "--bash-root-dir":
			ctx.RootDir = value
		case "--bash-workspace-root":
			ctx.WorkspaceRoot = value
		case "--bash-real-home":
			ctx.RealHome = value
		case "--bash-trusted-host-path":
			ctx.TrustedHostPath = value
		case "--bash-host-git-bin":
			ctx.HostGitBin = value
		case "--bash-host-gh-bin":
			ctx.HostGhBin = value
		default:
			return ctx, args
		}
		args = args[1:]
	}
	// scripts/workcell::publish_pr_main always forwards
	// --bash-trusted-host-path=${TRUSTED_HOST_PATH}; the legacy
	// hard-coded fallback table here was never reachable from the
	// real entrypoint and has been removed (W9).  Tests that exercise
	// parseBashContextFlags directly MUST set --bash-trusted-host-path
	// explicitly.
	if ctx.RealHome == "" {
		if home, ok := os.LookupEnv("HOME"); ok {
			ctx.RealHome = home
		}
	}
	return ctx, args
}
