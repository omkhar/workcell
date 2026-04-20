// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeWorkflowLanesFixture(t *testing.T, root string) string {
	t.Helper()

	workflowDir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workflowDir) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workflowDir, "ci.yml"), []byte(`name: CI

on:
  pull_request:
    branches:
      - main
  push:
    branches:
      - main

jobs:
  pr-shape:
    name: Pull request shape
    runs-on: ubuntu-latest
    steps:
      - run: true

  heavy:
    name: Reproducible build (${{ matrix.platform_name }})
    if: ${{ github.event_name != 'pull_request' || contains(github.event.pull_request.labels.*.name, 'approved-heavy-ci') }}
    strategy:
      matrix:
        include:
          - platform: linux/amd64
            platform_name: amd64
    runs-on: ubuntu-latest
    steps:
      - run: true
`), 0o644); err != nil {
		t.Fatalf("WriteFile(ci.yml) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workflowDir, "docs.yml"), []byte(`name: Docs

on:
  pull_request:
    branches:
      - main
    paths:
      - "README.md"
      - "docs/**"

jobs:
  spelling:
    name: Spelling and manpage
    runs-on: ubuntu-latest
    steps:
      - run: true
`), 0o644); err != nil {
		t.Fatalf("WriteFile(docs.yml) error = %v", err)
	}

	policyPath := filepath.Join(root, "policy", "workflow-lane-policy.json")
	if err := os.MkdirAll(filepath.Dir(policyPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(policy) error = %v", err)
	}
	if err := os.WriteFile(policyPath, []byte(`{
  "version": 1,
  "lanes": {
    "ci.yml/heavy[platform=linux/amd64,platform_name=amd64]": {
      "profiles": ["pr-parity", "repo-core"],
      "authority": "repo-core",
      "local_mode": "partial",
      "local_script": "scripts/verify-reproducible-build.sh",
      "local_order": 30
    },
    "ci.yml/pr-shape": {
      "profiles": ["pr-parity"],
      "authority": "pr-parity",
      "local_mode": "mirrored",
      "local_script": "scripts/ci/job-pr-shape.sh",
      "local_order": 10
    },
    "docs.yml/spelling": {
      "profiles": ["pr-parity"],
      "authority": "pr-parity",
      "local_mode": "mirrored",
      "local_script": "scripts/ci/job-docs.sh",
      "local_order": 20
    }
  }
}`), 0o644); err != nil {
		t.Fatalf("WriteFile(policy) error = %v", err)
	}
	return policyPath
}

func TestGenerateWorkflowLaneManifestExpandsMatrixAndPathFilters(t *testing.T) {
	root := t.TempDir()
	policyPath := writeWorkflowLanesFixture(t, root)
	manifestPath := filepath.Join(root, "policy", "workflow-lanes.json")

	if err := GenerateWorkflowLaneManifest(root, policyPath, manifestPath); err != nil {
		t.Fatalf("GenerateWorkflowLaneManifest() error = %v", err)
	}

	var manifest WorkflowLaneManifest
	if err := readJSONFile(manifestPath, &manifest); err != nil {
		t.Fatalf("readJSONFile(manifest) error = %v", err)
	}
	if len(manifest.Lanes) != 3 {
		t.Fatalf("lane count = %d, want 3", len(manifest.Lanes))
	}

	var docsLane WorkflowLaneManifestEntry
	var heavyLane WorkflowLaneManifestEntry
	for _, lane := range manifest.Lanes {
		switch lane.ID {
		case "docs.yml/spelling":
			docsLane = lane
		case "ci.yml/heavy[platform=linux/amd64,platform_name=amd64]":
			heavyLane = lane
		}
	}
	if !strings.Contains(docsLane.JobName, "Spelling and manpage") {
		t.Fatalf("docs lane job name = %q", docsLane.JobName)
	}
	if got := docsLane.WorkflowPathGlobs["pull_request"]; len(got) != 2 || got[0] != "README.md" || got[1] != "docs/**" {
		t.Fatalf("docs pull_request path globs = %#v", got)
	}
	if got := heavyLane.RequiredLabels; len(got) != 1 || got[0] != "approved-heavy-ci" {
		t.Fatalf("heavy required labels = %#v", got)
	}
	if got := heavyLane.JobName; got != "Reproducible build (amd64)" {
		t.Fatalf("heavy lane job name = %q", got)
	}
}

func TestPlanWorkflowLanesRespectsPathsLabelsAndGitHubOnlyModes(t *testing.T) {
	root := t.TempDir()
	policyPath := writeWorkflowLanesFixture(t, root)
	manifestPath := filepath.Join(root, "policy", "workflow-lanes.json")

	if err := GenerateWorkflowLaneManifest(root, policyPath, manifestPath); err != nil {
		t.Fatalf("GenerateWorkflowLaneManifest() error = %v", err)
	}

	plan, err := PlanWorkflowLanes(manifestPath, WorkflowLanePlannerConfig{
		Profile:      "pr-parity",
		Event:        "pull_request",
		BaseBranch:   "main",
		ChangedFiles: []string{"internal/foo.go"},
	})
	if err != nil {
		t.Fatalf("PlanWorkflowLanes() error = %v", err)
	}

	statusByID := map[string]string{}
	for _, lane := range plan.Lanes {
		statusByID[lane.ID] = lane.Status + ":" + lane.Reason
	}
	if got := statusByID["ci.yml/pr-shape"]; !strings.HasPrefix(got, "local:") {
		t.Fatalf("pr-shape status = %q, want local", got)
	}
	if got := statusByID["docs.yml/spelling"]; got != "skipped:path-filter-not-selected" {
		t.Fatalf("docs lane status = %q, want skipped:path-filter-not-selected", got)
	}
	if got := statusByID["ci.yml/heavy[platform=linux/amd64,platform_name=amd64]"]; got != "skipped:missing-label:approved-heavy-ci" {
		t.Fatalf("heavy lane status = %q, want missing approved-heavy-ci label", got)
	}
}
