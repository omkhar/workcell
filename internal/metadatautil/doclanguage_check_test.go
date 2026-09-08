// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sevenSentenceParagraph reproduces the paragraph that the reviewer split in
// PR #584 ("Split the seven-sentence audit paragraph").
const sevenSentenceParagraph = "`workcell session timeline` selects audit records for one session. " +
	"`workcell session export` creates a session bundle. " +
	"`workcell --gc` preserves durable session records. " +
	"`workcell session delete` removes a stopped record. " +
	"A full delete also removes the recorded stopped container, debug log, file-trace log, " +
	"transcript log, session audit directory, and audit seal when they exist. " +
	"It does not remove the recorded isolated clone. " +
	"It does not rewrite the shared profile audit log."

// Each fixture carries prose that the reviewer flagged in PRs #569, #577,
// #584, #595 and #598, before any counter existed. A rule that stops reporting
// its fixture has lost the defect it was built for.
func TestScanDocLanguageFlagsReviewedProse(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		rule string
	}{
		{
			name: "pr584 twenty-one word instruction",
			body: "Update this evidence map in the same change as a support, release, audit, or\n" +
				"runtime-boundary claim that changes the evidence map.\n",
			rule: ruleSentenceWords,
		},
		{
			name: "pr595 twenty-three word certification instruction",
			body: "The certification record must identify the exact matrix row.\n" +
				"It must record the Workcell commit SHA, worktree dirty state, host kernel,\n" +
				"cgroup mode, Docker security features, command, UTC timestamp, and cleanup status.\n",
			rule: ruleSentenceWords,
		},
		{
			name: "pr598 prohibited -ing verb form",
			body: "Extend stable behavior for demonstrated variation without breaking existing\ncontracts.\n",
			rule: ruleIngVerb,
		},
		{
			name: "pr598 four word noun cluster",
			body: "Add extended ACL rejection to the release asset staging path.\n",
			rule: ruleNounCluster,
		},
		{
			name: "pr584 seven sentence paragraph",
			body: sevenSentenceParagraph + "\n",
			rule: ruleParagraphSentences,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			findings := scanFixture(t, testCase.body)
			if !hasRule(findings, testCase.rule) {
				t.Fatalf("expected rule %s for %q, found %v", testCase.rule, testCase.body, findings)
			}
		})
	}
}

// The exclusion set is the anchoring half of this check: prose rules that read
// a fenced example, an inline code span, a table row or a heading report a
// defect the document does not contain.
func TestScanDocLanguageIgnoresNonProse(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
	}{
		{
			name: "fenced block",
			body: "Read the example.\n\n```\n" + sevenSentenceParagraph + "\n```\n",
		},
		{
			name: "tilde fenced block",
			body: "Read the example.\n\n~~~\n" + sevenSentenceParagraph + "\n~~~\n",
		},
		{
			name: "table row",
			body: "| Term | Meaning |\n|---|---|\n| release asset staging path | The reviewed path. |\n",
		},
		{
			name: "inline code span",
			body: "Run `the release asset staging path` when the check fails.\n",
		},
		{
			name: "heading",
			body: "# Extend stable behavior for demonstrated variation without breaking contracts\n",
		},
		{
			name: "list items are separate blocks",
			body: "- One sentence.\n- Two sentences.\n- Three sentences.\n- Four sentences.\n" +
				"- Five sentences.\n- Six sentences.\n- Seven sentences.\n",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if findings := scanFixture(t, testCase.body); len(findings) != 0 {
				t.Fatalf("expected no finding, found %v", findings)
			}
		})
	}
}

// A version number, a file extension and a lowercase successor keep one
// sentence whole, so the paragraph rule counts sentences and not full stops.
func TestSplitSentencesKeepsDottedTokensWhole(t *testing.T) {
	t.Parallel()
	text := "The tag is v1.2.3 for this release. Read docs/documentation-language.md next."
	if got := splitSentences(text); len(got) != 2 {
		t.Fatalf("expected 2 sentences, found %d: %q", len(got), got)
	}
}

func TestCheckDocLanguageRatchet(t *testing.T) {
	t.Parallel()
	// A count at the baseline passes, a count above it fails, and a baseline
	// row whose document no longer violates the rule fails as stale.
	body := "Add extended ACL rejection to the release asset staging path.\n"
	cases := []struct {
		name     string
		body     string
		baseline string
		wantErr  string
	}{
		{name: "at baseline", body: body, baseline: "docs/page.md\tnoun-cluster\t1\n"},
		{
			name:     "growth fails",
			body:     body + "\nRemove the release asset staging path.\n",
			baseline: "docs/page.md\tnoun-cluster\t1\n",
			wantErr:  "baseline allows 1",
		},
		{
			name:     "unlisted violation fails",
			body:     body,
			baseline: "",
			wantErr:  "baseline allows 0",
		},
		{
			name:     "stale row fails",
			body:     "Read the reviewed page.\n",
			baseline: "docs/page.md\tnoun-cluster\t1\n",
			wantErr:  "stale baseline row",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeDocFixture(t, filepath.Join(root, "docs", "page.md"), testCase.body)
			writeDocFixture(t, filepath.Join(root, docLanguageBaselinePath), testCase.baseline)
			err := CheckDocLanguage(root)
			if testCase.wantErr == "" {
				if err != nil {
					t.Fatalf("expected a clean result, found %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("expected an error containing %q, found %v", testCase.wantErr, err)
			}
		})
	}
}

// An empty inventory must not pass: a check that reads no document reports a
// clean result over prose it never opened.
func TestCheckDocLanguageRejectsEmptyInventory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeDocFixture(t, filepath.Join(root, "docs", ".keep"), "")
	writeDocFixture(t, filepath.Join(root, docLanguageBaselinePath), "")
	if err := CheckDocLanguage(root); err == nil {
		t.Fatal("expected an empty inventory to fail")
	}
}

// The repository documents must satisfy their own ratchet.
func TestCheckDocLanguageRepositoryDocuments(t *testing.T) {
	t.Parallel()
	if err := CheckDocLanguage(repositoryRootForDocLanguage(t)); err != nil {
		t.Fatalf("repository documents failed the language ratchet: %v", err)
	}
}

func repositoryRootForDocLanguage(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return root
}

func scanFixture(t *testing.T, body string) []docLanguageFinding {
	t.Helper()
	path := filepath.Join(t.TempDir(), "page.md")
	writeDocFixture(t, path, body)
	findings, err := scanDocLanguage(path)
	if err != nil {
		t.Fatalf("scan fixture: %v", err)
	}
	return findings
}

func writeDocFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func hasRule(findings []docLanguageFinding, rule string) bool {
	for _, finding := range findings {
		if finding.rule == rule {
			return true
		}
	}
	return false
}
