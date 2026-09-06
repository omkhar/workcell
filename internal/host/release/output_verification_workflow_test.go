// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin || linux

package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestReleaseWorkflowVerifiesOutputsBeforePublication(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	data, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)

	var document struct {
		Jobs map[string]struct {
			Needs       yaml.Node         `yaml:"needs"`
			Permissions map[string]string `yaml:"permissions"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse release workflow: %v", err)
	}

	verify, ok := document.Jobs["verify-release-outputs"]
	if !ok {
		t.Fatal("release workflow is missing verify-release-outputs")
	}
	if !workflowNeedsJob(verify.Needs, "tag-policy") || !workflowNeedsJob(verify.Needs, "release") {
		t.Fatal("verify-release-outputs must depend on tag-policy and release")
	}
	if len(verify.Permissions) != 4 ||
		verify.Permissions["actions"] != "read" ||
		verify.Permissions["attestations"] != "read" ||
		verify.Permissions["contents"] != "read" ||
		verify.Permissions["packages"] != "read" {
		t.Fatalf("verify-release-outputs permissions = %#v, want four read-only permissions", verify.Permissions)
	}

	publish, ok := document.Jobs["publish-github-release"]
	if !ok || !workflowNeedsJob(publish.Needs, "verify-release-outputs") {
		t.Fatal("publisher must depend on verify-release-outputs")
	}
	if len(publish.Permissions) != 4 ||
		publish.Permissions["actions"] != "read" ||
		publish.Permissions["attestations"] != "read" ||
		publish.Permissions["contents"] != "write" ||
		publish.Permissions["packages"] != "read" {
		t.Fatalf("publish-github-release permissions = %#v, want read verification permissions plus contents write", publish.Permissions)
	}
	if strings.Count(workflow, "./scripts/verify-release-outputs.sh") != 2 {
		t.Fatalf("release workflow verifier invocation count = %d, want independent and pre-publication checks", strings.Count(workflow, "./scripts/verify-release-outputs.sh"))
	}
	if strings.Count(workflow, `--source-digest "${RELEASE_COMMIT}"`) != 2 {
		t.Fatal("both release-output checks must bind proofs to the tag commit")
	}
	if strings.Count(workflow, `--workflow-digest "${GITHUB_WORKFLOW_SHA}"`) != 2 {
		t.Fatal("both release-output checks must bind proofs to the trusted workflow commit")
	}
	if strings.Count(workflow, "sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6") != 4 {
		t.Fatalf("Cosign installer count = %d, want signing, independent verification, and pre-publication verification coverage", strings.Count(workflow, "sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6"))
	}
	if strings.Contains(workflow, "verify-release-outputs:\n") && strings.Contains(workflow, "id-token: write") {
		verifyBlock := workflow[strings.Index(workflow, "  verify-release-outputs:"):strings.Index(workflow, "  publish-github-release:")]
		if strings.Contains(verifyBlock, "id-token: write") || strings.Contains(verifyBlock, "contents: write") || strings.Contains(verifyBlock, "packages: write") || strings.Contains(verifyBlock, "attestations: write") {
			t.Fatal("verify-release-outputs must not receive publication or signing authority")
		}
	}
}
