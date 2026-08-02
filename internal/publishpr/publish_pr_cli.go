// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package publishpr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/shellproto"
)

const approvedLargeCertifiedAdapterLabel = "approved-large-certified-adapter"

type pullRequestListEntry struct {
	BaseRefName    string          `json:"baseRefName"`
	HeadRefName    string          `json:"headRefName"`
	HeadRepository json.RawMessage `json:"headRepository"`
	IsDraft        *bool           `json:"isDraft"`
	Labels         *[]struct {
		Name string `json:"name"`
	} `json:"labels"`
	URL string `json:"url"`
}

type pullRequestHeadRepository struct {
	NameWithOwner string `json:"nameWithOwner"`
}

type repositoryView struct {
	NameWithOwner string `json:"nameWithOwner"`
}

func parseRepositoryNameWithOwner(raw string) (string, error) {
	var repository repositoryView
	if err := json.Unmarshal([]byte(raw), &repository); err != nil {
		return "", &cliexit.ExitCodeError{Code: 1, Message: fmt.Sprintf("publish-pr could not parse the origin repository lookup: %v", err)}
	}
	nameWithOwner := strings.TrimSpace(repository.NameWithOwner)
	if nameWithOwner == "" {
		return "", &cliexit.ExitCodeError{Code: 1, Message: "publish-pr could not determine the origin repository identity."}
	}
	return nameWithOwner, nil
}

func repositorySelectorFromRemoteURL(raw string) (string, error) {
	remoteURL := strings.TrimSpace(raw)
	if remoteURL == "" || strings.ContainsAny(remoteURL, "\x00\r\n") {
		return "", &cliexit.ExitCodeError{Code: 2, Message: "publish-pr could not derive a GitHub repository selector from the origin push URL."}
	}
	if strings.HasPrefix(remoteURL, "/") {
		return remoteURL, nil
	}

	selectorFromHostPath := func(host, path string) (string, bool) {
		host = strings.TrimSpace(host)
		path = strings.Trim(strings.TrimSpace(path), "/")
		path = strings.TrimSuffix(path, ".git")
		if host == "" || strings.HasPrefix(host, "-") {
			return "", false
		}
		parts := strings.Split(path, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" ||
			strings.HasPrefix(parts[0], "-") || strings.HasPrefix(parts[1], "-") {
			return "", false
		}
		return strings.ToLower(host) + "/" + path, true
	}

	if schemeEnd := strings.Index(remoteURL, "://"); schemeEnd > 0 {
		scheme := strings.ToLower(remoteURL[:schemeEnd])
		if scheme != "https" && scheme != "ssh" {
			return "", &cliexit.ExitCodeError{Code: 2, Message: "publish-pr could not derive a GitHub repository selector from the origin push URL."}
		}
		parsed, err := url.Parse(remoteURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return "", &cliexit.ExitCodeError{Code: 2, Message: "publish-pr could not derive a GitHub repository selector from the origin push URL."}
		}
		if _, ok := selectorFromHostPath(parsed.Host, parsed.Path); !ok {
			return "", &cliexit.ExitCodeError{Code: 2, Message: "publish-pr could not derive a GitHub repository selector from the origin push URL."}
		}
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.ForceQuery = false
		parsed.Fragment = ""
		parsed.RawFragment = ""
		return parsed.String(), nil
	}

	if colon := strings.IndexByte(remoteURL, ':'); colon > 0 && !strings.Contains(remoteURL[:colon], "/") {
		host := remoteURL[:colon]
		if at := strings.LastIndexByte(host, '@'); at >= 0 {
			host = host[at+1:]
		}
		if selector, ok := selectorFromHostPath(host, remoteURL[colon+1:]); ok {
			return selector, nil
		}
	}

	return "", &cliexit.ExitCodeError{Code: 2, Message: "publish-pr could not derive a GitHub repository selector from the origin push URL."}
}

func parseExistingPullRequest(raw, repositoryNameWithOwner string, opts *Options) (string, error) {
	if opts == nil {
		return "", &cliexit.ExitCodeError{Code: 1, Message: "publish-pr could not validate the existing pull request without publication options."}
	}
	trimmed := bytes.TrimSpace([]byte(raw))
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return "", &cliexit.ExitCodeError{Code: 1, Message: "publish-pr existing pull request lookup did not return a JSON array."}
	}
	var entries []pullRequestListEntry
	if err := json.Unmarshal(trimmed, &entries); err != nil {
		return "", &cliexit.ExitCodeError{Code: 1, Message: fmt.Sprintf("publish-pr could not parse the existing pull request lookup: %v", err)}
	}
	matches := make([]pullRequestListEntry, 0, len(entries))
	for index, entry := range entries {
		if strings.TrimSpace(entry.BaseRefName) == "" ||
			strings.TrimSpace(entry.HeadRefName) == "" {
			return "", &cliexit.ExitCodeError{
				Code:    1,
				Message: fmt.Sprintf("publish-pr existing pull request lookup returned an incomplete entry at index %d.", index),
			}
		}
		if entry.BaseRefName != opts.Base || entry.HeadRefName != opts.Branch {
			continue
		}

		headRepositoryJSON := bytes.TrimSpace(entry.HeadRepository)
		if len(headRepositoryJSON) == 0 {
			return "", &cliexit.ExitCodeError{
				Code:    1,
				Message: fmt.Sprintf("publish-pr existing pull request lookup returned an incomplete entry at index %d.", index),
			}
		}
		if bytes.Equal(headRepositoryJSON, []byte("null")) {
			continue
		}
		var headRepository pullRequestHeadRepository
		if err := json.Unmarshal(headRepositoryJSON, &headRepository); err != nil ||
			strings.TrimSpace(headRepository.NameWithOwner) == "" {
			return "", &cliexit.ExitCodeError{
				Code:    1,
				Message: fmt.Sprintf("publish-pr existing pull request lookup returned an incomplete entry at index %d.", index),
			}
		}
		if headRepository.NameWithOwner != repositoryNameWithOwner {
			continue
		}
		if entry.IsDraft == nil ||
			entry.Labels == nil ||
			strings.TrimSpace(entry.URL) == "" {
			return "", &cliexit.ExitCodeError{
				Code:    1,
				Message: fmt.Sprintf("publish-pr existing pull request lookup returned an incomplete entry at index %d.", index),
			}
		}
		matches = append(matches, entry)
	}
	switch len(matches) {
	case 0:
		return "", nil
	case 1:
		entry := matches[0]
		url := strings.TrimSpace(entry.URL)
		if opts.Base != "main" && !*entry.IsDraft {
			return "", &cliexit.ExitCodeError{Code: 2, Message: "publish-pr found a matching non-main pull request that is not a draft; convert it to draft or close it before retrying."}
		}
		if opts.ApprovedLargeCertifiedAdapter {
			hasRequiredLabel := false
			for _, label := range *entry.Labels {
				if label.Name == approvedLargeCertifiedAdapterLabel {
					hasRequiredLabel = true
					break
				}
			}
			if !hasRequiredLabel {
				return "", &cliexit.ExitCodeError{Code: 2, Message: "publish-pr found a matching pull request without the approved-large-certified-adapter label; add the label or close the pull request before retrying."}
			}
		}
		return url, nil
	default:
		return "", &cliexit.ExitCodeError{Code: 1, Message: "publish-pr found multiple open pull requests for the selected base and branch."}
	}
}

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
	resolveRepositorySelector := func() (string, error) {
		originPushURL, originErr := remoteOriginPushURL(ctx, resolvedWorkspace)
		if originErr != nil {
			return "", &cliexit.ExitCodeError{Code: 2, Message: fmt.Sprintf("publish-pr requires exactly one origin push URL in %s.", resolvedWorkspace)}
		}
		return repositorySelectorFromRemoteURL(originPushURL)
	}
	for _, line := range preflight.LowerAssuranceNotice {
		fmt.Fprintln(stderr, line)
	}

	current := currentBranch(ctx, resolvedWorkspace)
	dryRunRepositorySelector := ""
	if opts.DryRun {
		if current != opts.Branch {
			hasOnBranchIncludes, includeErr := hasOnBranchGitIncludes(ctx, resolvedWorkspace)
			if includeErr != nil {
				return includeErr
			}
			if hasOnBranchIncludes {
				return &cliexit.ExitCodeError{
					Code: 2,
					Message: fmt.Sprintf(
						"publish-pr dry-run cannot safely resolve branch-conditioned Git configuration before switching from %s to %s; check out the publication branch and retry.",
						current,
						opts.Branch,
					),
				}
			}
		}
		dryRunRepositorySelector, err = resolveRepositorySelector()
		if err != nil {
			return err
		}
	}
	publishExistingCommits := 0
	hasChanges := func() bool {
		if opts.Snapshot == "worktree" {
			return hasWorktreeChanges(ctx, resolvedWorkspace)
		}
		return hasStagedChanges(ctx, resolvedWorkspace)
	}
	if !hasChanges() {
		if current == opts.Branch {
			publishExistingCommits = 1
		} else if branchExists(ctx, resolvedWorkspace, opts.Branch) {
			return &cliexit.ExitCodeError{Code: 2, Message: fmt.Sprintf("publish-pr existing-branch mode requires branch %s to be checked out in %s.", opts.Branch, resolvedWorkspace)}
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
	if opts.ApprovedLargeCertifiedAdapter {
		shapeCmd = append(shapeCmd, "--allow-certified-adapter-shape")
	}
	pushCmd := clone("push", "--no-verify", "-u", "origin", opts.Branch)

	draft := !preflight.Ready
	resolvedBodyFile := ""
	if opts.BodyFile != "" {
		var bodyErr error
		resolvedBodyFile, bodyErr = resolveExistingFileOrDie(ctx, opts.BodyFile, "body")
		if bodyErr != nil {
			return bodyErr
		}
	}
	buildGitHubCommands := func(repositorySelector string) (repoViewCmd, prListCmd, prCmd []string) {
		repoViewCmd = []string{ctx.HostGhBin, "repo", "view", repositorySelector, "--json", "nameWithOwner"}
		prListCmd = []string{
			ctx.HostGhBin,
			"pr", "list",
			"-R", repositorySelector,
			"--base", opts.Base,
			"--head", opts.Branch,
			"--state", "open",
			"--json", "baseRefName,headRefName,headRepository,isDraft,labels,url",
			"--limit", "100",
		}
		prCmd = []string{ctx.HostGhBin, "pr", "create", "-R", repositorySelector, "--base", opts.Base, "--head", opts.Branch, "--title", preflight.TitleText}
		if opts.ApprovedLargeCertifiedAdapter {
			prCmd = append(prCmd, "--label", approvedLargeCertifiedAdapterLabel)
		}
		if draft {
			prCmd = append(prCmd, "--draft")
		}
		if resolvedBodyFile != "" {
			prCmd = append(prCmd, "--body-file", resolvedBodyFile)
		} else {
			prCmd = append(prCmd, "--body", preflight.BodyText)
		}
		return repoViewCmd, prListCmd, prCmd
	}

	if opts.DryRun {
		repoViewCmd, prListCmd, prCmd := buildGitHubCommands(dryRunRepositorySelector)
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
		EmitCommand(stdout, repoViewCmd)
		EmitCommand(stdout, prListCmd)
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
	repositorySelector, err := resolveRepositorySelector()
	if err != nil {
		return err
	}
	repoViewCmd, prListCmd, prCmd := buildGitHubCommands(repositorySelector)
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
	var repoViewOut strings.Builder
	if err := run(repoViewCmd, &repoViewOut); err != nil {
		return err
	}
	repositoryNameWithOwner, err := parseRepositoryNameWithOwner(repoViewOut.String())
	if err != nil {
		return err
	}
	lookupExistingPR := func() (string, error) {
		var prListOut strings.Builder
		if err := run(prListCmd, &prListOut); err != nil {
			return "", err
		}
		return parseExistingPullRequest(prListOut.String(), repositoryNameWithOwner, opts)
	}
	prURL, err := lookupExistingPR()
	if err != nil {
		return err
	}
	if prURL == "" {
		var prOut strings.Builder
		if createErr := run(prCmd, &prOut); createErr != nil {
			prURL, err = lookupExistingPR()
			if err != nil {
				return err
			}
			if prURL == "" {
				return createErr
			}
		} else {
			prURL = strings.TrimRight(prOut.String(), "\n")
		}
	}
	// Fail-closed: render through a buffer first so a forbidden control
	// character in any field aborts the whole plan emission rather than
	// leaving the bash shim with a half-imported plan. Applies the same
	// fail-closed contract as FormatBundleResultForShell in
	// internal/injection (which buffers into a strings.Builder and
	// returns the rendered string to its caller); the structural shape
	// is different — this emitter writes the buffer to its io.Writer
	// directly because the caller passes stdout in.
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
