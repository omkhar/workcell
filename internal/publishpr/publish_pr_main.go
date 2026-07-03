// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package publishpr

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/omkhar/workcell/internal/cliexit"
)

func exit2(format string, args ...any) error {
	return &cliexit.ExitCodeError{Code: 2, Message: fmt.Sprintf(format, args...)}
}

// Options carries the parsed flag set for `workcell publish-pr`.
// Fields mirror the local variables in the bash publish_pr_main
// argument-parsing loop so the byte-for-byte semantics survive the
// translation. Inline-vs-file text inputs are kept as separate fields
// because the bash function rejects both being set; LoadTextArg
// reconciles them later when the caller is ready to read the body.
type Options struct {
	// Workspace is the host-side worktree to operate on; defaults to
	// the process CWD.  Must resolve to an existing directory; rejected
	// otherwise by resolveExistingDirectoryOrDie.
	Workspace string
	// Branch is the publish branch name; required (rejected when empty),
	// must pass `git check-ref-format --branch`, and must not be the
	// default branch (main/master).
	Branch string
	// Base is the target branch for the PR; defaults to "main".  Must
	// pass `git check-ref-format --branch`; non-main values require
	// AllowNonMainBase.
	Base string
	// AllowNonMainBase waives the main-only Base check, downgrading the
	// run to the lower-assurance draft path.
	AllowNonMainBase bool
	// GhBin is an optional explicit path to the host `gh` binary; when
	// non-empty it must point to a trusted executable
	// (IsTrustedHostToolPath).
	GhBin string
	// Snapshot selects which working-tree slice to publish; must be
	// "worktree" or "index"; rejected otherwise by ValidateSnapshotName.
	// Defaults to "worktree".
	Snapshot string
	// Title is the inline PR title.  Mutually exclusive with TitleFile;
	// one of the two MUST yield a non-empty value at Preflight.
	Title string
	// TitleFile is a host-path whose trimmed contents become the PR
	// title.  Mutually exclusive with Title.
	TitleFile string
	// Body is the inline PR body.  Mutually exclusive with BodyFile;
	// both may be empty (PR body is optional).
	Body string
	// BodyFile is a host-path whose trimmed contents become the PR body.
	// Mutually exclusive with Body.
	BodyFile string
	// CommitMessage is the inline commit message used for the publish
	// commit.  Mutually exclusive with CommitMessageFile; one of the
	// two MUST yield a non-empty value at Preflight.
	CommitMessage string
	// CommitMessageFile is a host-path whose trimmed contents become
	// the commit message.  Mutually exclusive with CommitMessage.
	CommitMessageFile string
	// Ready, when true, flips the gh PR off draft (publish_pr_main's
	// `--ready` flag).
	Ready bool
	// ApprovedLargeCertifiedAdapter allows a larger but still bounded
	// PR-shape gate for reviewed, live-certified adapter support PRs.
	ApprovedLargeCertifiedAdapter bool
	// DryRun, when true, prints the planned host commands instead of
	// executing them; output shape is asserted by
	// tests/scenarios/shared/test-publish-pr-dry-run.sh.
	DryRun bool
	// HelpRequested is set when ParseArgs encountered -h / --help and
	// short-circuited; callers should emit UsageText and exit 0.
	HelpRequested bool
}

// DefaultOptions returns an Options populated with the same defaults
// the bash publish_pr_main function applies before its argument loop:
// base=main, snapshot=worktree, workspace=$PWD.
func DefaultOptions() *Options {
	cwd, _ := os.Getwd()
	return &Options{
		Workspace: cwd,
		Base:      "main",
		Snapshot:  "worktree",
	}
}

// ParseArgs parses the publish-pr argument vector. It mirrors the
// while/case parsing loop in scripts/workcell publish_pr_main and
// uses the same error messages so user-visible stderr stays identical.
// ParseArgs does NOT run --branch / --base / --snapshot validators;
// callers compose ParseArgs with Validate to keep the responsibilities
// separated and the unit tests focused.
func ParseArgs(args []string) (*Options, error) {
	opts := DefaultOptions()
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--workspace":
			v, err := optionValueOrDie(arg, peek(args, i+1))
			if err != nil {
				return nil, err
			}
			opts.Workspace = v
			i++
		case "--branch":
			v, err := optionValueOrDie(arg, peek(args, i+1))
			if err != nil {
				return nil, err
			}
			opts.Branch = v
			i++
		case "--base":
			v, err := optionValueOrDie(arg, peek(args, i+1))
			if err != nil {
				return nil, err
			}
			opts.Base = v
			i++
		case "--allow-non-main-base":
			opts.AllowNonMainBase = true
		case "--gh-bin":
			v, err := optionValueOrDie(arg, peek(args, i+1))
			if err != nil {
				return nil, err
			}
			opts.GhBin = v
			i++
		case "--snapshot":
			v, err := optionValueOrDie(arg, peek(args, i+1))
			if err != nil {
				return nil, err
			}
			opts.Snapshot = v
			i++
		case "--title":
			v, err := rawOptionValueOrDie(arg, peek(args, i+1))
			if err != nil {
				return nil, err
			}
			opts.Title = v
			i++
		case "--title-file":
			v, err := optionValueOrDie(arg, peek(args, i+1))
			if err != nil {
				return nil, err
			}
			opts.TitleFile = v
			i++
		case "--body":
			v, err := rawOptionValueOrDie(arg, peek(args, i+1))
			if err != nil {
				return nil, err
			}
			opts.Body = v
			i++
		case "--body-file":
			v, err := optionValueOrDie(arg, peek(args, i+1))
			if err != nil {
				return nil, err
			}
			opts.BodyFile = v
			i++
		case "--commit-message":
			v, err := rawOptionValueOrDie(arg, peek(args, i+1))
			if err != nil {
				return nil, err
			}
			opts.CommitMessage = v
			i++
		case "--commit-message-file":
			v, err := optionValueOrDie(arg, peek(args, i+1))
			if err != nil {
				return nil, err
			}
			opts.CommitMessageFile = v
			i++
		case "--ready":
			opts.Ready = true
		case "--approved-large-certified-adapter":
			opts.ApprovedLargeCertifiedAdapter = true
		case "--dry-run":
			opts.DryRun = true
		case "-h", "--help":
			opts.HelpRequested = true
			return opts, nil
		default:
			return nil, exit2("Unsupported publish-pr option: %s", arg)
		}
	}
	return opts, nil
}

// peek returns args[i] if i is in range, or "" otherwise. The bash
// function uses "${2-}" to surface the next positional argument
// without dying on set -u; this helper preserves that behaviour.
func peek(args []string, i int) string {
	if i < 0 || i >= len(args) {
		return ""
	}
	return args[i]
}

// optionValueOrDie mirrors scripts/workcell option_value_or_die:
// the value may not be empty and may not start with `--`.
func optionValueOrDie(option, value string) (string, error) {
	if value == "" || strings.HasPrefix(value, "--") {
		return "", exit2("Option %s requires a value.", option)
	}
	return value, nil
}

// rawOptionValueOrDie mirrors scripts/workcell raw_option_value_or_die:
// only emptiness is rejected, because user-supplied free-form text
// (titles, bodies, commit messages) is allowed to start with `--`.
func rawOptionValueOrDie(option, value string) (string, error) {
	if value == "" {
		return "", exit2("Option %s requires a value.", option)
	}
	return value, nil
}

// ValidateSnapshotName mirrors validate_publish_snapshot_name. The
// bash function emits a two-line stderr message and exits 2; the Go
// translation packs both lines into the ExitCodeError so callers can
// write them straight to stderr without re-formatting.
func ValidateSnapshotName(snapshot string) error {
	switch snapshot {
	case "index", "worktree":
		return nil
	default:
		return exit2("Unsupported publish snapshot: %s\nUse --snapshot index or --snapshot worktree.", snapshot)
	}
}

// CheckRefFormatFunc lets callers inject a `git check-ref-format`
// adapter. ValidateBranchName and ValidateBaseName invoke it to honour
// the bash check that delegates to `${HOST_GIT_BIN} check-ref-format
// --branch ${branch}`. Returning false means the branch name is
// invalid.
type CheckRefFormatFunc func(branch string) bool

// ValidateBranchName mirrors validate_publish_branch_name: the branch
// must be non-empty, must not be the default (main/master), and must
// pass `git check-ref-format --branch`.
func ValidateBranchName(branch string, checkRefFormat CheckRefFormatFunc) error {
	if branch == "" {
		return exit2("publish-pr requires --branch.")
	}
	if branch == "main" || branch == "master" {
		return exit2("publish-pr refuses the default branch. Use a feature branch instead.")
	}
	if checkRefFormat != nil && !checkRefFormat(branch) {
		return exit2("Invalid publish branch name: %s", branch)
	}
	return nil
}

// ValidateBaseName mirrors validate_publish_base_name: the base must
// be non-empty, must pass `git check-ref-format --branch`, and must
// either be main or carry an explicit --allow-non-main-base waiver.
func ValidateBaseName(base string, allowNonMainBase bool, checkRefFormat CheckRefFormatFunc) error {
	if base == "" {
		return exit2("publish-pr requires a non-empty --base branch name.")
	}
	if checkRefFormat != nil && !checkRefFormat(base) {
		return exit2("Invalid publish base branch name: %s", base)
	}
	if base != "main" && !allowNonMainBase {
		return exit2("publish-pr only supports --base main by default. Use --allow-non-main-base for an explicit lower-assurance draft PR.")
	}
	return nil
}

// RepoOwnedChecksExpected mirrors publish_pr_repo_owned_checks_expected:
// repo-owned PR checks are expected only when the base is main. The
// string return type matches the dry-run output contract (the bash
// function prints `publish_repo_owned_pr_checks_expected=1` or `=0` to
// stdout, and the scenario regression test in
// tests/scenarios/shared/test-publish-pr-dry-run.sh greps for exactly
// those literals).
func RepoOwnedChecksExpected(base string) string {
	if base == "main" {
		return "1"
	}
	return "0"
}

// LoadTextArg mirrors publish_pr_load_text_arg: the bash helper accepts
// either an inline value (--title) or a file value (--title-file) but
// not both, reads the file when set, and rejects an empty value when
// the field is required. The fileReader hook lets tests swap a real
// disk read for an in-memory fixture.
func LoadTextArg(inline, file, label string, required bool, fileReader func(string) (string, error)) (string, error) {
	if inline != "" && file != "" {
		return "", exit2("Use only one of --%s or --%s-file.", label, label)
	}
	if file != "" {
		if fileReader == nil {
			fileReader = readFileString
		}
		got, err := fileReader(file)
		if err != nil {
			return "", exit2("%s file does not exist: %s", label, file)
		}
		return got, nil
	}
	if inline != "" {
		return inline, nil
	}
	if required {
		return "", exit2("publish-pr requires --%s or --%s-file.", label, label)
	}
	return "", nil
}

func readFileString(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\n"), nil
}

// PreflightInputs reconciles parsed CLI flags into the resolved values
// publish_pr_main uses for the rest of the run. The bash function does
// this just before the git/gh execution begins: it reads the text-arg
// files, applies the lower-assurance non-main base side effects, and
// computes repo_owned_pr_checks_expected. Splitting this out into a
// pure-Go function keeps the validator boundary self-contained and
// gives the upcoming 24.2b execution PR a typed handoff.
type PreflightInputs struct {
	// TitleText is the resolved, non-empty PR title (LoadTextArg of
	// Title/TitleFile).
	TitleText string
	// BodyText is the resolved PR body; may be empty (PR body is
	// optional).
	BodyText string
	// CommitMessageText is the resolved, non-empty commit message
	// (LoadTextArg of CommitMessage/CommitMessageFile).
	CommitMessageText string
	// PublishBaseMode is "main" for the standard run and
	// "lower-assurance" when --allow-non-main-base downgraded the run.
	PublishBaseMode string
	// RepoOwnedPRChecksExpected mirrors publish_pr_repo_owned_checks_expected:
	// "1" when the resolved base is main, "0" otherwise.  Kept as a
	// string because the dry-run scenario greps for the literal
	// `publish_repo_owned_pr_checks_expected=1|0`.
	RepoOwnedPRChecksExpected string
	// Ready propagates Options.Ready so the host execution layer can
	// flip the gh PR off draft without re-parsing the option vector.
	Ready bool
	// LowerAssuranceNotice carries stderr lines to emit when the run
	// has been downgraded (non-main base).  Empty for standard runs.
	LowerAssuranceNotice []string
}

// Preflight applies the bash validators + text-arg loading + base-mode
// downgrade in one pass. It mirrors the section of publish_pr_main
// that runs between the argument-parsing loop and the trap RETURN that
// arms the temp-file cleanup. host-path resolution, gh/git binary
// lookup, and worktree existence checks remain in the (future)
// execution layer because they touch the host filesystem and trusted
// PATH lists.
func Preflight(opts *Options, checkRefFormat CheckRefFormatFunc, fileReader func(string) (string, error)) (*PreflightInputs, error) {
	if opts == nil {
		return nil, exit2("publish-pr preflight requires parsed options.")
	}
	if err := ValidateSnapshotName(opts.Snapshot); err != nil {
		return nil, err
	}
	if err := ValidateBranchName(opts.Branch, checkRefFormat); err != nil {
		return nil, err
	}
	if err := ValidateBaseName(opts.Base, opts.AllowNonMainBase, checkRefFormat); err != nil {
		return nil, err
	}
	titleText, err := LoadTextArg(opts.Title, opts.TitleFile, "title", true, fileReader)
	if err != nil {
		return nil, err
	}
	if titleText == "" {
		return nil, exit2("publish-pr requires a non-empty PR title.")
	}
	bodyText, err := LoadTextArg(opts.Body, opts.BodyFile, "body", false, fileReader)
	if err != nil {
		return nil, err
	}
	commitText, err := LoadTextArg(opts.CommitMessage, opts.CommitMessageFile, "commit-message", true, fileReader)
	if err != nil {
		return nil, err
	}
	if commitText == "" {
		return nil, exit2("publish-pr requires a non-empty commit message.")
	}

	result := &PreflightInputs{
		TitleText:                 titleText,
		BodyText:                  bodyText,
		CommitMessageText:         commitText,
		PublishBaseMode:           "main",
		RepoOwnedPRChecksExpected: RepoOwnedChecksExpected(opts.Base),
		Ready:                     opts.Ready,
	}
	if opts.Base != "main" {
		result.PublishBaseMode = "lower-assurance-non-main"
		result.Ready = false
		result.LowerAssuranceNotice = []string{
			fmt.Sprintf("publish-pr preflight: repo-owned PR checks are not expected for --base %s; use this only for an explicit lower-assurance draft review unit.", opts.Base),
			"publish-pr lower-assurance mode: non-main --base stays draft and the normal main-based PR validation and merge gating do not apply to that PR shape.",
		}
	}
	return result, nil
}

// WriteUsage writes the canonical publish-pr help text to w. It is the
// shared helper for both the success path (-h / --help on stdout) and
// the failure path (unsupported option on stderr) so the future
// helper wrapper does not duplicate the "fmt.Fprint(stdout/stderr,
// UsageText())" pattern.
func WriteUsage(w io.Writer) {
	fmt.Fprint(w, UsageText())
}
