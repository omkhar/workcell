// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

const claudeMentionContract = "claude-mention.yml must match the reviewed owner-gated contract"

func readClaudeMentionWorkflow(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "claude-mention.yml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestClaudeMentionWorkflowMatchesContract(t *testing.T) {
	if err := metadatautil.ValidateClaudeMentionWorkflow(readClaudeMentionWorkflow(t)); err != nil {
		t.Fatalf("repository workflow rejected: %v", err)
	}
}

// A pin bump, by the owner or by Dependabot, changes only the commit and its
// comment, so it keeps the contract.
func TestClaudeMentionWorkflowAcceptsPinBump(t *testing.T) {
	workflow := readClaudeMentionWorkflow(t)
	const placeholder = "anthropics/claude-code-action@0000000000000000000000000000000000000000 # v1"
	if !strings.Contains(workflow, placeholder) {
		t.Skip("the placeholder pin is already replaced")
	}
	bumped := strings.Replace(workflow, placeholder, "anthropics/claude-code-action@0123456789abcdef0123456789abcdef01234567 # v1.2.3", 1)
	if err := metadatautil.ValidateClaudeMentionWorkflow(bumped); err != nil {
		t.Fatalf("pin bump rejected: %v", err)
	}
}

func TestClaudeMentionWorkflowRejectsChanges(t *testing.T) {
	workflow := readClaudeMentionWorkflow(t)
	for name, change := range map[string][2]string{
		"run actor gate removed":     {"github.actor_id == '22666538' &&\n", ""},
		"comment author id changed":  {"github.event.comment.user.id == 22666538", "github.event.comment.user.id == 1"},
		"review owner check removed": {"github.event.review.user.id == 22666538 &&\n          github.event.pull_request.user.id == 22666538 &&\n", "github.event.review.user.id == 22666538 &&\n"},
		"mention check removed":      {"contains(github.event.issue.body, '@claude')", "true"},
		"dispatch trigger added":     {"on:\n", "on:\n  workflow_dispatch:\n"},
		"edited comments trigger":    {"      - created\n  pull_request_review_comment:", "      - created\n      - edited\n  pull_request_review_comment:"},
		"contents write":             {"contents: read #", "contents: write #"},
		"environment renamed":        {"name: claude-mention", "name: other"},
		"rerun check skipped":        {"      - name: Require the owner to start or rerun the run\n", "      - name: Require the owner to start or rerun the run\n        if: false\n"},
		"fork refusal ignored":       {"      - name: Refuse pull requests from forks\n", "      - name: Refuse pull requests from forks\n        continue-on-error: true\n"},
		"fork refusal weakened":      {`if [[ "${head_repo_id}" != "${REPOSITORY_ID}" ]]; then`, `if false; then`},
		"Bash allowed":               {`"Bash,Edit,`, `"Edit,`},
		"checkout keeps credentials": {"persist-credentials: false", "persist-credentials: true"},
		"action pinned by tag":       {"anthropics/claude-code-action@0000000000000000000000000000000000000000", "anthropics/claude-code-action@v1"},
		"action from a fork":         {"anthropics/claude-code-action@0000000000000000000000000000000000000000", "mallory/claude-code-action@0000000000000000000000000000000000000000"},
		"job env added":              {"    timeout-minutes: 30\n", "    timeout-minutes: 30\n    env:\n      NODE_OPTIONS: --require /tmp/x.js\n"},
		"secret moved to an alias":   {"claude_code_oauth_token: ${{ secrets.CLAUDE_CODE_OAUTH_TOKEN }}", "claude_code_oauth_token: &token ${{ secrets.CLAUDE_CODE_OAUTH_TOKEN }}"},
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(workflow, change[0]) {
				t.Fatalf("fixture text %q is absent from the workflow", change[0])
			}
			mutated := strings.Replace(workflow, change[0], change[1], 1)
			err := metadatautil.ValidateClaudeMentionWorkflow(mutated)
			if err == nil || !strings.Contains(err.Error(), claudeMentionContract) {
				t.Fatalf("ValidateClaudeMentionWorkflow() error = %v, want %q", err, claudeMentionContract)
			}
		})
	}
}

func TestClaudeMentionWorkflowRejectsSecondAction(t *testing.T) {
	workflow := readClaudeMentionWorkflow(t)
	second := workflow + "      - uses: anthropics/claude-code-action@0000000000000000000000000000000000000000\n"
	err := metadatautil.ValidateClaudeMentionWorkflow(second)
	if err == nil || !strings.Contains(err.Error(), "found 2") {
		t.Fatalf("ValidateClaudeMentionWorkflow() error = %v, want a second-action rejection", err)
	}
}

// TestClaudeMentionWorkflowRejectsEvasions runs the shared evasion corpus
// against the rerun owner check.
func TestClaudeMentionWorkflowRejectsEvasions(t *testing.T) {
	anchor := `triggering_id="$(gh api "users/${TRIGGERING_ACTOR}" --jq '.id')"`
	RequireRejectsAllEvasions(t, readClaudeMentionWorkflow(t), anchor, claudeMentionContract,
		metadatautil.ValidateClaudeMentionWorkflow)
}

func TestClaudeMentionReferencesStayInTheirWorkflow(t *testing.T) {
	for name, text := range map[string]string{
		"action":             "      - uses: anthropics/claude-code-action@0000000000000000000000000000000000000000\n",
		"secret":             "        env:\n          TOKEN: ${{ secrets.CLAUDE_CODE_OAUTH_TOKEN }}\n",
		"environment":        "    environment:\n      name: claude-mention\n",
		"comment that names": "# claude-code-action\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := metadatautil.ValidateClaudeMentionPlacement(text, ".github/workflows/ci.yml"); err == nil {
				t.Fatal("another workflow may name the claude-mention action, secret or environment")
			}
		})
	}
	if err := metadatautil.ValidateClaudeMentionPlacement("name: CI\n", ".github/workflows/ci.yml"); err != nil {
		t.Fatalf("unrelated workflow rejected: %v", err)
	}
	if err := metadatautil.ValidateClaudeMentionPlacement("name: x\n", ".github/workflows/claude-mention.yml"); err == nil {
		t.Fatal("claude-mention.yml must match its contract")
	}
}
