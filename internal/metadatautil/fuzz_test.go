// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"os"
	"path/filepath"
	"testing"
)

// addRepoFileSeed seeds a fuzz corpus with the real contents of a tracked repo
// config so the checked-in corpus is grounded in the shapes these parsers
// actually gate. Package tests run with the package directory as the working
// directory, so the repository root is two levels up. A missing seed file is a
// wiring error and fails loudly rather than silently degrading the corpus.
func addRepoFileSeed(f *testing.F, relPath string) {
	f.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", relPath))
	if err != nil {
		f.Fatalf("read fuzz seed %s: %v", relPath, err)
	}
	f.Add(string(data))
}

// FuzzExtractWorkflowUses exercises the workflow-YAML `uses:` extractor that
// feeds the default-deny action allowlist scan. Workflow files can carry
// contributor-authored YAML, so a panic in extractWorkflowUses would surface as
// a process-level crash at scan time. The invariant is "no panic": for any
// input, extractWorkflowUses must return cleanly (an error for malformed YAML
// is expected and fine). Seeds are real repository workflows plus malformed
// shapes.
func FuzzExtractWorkflowUses(f *testing.F) {
	addRepoFileSeed(f, filepath.Join(".github", "workflows", "ci.yml"))
	addRepoFileSeed(f, filepath.Join(".github", "workflows", "mutation.yml"))
	addRepoFileSeed(f, filepath.Join(".github", "workflows", "security.yml"))
	seeds := []string{
		"",
		"\n",
		"jobs:\n",
		"jobs: {}\n",
		"jobs:\n  build:\n    uses: owner/repo/.github/workflows/x.yml@abc\n",
		"jobs:\n  build:\n    steps:\n      - uses: actions/checkout@v4\n",
		"jobs:\n  build:\n    steps:\n      - \"uses\": actions/checkout@v4\n",
		"jobs:\n  build:\n    steps:\n      - uses:   \n",
		"jobs: [1, 2, 3]\n",
		"not: valid: yaml: [\n",
		"\x00",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, workflowText string) {
		_, _ = extractWorkflowUses(workflowText)
	})
}

// FuzzParseToolPins exercises the [tool_pins] TOML parser that binds every
// workflow tool pin to policy/tool-pins.toml. The policy text is
// human-reviewed, but the parser must still never crash on arbitrary bytes.
// The invariant is "no panic": for any input, parseToolPins must return cleanly
// (an error for malformed or incomplete policy is expected). Seeds are the real
// policy/tool-pins.toml plus malformed shapes.
func FuzzParseToolPins(f *testing.F) {
	addRepoFileSeed(f, filepath.Join("policy", "tool-pins.toml"))
	seeds := []string{
		"",
		"\n",
		"[tool_pins]\n",
		"[tool_pins]\ncosign = \"v3.1.1\"\n",
		"[tool_pins]\nunknown = \"x\"\n",
		"[other]\ncosign = \"v3.1.1\"\n",
		"[tool_pins]\ncosign = \"\"\n",
		"[tool_pins\n",
		"cosign = \"v3.1.1\"\n",
		"\x00",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, text string) {
		_, _ = parseToolPins(text, "fuzz-tool-pins.toml")
	})
}

// FuzzValidateControlPlaneManifest exercises the control-plane manifest JSON
// validator on arbitrary bytes. The manifest gates runtime/host artifact
// attestation, so a panic in validation would surface as a crash on load. The
// invariant is "no panic": for any input, validateControlPlaneManifestBytes
// must return cleanly (an error for malformed or invalid manifests is
// expected). Seeds are the real committed manifest plus malformed shapes.
func FuzzValidateControlPlaneManifest(f *testing.F) {
	addRepoFileSeed(f, filepath.Join("runtime", "container", "control-plane-manifest.json"))
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	seeds := []string{
		"",
		"{}",
		"null",
		"[]",
		"{",
		`{"schema_version":1}`,
		`{"schema_version":2}`,
		`{"schema_version":2,"host_artifacts":[],"runtime_artifacts":[]}`,
		`{"schema_version":2,` +
			`"host_artifacts":[{"repo_path":"scripts/workcell","sha256":"` + digest + `"}],` +
			`"runtime_artifacts":[{"kind":"runtime-control-plane","repo_path":"a","runtime_path":"/a","sha256":"` + digest + `"}]}`,
		`{"schema_version":2,` +
			`"host_artifacts":[{"repo_path":"a","sha256":"not-a-digest"}],` +
			`"runtime_artifacts":[{"kind":"k","repo_path":"a","runtime_path":"relative","sha256":"` + digest + `"}]}`,
		"\x00",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, data string) {
		_ = validateControlPlaneManifestBytes([]byte(data))
	})
}
