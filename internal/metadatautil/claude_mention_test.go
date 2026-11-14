// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
	"gopkg.in/yaml.v3"
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
		"PR author check removed":    {"(!github.event.issue.pull_request || github.event.issue.user.id == 22666538) &&\n", ""},
		"mention check removed":      {"contains(github.event.issue.body, '@claude')", "true"},
		"dispatch trigger added":     {"on:\n", "on:\n  workflow_dispatch:\n"},
		"edited comments trigger":    {"      - created\n  issues:", "      - created\n      - edited\n  issues:"},
		"contents write":             {"contents: read #", "contents: write #"},
		"environment renamed":        {"name: claude-mention", "name: other"},
		"rerun check skipped":        {"      - name: Require the owner to start or rerun the run\n", "      - name: Require the owner to start or rerun the run\n        if: false\n"},
		"PR refusal ignored":         {"      - name: Refuse pull requests the owner did not open here\n", "      - name: Refuse pull requests the owner did not open here\n        continue-on-error: true\n"},
		"PR refusal weakened":        {`if [[ "${pr}" != "${REPOSITORY_ID} ${OWNER_ID}" ]]; then`, `if false; then`},
		"review trigger added":       {"  issues:\n", "  pull_request_review:\n    types:\n      - submitted\n  issues:\n"},
		"tmp wholly denied":          {"Read(//tmp/claude*/**)", "Read(//tmp/**)"},
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

// The contract is the exact parsed workflow, so it needs no evasion corpus
// run: any rewrite of a run body changes the digest. The change table above
// covers the gate, the triggers and the tool list.

// TestClaudeMentionWorkflowShape pins the decisions the digest encodes, so a
// digest refresh cannot quietly drop them.
func TestClaudeMentionWorkflowShape(t *testing.T) {
	var document struct {
		On   map[string]yaml.Node `yaml:"on"`
		Jobs map[string]struct {
			If          string `yaml:"if"`
			Environment struct {
				Name string `yaml:"name"`
			} `yaml:"environment"`
			Steps []struct {
				Uses string            `yaml:"uses"`
				With map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readClaudeMentionWorkflow(t)), &document); err != nil {
		t.Fatal(err)
	}
	// Both events run this file from the default branch, so the environment
	// can allow main alone. A PR review event runs the PR merge ref.
	if len(document.On) != 2 || document.On["issues"].Kind == 0 || document.On["issue_comment"].Kind == 0 {
		t.Fatalf("triggers = %v, want issues and issue_comment only", document.On)
	}
	job := document.Jobs["claude"]
	if job.Environment.Name != "claude-mention" {
		t.Fatalf("environment = %q", job.Environment.Name)
	}
	gate := strings.Join(strings.Fields(job.If), " ")
	for _, clause := range []string{
		"github.actor_id == '22666538'",
		"github.event.sender.id == 22666538",
		"github.event.comment.user.id == 22666538",
		"(!github.event.issue.pull_request || github.event.issue.user.id == 22666538)",
		"github.event.issue.user.id == 22666538 && contains(github.event.issue.body, '@claude')",
	} {
		if !strings.Contains(gate, clause) {
			t.Errorf("job gate lacks %q", clause)
		}
	}
	var args string
	for _, step := range job.Steps {
		if strings.HasPrefix(step.Uses, "anthropics/claude-code-action@") {
			args = step.With["claude_args"]
		}
	}
	for _, deny := range []string{"Bash", "Edit", "Write", "WebFetch", "WebSearch", "Read(//proc/**)", "Read(//home/runner/work/_temp/**)", "Read(./.git/**)"} {
		if !strings.Contains(args, deny) {
			t.Errorf("claude_args lacks the %s deny", deny)
		}
	}
	// The action saves comment images under /tmp/github-images for Claude to read.
	if strings.Contains(args, "Read(//tmp/**)") {
		t.Error("claude_args denies all of /tmp, which hides comment images")
	}
}

func TestClaudeMentionReferencesStayInTheirWorkflow(t *testing.T) {
	job := func(body string) string {
		return "name: CI\non:\n  push:\npermissions: {}\njobs:\n  x:\n    runs-on: ubuntu-latest\n" + body
	}
	for name, text := range map[string]string{
		"action":                    job("    steps:\n      - uses: anthropics/claude-code-action@0000000000000000000000000000000000000000\n"),
		"secret":                    job("    steps:\n      - env:\n          TOKEN: ${{ secrets.CLAUDE_CODE_OAUTH_TOKEN }}\n        run: true\n"),
		"secret in lower case":      job("    steps:\n      - env:\n          TOKEN: ${{ secrets.claude_code_oauth_token }}\n        run: true\n"),
		"secret by literal index":   job("    steps:\n      - env:\n          TOKEN: ${{ secrets['Claude_Code_OAuth_Token'] }}\n        run: true\n"),
		"secret by computed index":  job("    steps:\n      - env:\n          TOKEN: ${{ secrets[format('{0}_TOKEN', 'CLAUDE_CODE_OAUTH')] }}\n        run: true\n"),
		"secret index by variable":  job("    steps:\n      - env:\n          TOKEN: ${{ secrets[vars.NAME] }}\n        run: true\n"),
		"all secrets as JSON":       job("    steps:\n      - env:\n          ALL: ${{ toJSON(secrets) }}\n        run: true\n"),
		"all secrets spaced":        job("    steps:\n      - env:\n          ALL: ${{ ToJson( secrets ) }}\n        run: true\n"),
		"all secrets in a step if":  job("    steps:\n      - if: contains(toJSON(secrets), 'x')\n        run: true\n"),
		"environment":               job("    environment:\n      name: claude-mention\n"),
		"environment scalar":        job("    environment: claude-mention\n"),
		"environment mixed case":    job("    environment:\n      name: Claude-Mention\n"),
		"environment as expression": job("    environment: ${{ format('claude-{0}', 'mention') }}\n"),
		"environment name expr":     job("    environment:\n      name: ${{ vars.ENVIRONMENT }}\n"),
		"comment that names":        "# claude-code-action\n" + job("    steps:\n      - run: true\n"),
		"escaped environment name":  job("    environment:\n      name: \"claude\\x2dmention\"\n"),
		"escaped action reference":  job("    steps:\n      - uses: \"anthropics/claude\\x2dcode-action@0000000000000000000000000000000000000000\"\n"),
		"environment through alias": "x: &e ${{ vars.ENVIRONMENT }}\n" + job("    environment: *e\n"),
		"unparsable workflow":       "jobs: [\n",
		"recursive alias":           "a: &x [*x]\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := metadatautil.ValidateClaudeMentionPlacement(text, ".github/workflows/ci.yml"); err == nil {
				t.Fatal("another workflow may reach the claude-mention action, secret or environment")
			}
		})
	}
	for name, text := range map[string]string{
		"unrelated workflow":    job("    steps:\n      - run: true\n"),
		"other secret":          job("    environment:\n      name: release\n    steps:\n      - env:\n          T: ${{ secrets.WORKCELL_HOSTED_CONTROLS_TOKEN }}\n        if: steps.secrets.outputs.present == 'true'\n        run: true\n"),
		"other secret by index": job("    steps:\n      - env:\n          T: ${{ secrets['OTHER_TOKEN'] }}\n        run: true\n"),
		"step named secrets":    job("    steps:\n      - id: secrets\n        run: echo secrets\n"),
	} {
		t.Run(name, func(t *testing.T) {
			if err := metadatautil.ValidateClaudeMentionPlacement(text, ".github/workflows/ci.yml"); err != nil {
				t.Fatalf("workflow rejected: %v", err)
			}
		})
	}
	if err := metadatautil.ValidateClaudeMentionPlacement("name: x\n", ".github/workflows/claude-mention.yml"); err == nil {
		t.Fatal("claude-mention.yml must match its contract")
	}
}
