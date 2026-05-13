// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package publishpr

// usageText is the canonical help text for `workcell publish-pr`,
// migrated verbatim from scripts/workcell's publish_pr_usage()
// function.
const usageText = "Usage: workcell publish-pr [options]\n" +
	"\n" +
	"Options:\n" +
	"  --workspace PATH              Workspace to publish (default: current directory)\n" +
	"  --branch NAME                 Feature branch to create or switch to (required)\n" +
	"  --base NAME                   Base branch for the PR (default: main)\n" +
	"  --allow-non-main-base         Allow a lower-assurance draft PR against a non-main base\n" +
	"  --gh-bin PATH                 Host GitHub CLI to use for PR creation\n" +
	"  --snapshot index|worktree     Publish the current index or stage the worktree (default: worktree)\n" +
	"  --title TEXT                  PR title\n" +
	"  --title-file PATH             Read the PR title from PATH\n" +
	"  --body TEXT                   PR body (default: empty)\n" +
	"  --body-file PATH              Read the PR body from PATH\n" +
	"  --commit-message TEXT         Commit message to sign and publish\n" +
	"  --commit-message-file PATH    Read the commit message from PATH\n" +
	"  --ready                       Create a ready PR instead of a draft PR\n" +
	"  --dry-run                     Print the planned host commands and exit\n" +
	"  -h, --help                    Show this help text\n" +
	"\n" +
	"Notes:\n" +
	"  - publish-pr runs on the host, not inside the Workcell container.\n" +
	"  - It uses the host Git and GitHub CLI configuration so maintainer signing\n" +
	"    and publication stay outside the Tier 1 container boundary.\n" +
	"  - It blocks over-broad branch diffs before push so published PRs stay\n" +
	"    reviewable by a human without juggling unrelated concerns.\n" +
	"  - By default publish-pr only supports --base main because repo-owned PR\n" +
	"    workflows and hosted controls only cover main-based review units.\n" +
	"  - --allow-non-main-base is an explicit lower-assurance escape hatch: the PR\n" +
	"    stays draft-only and the normal main-based PR validation and merge gating\n" +
	"    do not apply to that PR shape.\n" +
	"  - publish-pr preflights whether repo-owned PR checks are expected for the\n" +
	"    chosen base and surfaces the effective validation mode in dry-run output.\n" +
	"  - Host-side git commands explicitly bypass repo hooks during publication\n" +
	"    because workspace hook content is untrusted; new commits are signed and\n" +
	"    every commit being published must verify against host signing trust.\n" +
	"  - The default snapshot is worktree, which stages the current tracked and\n" +
	"    untracked workspace changes with `git add -A` before committing.\n"

// UsageText returns the canonical `workcell publish-pr` help string.
func UsageText() string {
	return usageText
}
