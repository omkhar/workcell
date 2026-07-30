// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package publishpr

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/cliexit"
)

func TestParseArgsAppliesDefaults(t *testing.T) {
	t.Parallel()
	opts, err := ParseArgs(nil)
	if err != nil {
		t.Fatalf("ParseArgs(nil) returned err = %v", err)
	}
	if opts.Base != "main" {
		t.Errorf("Base = %q, want main", opts.Base)
	}
	if opts.Snapshot != "worktree" {
		t.Errorf("Snapshot = %q, want worktree", opts.Snapshot)
	}
	if opts.Ready || opts.DryRun || opts.AllowNonMainBase || opts.ApprovedLargeCertifiedAdapter || opts.HelpRequested {
		t.Errorf("Ready/DryRun/AllowNonMainBase/ApprovedLargeCertifiedAdapter/HelpRequested = %v/%v/%v/%v/%v, want all false",
			opts.Ready, opts.DryRun, opts.AllowNonMainBase, opts.ApprovedLargeCertifiedAdapter, opts.HelpRequested)
	}
}

func TestParseRepositoryNameWithOwner(t *testing.T) {
	t.Parallel()
	got, err := parseRepositoryNameWithOwner("{\"nameWithOwner\":\"example/repo\"}\n")
	if err != nil {
		t.Fatalf("parseRepositoryNameWithOwner() err = %v", err)
	}
	if got != "example/repo" {
		t.Errorf("parseRepositoryNameWithOwner() = %q, want example/repo", got)
	}
	for _, raw := range []string{"null", "{}", "not-json"} {
		if _, err := parseRepositoryNameWithOwner(raw); err == nil {
			t.Errorf("parseRepositoryNameWithOwner(%q) err = nil, want error", raw)
		}
	}
}

func TestRepositorySelectorFromRemoteURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "https", raw: "https://github.com/example/repo.git", want: "https://github.com/example/repo.git"},
		{name: "https-credentials", raw: "https://user:TOKEN@github.com/example/repo.git", want: "https://github.com/example/repo.git"},
		{name: "https-query", raw: "https://github.com/example/repo.git?token=SECRET", want: "https://github.com/example/repo.git"},
		{name: "ssh-url", raw: "ssh://git@github.example.com:2222/example/repo.git", want: "ssh://github.example.com:2222/example/repo.git"},
		{name: "github-ssh-endpoint", raw: "ssh://git@ssh.github.com:443/example/repo.git", want: "ssh://ssh.github.com:443/example/repo.git"},
		{name: "scp", raw: "git@github.com:example/repo.git", want: "github.com/example/repo"},
		{name: "absolute-local-fixture", raw: "/tmp/example/repo.git", want: "/tmp/example/repo.git"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := repositorySelectorFromRemoteURL(tc.raw)
			if err != nil {
				t.Fatalf("repositorySelectorFromRemoteURL() err = %v", err)
			}
			if got != tc.want {
				t.Errorf("repositorySelectorFromRemoteURL() = %q, want %q", got, tc.want)
			}
			for _, secret := range []string{"TOKEN", "SECRET"} {
				if strings.Contains(got, secret) {
					t.Errorf("repositorySelectorFromRemoteURL() leaked %q in %q", secret, got)
				}
			}
		})
	}
	for _, raw := range []string{"", "-R", "https://github.com/example", "git@github.com:-owner/repo.git", "relative/path.git", "https://github.com/example/repo/extra.git", "file://github.com/example/repo.git", "https://user:TOKEN%ZZ@github.com/example/repo.git"} {
		if _, err := repositorySelectorFromRemoteURL(raw); err == nil {
			t.Errorf("repositorySelectorFromRemoteURL(%q) err = nil, want error", raw)
		} else if strings.Contains(err.Error(), "TOKEN") {
			t.Errorf("repositorySelectorFromRemoteURL(%q) leaked credential material in err = %v", raw, err)
		}
	}
}

func TestParseExistingPullRequest(t *testing.T) {
	t.Parallel()
	mainOptions := &Options{Base: "main", Branch: "feature/x"}
	nonMainOptions := &Options{Base: "feature/base", Branch: "feature/x"}
	certifiedOptions := &Options{Base: "main", Branch: "feature/x", ApprovedLargeCertifiedAdapter: true}
	entry := func(repository, base, head, url string, draft bool, labels string) string {
		return `{"baseRefName":"` + base + `","headRefName":"` + head +
			`","headRepository":{"nameWithOwner":"` + repository +
			`"},"isDraft":` + fmt.Sprintf("%t", draft) + `,"labels":` + labels +
			`,"url":"` + url + `"}`
	}
	cases := []struct {
		name          string
		raw           string
		opts          *Options
		want          string
		wantSubstring string
	}{
		{name: "none", raw: "[]\n", opts: mainOptions},
		{name: "one", raw: "[" + entry("example/repo", "main", "feature/x", "https://example.invalid/pr/123", false, "[]") + "]", opts: mainOptions, want: "https://example.invalid/pr/123"},
		{name: "foreign-only", raw: "[" + entry("attacker/repo", "main", "feature/x", "https://example.invalid/pr/foreign", false, "[]") + "]", opts: mainOptions},
		{name: "foreign-and-origin", raw: "[" + entry("attacker/repo", "main", "feature/x", "https://example.invalid/pr/foreign", false, "[]") + "," + entry("example/repo", "main", "feature/x", "https://example.invalid/pr/123", false, "[]") + "]", opts: mainOptions, want: "https://example.invalid/pr/123"},
		{name: "deleted-foreign-only", raw: `[{"baseRefName":"main","headRefName":"feature/x","headRepository":null}]`, opts: mainOptions},
		{name: "deleted-foreign-and-origin", raw: `[{"baseRefName":"main","headRefName":"feature/x","headRepository":null},` + entry("example/repo", "main", "feature/x", "https://example.invalid/pr/123", false, "[]") + "]", opts: mainOptions, want: "https://example.invalid/pr/123"},
		{name: "incomplete-foreign", raw: `[{"baseRefName":"main","headRefName":"feature/x","headRepository":{"nameWithOwner":"attacker/repo"}}]`, opts: mainOptions},
		{name: "null", raw: "null", opts: mainOptions, wantSubstring: "did not return a JSON array"},
		{name: "malformed", raw: "not-json", opts: mainOptions, wantSubstring: "did not return a JSON array"},
		{name: "null-entry", raw: "[null]", opts: mainOptions, wantSubstring: "incomplete entry at index 0"},
		{name: "empty-entry", raw: "[{}]", opts: mainOptions, wantSubstring: "incomplete entry at index 0"},
		{name: "missing-head-repository", raw: `[{"baseRefName":"main","headRefName":"feature/x","isDraft":false,"labels":[],"url":"https://example.invalid/pr/123"}]`, opts: mainOptions, wantSubstring: "incomplete entry at index 0"},
		{name: "empty-head-repository", raw: `[{"baseRefName":"main","headRefName":"feature/x","headRepository":{},"isDraft":false,"labels":[],"url":"https://example.invalid/pr/123"}]`, opts: mainOptions, wantSubstring: "incomplete entry at index 0"},
		{name: "missing-url", raw: "[" + entry("example/repo", "main", "feature/x", "", false, "[]") + "]", opts: mainOptions, wantSubstring: "incomplete entry at index 0"},
		{name: "missing-draft-state", raw: `[{"baseRefName":"main","headRefName":"feature/x","headRepository":{"nameWithOwner":"example/repo"},"labels":[],"url":"https://example.invalid/pr/123"}]`, opts: mainOptions, wantSubstring: "incomplete entry at index 0"},
		{name: "missing-labels", raw: `[{"baseRefName":"main","headRefName":"feature/x","headRepository":{"nameWithOwner":"example/repo"},"isDraft":false,"url":"https://example.invalid/pr/123"}]`, opts: mainOptions, wantSubstring: "incomplete entry at index 0"},
		{name: "ambiguous", raw: "[" + entry("example/repo", "main", "feature/x", "https://example.invalid/pr/1", false, "[]") + "," + entry("example/repo", "main", "feature/x", "https://example.invalid/pr/2", false, "[]") + "]", opts: mainOptions, wantSubstring: "multiple open pull requests"},
		{name: "non-main-draft", raw: "[" + entry("example/repo", "feature/base", "feature/x", "https://example.invalid/pr/123", true, "[]") + "]", opts: nonMainOptions, want: "https://example.invalid/pr/123"},
		{name: "non-main-ready", raw: "[" + entry("example/repo", "feature/base", "feature/x", "https://example.invalid/pr/123", false, "[]") + "]", opts: nonMainOptions, wantSubstring: "not a draft"},
		{name: "certified-label-present", raw: "[" + entry("example/repo", "main", "feature/x", "https://example.invalid/pr/123", false, `[{"name":"approved-large-certified-adapter"}]`) + "]", opts: certifiedOptions, want: "https://example.invalid/pr/123"},
		{name: "certified-label-missing", raw: "[" + entry("example/repo", "main", "feature/x", "https://example.invalid/pr/123", false, "[]") + "]", opts: certifiedOptions, wantSubstring: "without the approved-large-certified-adapter label"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseExistingPullRequest(tc.raw, "example/repo", tc.opts)
			if tc.wantSubstring == "" {
				if err != nil {
					t.Fatalf("parseExistingPullRequest() err = %v", err)
				}
				if got != tc.want {
					t.Errorf("parseExistingPullRequest() = %q, want %q", got, tc.want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantSubstring) {
				t.Errorf("parseExistingPullRequest() err = %v, want substring %q", err, tc.wantSubstring)
			}
		})
	}
}

func TestParseArgsAcceptsAllSupportedFlags(t *testing.T) {
	t.Parallel()
	args := []string{
		"--workspace", "/work",
		"--branch", "feature/x",
		"--base", "feature/review-stack",
		"--allow-non-main-base",
		"--gh-bin", "/opt/homebrew/bin/gh",
		"--snapshot", "index",
		"--title", "T",
		"--title-file", "/tmp/title.txt",
		"--body", "B",
		"--body-file", "/tmp/body.txt",
		"--commit-message", "C",
		"--commit-message-file", "/tmp/commit.txt",
		"--ready",
		"--approved-large-certified-adapter",
		"--dry-run",
	}
	opts, err := ParseArgs(args)
	if err != nil {
		t.Fatalf("ParseArgs() returned err = %v", err)
	}
	want := Options{
		Workspace:                     "/work",
		Branch:                        "feature/x",
		Base:                          "feature/review-stack",
		AllowNonMainBase:              true,
		GhBin:                         "/opt/homebrew/bin/gh",
		Snapshot:                      "index",
		Title:                         "T",
		TitleFile:                     "/tmp/title.txt",
		Body:                          "B",
		BodyFile:                      "/tmp/body.txt",
		CommitMessage:                 "C",
		CommitMessageFile:             "/tmp/commit.txt",
		Ready:                         true,
		ApprovedLargeCertifiedAdapter: true,
		DryRun:                        true,
	}
	got := *opts
	if got != want {
		t.Errorf("ParseArgs() = %+v, want %+v", got, want)
	}
}

func TestParseArgsRejectsUnsupportedOption(t *testing.T) {
	t.Parallel()
	_, err := ParseArgs([]string{"--bogus"})
	ec, ok := cliexit.IsExitCodeError(err)
	if !ok {
		t.Fatalf("ParseArgs() err = %v, want ExitCodeError", err)
	}
	if ec.Code != 2 {
		t.Errorf("ExitCodeError.Code = %d, want 2", ec.Code)
	}
	if !strings.Contains(ec.Message, "Unsupported publish-pr option: --bogus") {
		t.Errorf("ExitCodeError.Message = %q, want substring 'Unsupported publish-pr option: --bogus'", ec.Message)
	}
}

func TestParseArgsRejectsMissingOptionValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
	}{
		{"workspace-bare", []string{"--workspace"}},
		{"workspace-flag-follows", []string{"--workspace", "--branch"}},
		{"branch-empty", []string{"--branch", ""}},
		{"base-bare", []string{"--base"}},
		{"snapshot-bare", []string{"--snapshot"}},
		{"gh-bin-bare", []string{"--gh-bin"}},
		{"title-file-bare", []string{"--title-file"}},
		{"body-file-bare", []string{"--body-file"}},
		{"commit-message-file-bare", []string{"--commit-message-file"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseArgs(tc.args)
			ec, ok := cliexit.IsExitCodeError(err)
			if !ok {
				t.Fatalf("ParseArgs(%v) err = %v, want ExitCodeError", tc.args, err)
			}
			if ec.Code != 2 {
				t.Errorf("ExitCodeError.Code = %d, want 2", ec.Code)
			}
			if !strings.Contains(ec.Message, "requires a value") {
				t.Errorf("ExitCodeError.Message = %q, want substring 'requires a value'", ec.Message)
			}
		})
	}
}

func TestParseArgsAcceptsTitleOrBodyStartingWithDoubleDash(t *testing.T) {
	t.Parallel()
	// raw_option_value_or_die in bash accepts values starting with --
	// for the user-supplied free-form text inputs. ParseArgs preserves
	// that asymmetry.
	cases := []struct {
		flag  string
		value string
		check func(*Options) string
	}{
		{"--title", "--title-with-dashes", func(o *Options) string { return o.Title }},
		{"--body", "--body-with-dashes", func(o *Options) string { return o.Body }},
		{"--commit-message", "--cm-with-dashes", func(o *Options) string { return o.CommitMessage }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.flag, func(t *testing.T) {
			t.Parallel()
			opts, err := ParseArgs([]string{tc.flag, tc.value})
			if err != nil {
				t.Fatalf("ParseArgs(%q %q) err = %v", tc.flag, tc.value, err)
			}
			if got := tc.check(opts); got != tc.value {
				t.Errorf("parsed value = %q, want %q", got, tc.value)
			}
		})
	}
}

func TestParseArgsHelpFlagShortCircuits(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"-h", "--help"} {
		flag := flag
		t.Run(flag, func(t *testing.T) {
			t.Parallel()
			opts, err := ParseArgs([]string{flag, "--branch", "should-not-parse"})
			if err != nil {
				t.Fatalf("ParseArgs(%q) err = %v", flag, err)
			}
			if !opts.HelpRequested {
				t.Errorf("HelpRequested = false, want true")
			}
			if opts.Branch != "" {
				t.Errorf("Branch parsed past help flag: %q", opts.Branch)
			}
		})
	}
}

func TestValidateSnapshotName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"index", "worktree"} {
		if err := ValidateSnapshotName(name); err != nil {
			t.Errorf("ValidateSnapshotName(%q) err = %v, want nil", name, err)
		}
	}
	err := ValidateSnapshotName("rebased")
	ec, ok := cliexit.IsExitCodeError(err)
	if !ok {
		t.Fatalf("err = %v, want ExitCodeError", err)
	}
	if ec.Code != 2 {
		t.Errorf("Code = %d, want 2", ec.Code)
	}
	for _, want := range []string{
		"Unsupported publish snapshot: rebased",
		"Use --snapshot index or --snapshot worktree.",
	} {
		if !strings.Contains(ec.Message, want) {
			t.Errorf("Message = %q, want substring %q", ec.Message, want)
		}
	}
}

func TestValidateBranchName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		branch         string
		checkRefFormat CheckRefFormatFunc
		wantSubstring  string
	}{
		{"empty", "", nil, "publish-pr requires --branch."},
		{"main-default", "main", nil, "refuses the default branch"},
		{"master-default", "master", nil, "refuses the default branch"},
		{"check-ref-rejects", "feature/x", func(string) bool { return false }, "Invalid publish branch name: feature/x"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateBranchName(tc.branch, tc.checkRefFormat)
			ec, ok := cliexit.IsExitCodeError(err)
			if !ok {
				t.Fatalf("err = %v, want ExitCodeError", err)
			}
			if ec.Code != 2 {
				t.Errorf("Code = %d, want 2", ec.Code)
			}
			if !strings.Contains(ec.Message, tc.wantSubstring) {
				t.Errorf("Message = %q, want substring %q", ec.Message, tc.wantSubstring)
			}
		})
	}

	if err := ValidateBranchName("feature/ok", func(string) bool { return true }); err != nil {
		t.Errorf("happy-path err = %v, want nil", err)
	}
	if err := ValidateBranchName("feature/no-check", nil); err != nil {
		t.Errorf("nil-check err = %v, want nil (no ref-format check applied)", err)
	}
}

func TestValidateBaseName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		base           string
		allowNonMain   bool
		checkRefFormat CheckRefFormatFunc
		wantSubstring  string
	}{
		{"empty", "", false, nil, "requires a non-empty --base"},
		{"non-main-no-waiver", "feature/x", false, nil, "only supports --base main by default"},
		{"check-ref-rejects", "bad..ref", false, func(string) bool { return false }, "Invalid publish base branch name: bad..ref"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateBaseName(tc.base, tc.allowNonMain, tc.checkRefFormat)
			ec, ok := cliexit.IsExitCodeError(err)
			if !ok {
				t.Fatalf("err = %v, want ExitCodeError", err)
			}
			if ec.Code != 2 {
				t.Errorf("Code = %d, want 2", ec.Code)
			}
			if !strings.Contains(ec.Message, tc.wantSubstring) {
				t.Errorf("Message = %q, want substring %q", ec.Message, tc.wantSubstring)
			}
		})
	}

	// Main base accepted without waiver, non-main accepted with waiver.
	if err := ValidateBaseName("main", false, func(string) bool { return true }); err != nil {
		t.Errorf("main err = %v, want nil", err)
	}
	if err := ValidateBaseName("feature/review-stack", true, func(string) bool { return true }); err != nil {
		t.Errorf("waivered non-main err = %v, want nil", err)
	}
}

func TestRepoOwnedChecksExpected(t *testing.T) {
	t.Parallel()
	if got := RepoOwnedChecksExpected("main"); got != "1" {
		t.Errorf("RepoOwnedChecksExpected(main) = %q, want 1", got)
	}
	if got := RepoOwnedChecksExpected("feature/review-stack"); got != "0" {
		t.Errorf("RepoOwnedChecksExpected(non-main) = %q, want 0", got)
	}
}

func TestLoadTextArg(t *testing.T) {
	t.Parallel()
	reader := func(p string) (string, error) {
		switch p {
		case "ok":
			return "from-file", nil
		case "missing":
			return "", errors.New("stat ok")
		}
		return "", errors.New("unknown")
	}

	t.Run("inline-and-file-both-set", func(t *testing.T) {
		t.Parallel()
		_, err := LoadTextArg("inline", "ok", "title", true, reader)
		ec, ok := cliexit.IsExitCodeError(err)
		if !ok || ec.Code != 2 {
			t.Fatalf("err = %v, want ExitCodeError{Code=2}", err)
		}
		if !strings.Contains(ec.Message, "Use only one of --title or --title-file.") {
			t.Errorf("Message = %q", ec.Message)
		}
	})

	t.Run("file-wins-when-inline-empty", func(t *testing.T) {
		t.Parallel()
		got, err := LoadTextArg("", "ok", "title", true, reader)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got != "from-file" {
			t.Errorf("got = %q, want from-file", got)
		}
	})

	t.Run("file-missing-reports-label", func(t *testing.T) {
		t.Parallel()
		_, err := LoadTextArg("", "missing", "body", false, reader)
		ec, ok := cliexit.IsExitCodeError(err)
		if !ok {
			t.Fatalf("err = %v, want ExitCodeError", err)
		}
		if !strings.Contains(ec.Message, "body file does not exist: missing") {
			t.Errorf("Message = %q", ec.Message)
		}
	})

	t.Run("inline-returned-verbatim", func(t *testing.T) {
		t.Parallel()
		got, err := LoadTextArg("hello", "", "title", true, reader)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got != "hello" {
			t.Errorf("got = %q, want hello", got)
		}
	})

	t.Run("optional-empty-ok", func(t *testing.T) {
		t.Parallel()
		got, err := LoadTextArg("", "", "body", false, reader)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got != "" {
			t.Errorf("got = %q, want empty", got)
		}
	})

	t.Run("required-empty-fails", func(t *testing.T) {
		t.Parallel()
		_, err := LoadTextArg("", "", "title", true, reader)
		ec, ok := cliexit.IsExitCodeError(err)
		if !ok || ec.Code != 2 {
			t.Fatalf("err = %v, want ExitCodeError{Code=2}", err)
		}
		if !strings.Contains(ec.Message, "publish-pr requires --title or --title-file.") {
			t.Errorf("Message = %q", ec.Message)
		}
	})
}

func TestPreflightHappyPathMain(t *testing.T) {
	t.Parallel()
	opts := &Options{
		Branch:        "feature/x",
		Base:          "main",
		Snapshot:      "worktree",
		Title:         "Title",
		CommitMessage: "Commit",
		Ready:         true,
	}
	result, err := Preflight(opts, func(string) bool { return true }, nil)
	if err != nil {
		t.Fatalf("Preflight err = %v", err)
	}
	if result.PublishBaseMode != "main" {
		t.Errorf("PublishBaseMode = %q, want main", result.PublishBaseMode)
	}
	if result.RepoOwnedPRChecksExpected != "1" {
		t.Errorf("RepoOwnedPRChecksExpected = %q, want 1", result.RepoOwnedPRChecksExpected)
	}
	if !result.Ready {
		t.Errorf("Ready = false, want true (main base preserves --ready)")
	}
	if len(result.LowerAssuranceNotice) != 0 {
		t.Errorf("LowerAssuranceNotice = %v, want empty", result.LowerAssuranceNotice)
	}
}

func TestPreflightDowngradesLowerAssuranceNonMain(t *testing.T) {
	t.Parallel()
	opts := &Options{
		Branch:           "feature/x",
		Base:             "feature/review-stack",
		AllowNonMainBase: true,
		Snapshot:         "worktree",
		Title:            "Title",
		CommitMessage:    "Commit",
		Ready:            true,
	}
	result, err := Preflight(opts, func(string) bool { return true }, nil)
	if err != nil {
		t.Fatalf("Preflight err = %v", err)
	}
	if result.PublishBaseMode != "lower-assurance-non-main" {
		t.Errorf("PublishBaseMode = %q, want lower-assurance-non-main", result.PublishBaseMode)
	}
	if result.RepoOwnedPRChecksExpected != "0" {
		t.Errorf("RepoOwnedPRChecksExpected = %q, want 0", result.RepoOwnedPRChecksExpected)
	}
	if result.Ready {
		t.Errorf("Ready = true, want false (non-main base must stay draft)")
	}
	if len(result.LowerAssuranceNotice) != 2 {
		t.Fatalf("LowerAssuranceNotice len = %d, want 2", len(result.LowerAssuranceNotice))
	}
	if !strings.Contains(result.LowerAssuranceNotice[0], "feature/review-stack") {
		t.Errorf("notice[0] = %q, want substring 'feature/review-stack'", result.LowerAssuranceNotice[0])
	}
	if !strings.Contains(result.LowerAssuranceNotice[1], "lower-assurance mode") {
		t.Errorf("notice[1] = %q, want substring 'lower-assurance mode'", result.LowerAssuranceNotice[1])
	}
}

func TestPreflightRejectsEmptyTitleAndCommit(t *testing.T) {
	t.Parallel()
	// Empty title is rejected (the bash function exits 2 with
	// "requires a non-empty PR title" after LoadTextArg returns "").
	opts := &Options{
		Branch:        "feature/x",
		Base:          "main",
		Snapshot:      "worktree",
		Title:         " ",
		CommitMessage: "commit",
	}
	// A space-only title is treated as non-empty by the bash function
	// because it uses `-z` which only rejects truly empty strings; the
	// Go translation matches that behaviour. So this test focuses on
	// the truly-empty path via missing inline + missing file.
	if _, err := Preflight(opts, func(string) bool { return true }, nil); err != nil {
		t.Fatalf("Preflight space-only title err = %v, want nil", err)
	}

	emptyTitleOpts := &Options{
		Branch:        "feature/x",
		Base:          "main",
		Snapshot:      "worktree",
		CommitMessage: "commit",
	}
	_, err := Preflight(emptyTitleOpts, func(string) bool { return true }, nil)
	ec, ok := cliexit.IsExitCodeError(err)
	if !ok {
		t.Fatalf("err = %v, want ExitCodeError", err)
	}
	if !strings.Contains(ec.Message, "--title or --title-file") {
		t.Errorf("Message = %q, want substring '--title or --title-file'", ec.Message)
	}

	emptyCommitOpts := &Options{
		Branch:   "feature/x",
		Base:     "main",
		Snapshot: "worktree",
		Title:    "T",
	}
	_, err = Preflight(emptyCommitOpts, func(string) bool { return true }, nil)
	ec, ok = cliexit.IsExitCodeError(err)
	if !ok {
		t.Fatalf("err = %v, want ExitCodeError", err)
	}
	if !strings.Contains(ec.Message, "--commit-message or --commit-message-file") {
		t.Errorf("Message = %q, want substring '--commit-message or --commit-message-file'", ec.Message)
	}
}

func TestPreflightPropagatesValidatorErrors(t *testing.T) {
	t.Parallel()
	// Snapshot validator failure short-circuits before text-arg loading.
	bad := &Options{
		Branch:        "feature/x",
		Base:          "main",
		Snapshot:      "bogus",
		Title:         "T",
		CommitMessage: "C",
	}
	_, err := Preflight(bad, func(string) bool { return true }, nil)
	ec, ok := cliexit.IsExitCodeError(err)
	if !ok {
		t.Fatalf("err = %v, want ExitCodeError", err)
	}
	if !strings.Contains(ec.Message, "Unsupported publish snapshot: bogus") {
		t.Errorf("Message = %q", ec.Message)
	}
}

func TestPreflightRejectsNilOptions(t *testing.T) {
	t.Parallel()
	_, err := Preflight(nil, nil, nil)
	ec, ok := cliexit.IsExitCodeError(err)
	if !ok {
		t.Fatalf("err = %v, want ExitCodeError", err)
	}
	if !strings.Contains(ec.Message, "requires parsed options") {
		t.Errorf("Message = %q", ec.Message)
	}
}

func TestWriteUsageMatchesUsageText(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	WriteUsage(&buf)
	if buf.String() != UsageText() {
		t.Errorf("WriteUsage output diverges from UsageText()")
	}
}

func TestIsExitCodeErrorWraps(t *testing.T) {
	t.Parallel()
	base := &cliexit.ExitCodeError{Code: 7, Message: "boom"}
	wrapped := wrapErrForTest(base)
	got, ok := cliexit.IsExitCodeError(wrapped)
	if !ok {
		t.Fatalf("cliexit.IsExitCodeError(wrapped) = false, want true")
	}
	if got.Code != 7 || got.Message != "boom" {
		t.Errorf("unwrapped = %+v, want {Code:7 Message:boom}", got)
	}
	if _, ok := cliexit.IsExitCodeError(errors.New("plain")); ok {
		t.Errorf("cliexit.IsExitCodeError(plain) = true, want false")
	}
}

func wrapErrForTest(err error) error {
	return errors.Join(errors.New("preamble"), err)
}
