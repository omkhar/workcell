// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRetentionFixture builds a temp repo with one workflow and a policy file.
func writeRetentionFixture(t *testing.T, workflow, policy string) (string, string) {
	t.Helper()
	root := t.TempDir()
	wfDir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatalf("mkdir workflows: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "release.yml"), []byte(workflow), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	policyPath := filepath.Join(root, "retention-policy.json")
	if err := os.WriteFile(policyPath, []byte(policy), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	return root, policyPath
}

const twoUploadWorkflow = `name: Release
jobs:
  preflight:
    steps:
      - uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
        with:
          name: evidence
          retention-days: 90
      - uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
        with:
          name: scratch
          retention-days: 7
`

const matchingPolicy = `{"version":1,"artifacts":{"release.yml":{"evidence":90,"scratch":7}}}`

func TestCheckRetentionPolicyMatches(t *testing.T) {
	root, policyPath := writeRetentionFixture(t, twoUploadWorkflow, matchingPolicy)
	if err := CheckRetentionPolicy(root, policyPath); err != nil {
		t.Fatalf("expected pass, got: %v", err)
	}
}

// TestCheckRetentionPolicyDetectsSwap is the case a workflow-wide value multiset
// cannot catch: the two retention values are swapped between artifacts, so the
// set {7,90} is unchanged but evidence retention dropped to 7.
func TestCheckRetentionPolicyDetectsSwap(t *testing.T) {
	swapped := strings.Replace(twoUploadWorkflow,
		"name: evidence\n          retention-days: 90",
		"name: evidence\n          retention-days: 7", 1)
	swapped = strings.Replace(swapped,
		"name: scratch\n          retention-days: 7",
		"name: scratch\n          retention-days: 90", 1)
	root, policyPath := writeRetentionFixture(t, swapped, matchingPolicy)
	err := CheckRetentionPolicy(root, policyPath)
	if err == nil {
		t.Fatal("expected drift error for swapped retention values, got nil")
	}
	if !strings.Contains(err.Error(), `artifact "evidence" retention drift: documented 90, workflow has 7`) {
		t.Fatalf("expected evidence drift, got: %v", err)
	}
}

func TestCheckRetentionPolicyRequiresExplicitRetention(t *testing.T) {
	noRetention := `name: Release
jobs:
  preflight:
    steps:
      - uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
        with:
          name: evidence
`
	root, policyPath := writeRetentionFixture(t, noRetention, `{"version":1,"artifacts":{"release.yml":{"evidence":90}}}`)
	err := CheckRetentionPolicy(root, policyPath)
	if err == nil || !strings.Contains(err.Error(), "no explicit retention-days") {
		t.Fatalf("expected explicit-retention error, got: %v", err)
	}
}

func TestCheckRetentionPolicyRejectsUndocumentedArtifact(t *testing.T) {
	root, policyPath := writeRetentionFixture(t, twoUploadWorkflow, `{"version":1,"artifacts":{"release.yml":{"evidence":90}}}`)
	err := CheckRetentionPolicy(root, policyPath)
	if err == nil || !strings.Contains(err.Error(), `artifact "scratch" is not documented`) {
		t.Fatalf("expected undocumented-artifact error, got: %v", err)
	}
}

func TestCheckRetentionPolicyRejectsStalePolicyEntry(t *testing.T) {
	root, policyPath := writeRetentionFixture(t, twoUploadWorkflow, `{"version":1,"artifacts":{"release.yml":{"evidence":90,"scratch":7,"ghost":30}}}`)
	err := CheckRetentionPolicy(root, policyPath)
	if err == nil || !strings.Contains(err.Error(), `documents artifact "ghost"`) {
		t.Fatalf("expected stale-policy-entry error, got: %v", err)
	}
}

func TestCheckRetentionPolicyRejectsUndocumentedWorkflow(t *testing.T) {
	root, policyPath := writeRetentionFixture(t, twoUploadWorkflow, `{"version":1,"artifacts":{"ci.yml":{"x":7}}}`)
	err := CheckRetentionPolicy(root, policyPath)
	if err == nil || !strings.Contains(err.Error(), "release.yml uploads artifacts but is not in") {
		t.Fatalf("expected undocumented-workflow error, got: %v", err)
	}
}

func TestCheckRetentionPolicyRejectsBadVersion(t *testing.T) {
	root, policyPath := writeRetentionFixture(t, twoUploadWorkflow, `{"version":2,"artifacts":{"release.yml":{"evidence":90,"scratch":7}}}`)
	err := CheckRetentionPolicy(root, policyPath)
	if err == nil || !strings.Contains(err.Error(), "must use version 1") {
		t.Fatalf("expected version error, got: %v", err)
	}
}
