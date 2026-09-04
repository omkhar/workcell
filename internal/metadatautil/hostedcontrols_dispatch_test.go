// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

func TestHostedControlDispatchPreservesInputReadOrder(t *testing.T) {
	t.Parallel()
	root, policyPath := writeHostedControlsFixture(t, "review-gated", "review-gated", singleOwnerCollaborator())
	first := filepath.Join(root, "actions-permissions.json")
	second := filepath.Join(root, "actions-selected-actions.json")
	for _, path := range []string{first, second} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}

	err := metadatautil.VerifyGitHubHostedControls(root, "omkhar/workcell", policyPath)
	var pathError *os.PathError
	if !errors.As(err, &pathError) || pathError.Path != first {
		t.Fatalf("VerifyGitHubHostedControls() error = %v, want first read failure for %s", err, first)
	}
}

func TestHostedControlDispatchPreservesActionsBeforeRulesets(t *testing.T) {
	t.Parallel()
	root, policyPath := writeHostedControlsFixture(t, "review-gated", "review-gated", singleOwnerCollaborator())
	rewriteFile(t, filepath.Join(root, "actions-permissions.json"), func(content string) string {
		return strings.Replace(content, "\"allowed_actions\": \"selected\"", "\"allowed_actions\": \"all\"", 1)
	})
	rewriteFile(t, filepath.Join(root, "rulesets.json"), func(content string) string {
		return strings.Replace(content, "\"type\": \"required_signatures\"", "\"type\": \"attacker-rule\"", 1)
	})

	err := metadatautil.VerifyGitHubHostedControls(root, "omkhar/workcell", policyPath)
	want := "GitHub Actions on omkhar/workcell must restrict allowed_actions to selected"
	if err == nil || err.Error() != want {
		t.Fatalf("VerifyGitHubHostedControls() error = %v, want %q", err, want)
	}
}

func TestHostedControlDispatchPreservesRulesetsBeforeVariables(t *testing.T) {
	t.Parallel()
	root, policyPath := writeHostedControlsFixture(t, "review-gated", "review-gated", singleOwnerCollaborator())
	rewriteFile(t, filepath.Join(root, "rulesets.json"), func(content string) string {
		return strings.Replace(content, "\"require_code_owner_review\": true", "\"require_code_owner_review\": false", 1)
	})
	rewriteFile(t, filepath.Join(root, "actions-variables.json"), func(content string) string {
		return strings.Replace(content, "\"value\": \"false\"", "\"value\": \"wrong\"", 1)
	})

	err := metadatautil.VerifyGitHubHostedControls(root, "omkhar/workcell", policyPath)
	want := "default-branch review ruleset on omkhar/workcell must require code owner review"
	if err == nil || err.Error() != want {
		t.Fatalf("VerifyGitHubHostedControls() error = %v, want %q", err, want)
	}
}

func singleOwnerCollaborator() []map[string]any {
	return []map[string]any{{
		"login": "omkhar",
		"permissions": map[string]any{
			"admin": true,
		},
	}}
}
