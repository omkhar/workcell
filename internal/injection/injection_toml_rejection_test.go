// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseTOMLSubsetRejectsForbiddenConstructs is a table-driven check
// that the migrated parseTOMLSubset (which now delegates to the shared
// tomlsubset.ParseDocument API) still rejects the five forbidden subset
// constructs the legacy injection parser caught.  Each row is the
// smallest input that exercises one rule, plus a substring that must
// appear in the returned error so the diagnostic stays meaningful for
// operators staring at a broken injection-policy.toml.
func TestParseTOMLSubsetRejectsForbiddenConstructs(t *testing.T) {
	t.Parallel()
	type row struct {
		name      string
		content   string
		wantError string
	}
	rows := []row{
		{
			name:      "array_of_tables_unsupported_name",
			content:   "[[notcopies]]\nsource = \"/tmp/x\"\n",
			wantError: "unsupported array-of-table",
		},
		{
			name:      "dotted_key_in_documents",
			content:   "[documents]\nfoo.bar = \"baz\"\n",
			wantError: "dotted TOML keys are not supported",
		},
		{
			name:      "duplicate_key_in_documents",
			content:   "[documents]\ncommon = \"/tmp/a\"\ncommon = \"/tmp/b\"\n",
			wantError: "duplicate key: common",
		},
		{
			name:      "multi_line_basic_string",
			content:   "version = \"\"\"hi\nthere\"\"\"\n",
			wantError: "multi-line strings are not supported",
		},
		{
			name:      "inline_table",
			content:   "[ssh]\nidentities = { primary = \"id_rsa\" }\n",
			wantError: "inline tables are not supported",
		},
	}
	for _, r := range rows {
		r := r
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseTOMLSubset(r.content, Path("test.toml"))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", r.wantError)
			}
			if !strings.Contains(err.Error(), r.wantError) {
				t.Fatalf("error %q does not contain %q", err.Error(), r.wantError)
			}
		})
	}
}

// TestParseTOMLSubsetAcceptsCopiesArrayOfTables confirms the one
// permitted array-of-tables construct ([[copies]]) still works through
// the migrated parser.  The legacy injection parser tolerated this
// specifically because injection policy fragments use it for file-copy
// entries; the new extractCopiesBlocks adapter is what preserves that
// behaviour now that the underlying tomlsubset.ParseDocument is strict.
func TestParseTOMLSubsetAcceptsCopiesArrayOfTables(t *testing.T) {
	t.Parallel()
	content := strings.Join([]string{
		"version = 1",
		"",
		"[[copies]]",
		"source = \"/tmp/a\"",
		"target = \"/state/injected/a\"",
		"classification = \"public\"",
		"",
		"[[copies]]",
		"source = \"/tmp/b\"",
		"target = \"~/.config/workcell/b\"",
		"classification = \"secret\"",
	}, "\n")
	loaded, err := parseTOMLSubset(content, Path("copies.toml"))
	if err != nil {
		t.Fatalf("parseTOMLSubset: %v", err)
	}
	copies, ok := loaded["copies"].([]any)
	if !ok {
		t.Fatalf("copies missing or wrong type: %#v", loaded["copies"])
	}
	if len(copies) != 2 {
		t.Fatalf("want 2 copies entries, got %d", len(copies))
	}
	first, ok := copies[0].(map[string]any)
	if !ok {
		t.Fatalf("copies[0] wrong shape: %#v", copies[0])
	}
	if got := first["source"]; got != "/tmp/a" {
		t.Fatalf("copies[0].source = %#v, want /tmp/a", got)
	}
	if got := first["classification"]; got != "public" {
		t.Fatalf("copies[0].classification = %#v, want public", got)
	}
}

// TestParseTOMLSubsetUnsupportedTopLevelTable exercises the path where a
// top-level [header] that is not `documents`, `ssh`, `credentials`, or
// `credentials.<allowlisted>` is rejected.  Shipped non-injection TOMLs
// in policy/ and adapters/ trip this and we want the migrated parser to
// keep producing the same diagnostic the legacy parser emitted.
func TestParseTOMLSubsetUnsupportedTopLevelTable(t *testing.T) {
	t.Parallel()
	_, err := parseTOMLSubset("[forbidden_host_paths]\nkey = \"value\"\n", Path("test.toml"))
	if err == nil {
		t.Fatal("expected unsupported-table error, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported table [forbidden_host_paths]") {
		t.Fatalf("error %q does not name the offending table", err.Error())
	}
}

// TestParseTOMLSubsetUnsupportedCredentialsScopedTable exercises the
// credentials.<name> rejection path: a credentials sub-table whose key
// is not in credentialContainerPaths must be rejected.
func TestParseTOMLSubsetUnsupportedCredentialsScopedTable(t *testing.T) {
	t.Parallel()
	_, err := parseTOMLSubset("[credentials.no_such_key]\nsource = \"/tmp/x\"\n", Path("test.toml"))
	if err == nil {
		t.Fatal("expected unsupported credentials table error, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported credentials table [credentials.no_such_key]") {
		t.Fatalf("error %q does not name the offending table", err.Error())
	}
}

func TestParseTOMLSubsetAcceptsCopilotCredentialTable(t *testing.T) {
	t.Parallel()
	loaded, err := parseTOMLSubset("[credentials.copilot_github_token]\nsource = \"/tmp/copilot-token.txt\"\n", Path("test.toml"))
	if err != nil {
		t.Fatalf("parseTOMLSubset rejected supported Copilot credential table: %v", err)
	}
	credentials, ok := loaded["credentials"].(map[string]any)
	if !ok {
		t.Fatalf("credentials table missing or wrong type: %#v", loaded["credentials"])
	}
	if _, ok := credentials["copilot_github_token"]; !ok {
		t.Fatalf("credentials table missing copilot_github_token: %#v", credentials)
	}
}

// TestParseTOMLSubsetShippedPolicyFiles walks every real-world TOML in
// policy/ and adapters/*/requirements.toml and confirms the migrated
// injection parser rejects each one.  None of these files conform to
// the injection-policy schema enforced here, so they MUST all fail to
// parse — either because they use a non-`copies` array-of-table
// ([[functional.FR-001]] etc.) or because they declare a top-level
// table outside the injection whitelist.
//
// The intent of this test mirrors PR 37's equivalent in authpolicy:
//
//  1. Verify the shared tomlsubset.ParseDocument propagates errors
//     through the migrated injection.parseTOMLSubset adapter without
//     silently accepting otherwise-malformed input.
//  2. Lock in the negative ground truth: if a future change accidentally
//     widens the injection schema (say, by allowing [forbidden_host_paths]
//     or [[functional.FR-001]]), this test fails immediately.
func TestParseTOMLSubsetShippedPolicyFiles(t *testing.T) {
	t.Parallel()
	repoRoot := findRepoRoot(t)
	paths := []string{
		"policy/forbidden-host-paths.toml",
		"policy/github-hosted-controls.toml",
		"policy/operator-contract.toml",
		"policy/provider-bumps.toml",
		"policy/requirements.toml",
		"policy/reviewer-identities.toml",
		"adapters/codex/requirements.toml",
	}
	for _, rel := range paths {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			t.Parallel()
			abs := filepath.Join(repoRoot, rel)
			data, err := os.ReadFile(abs)
			if err != nil {
				t.Fatalf("read %s: %v", abs, err)
			}
			_, err = parseTOMLSubset(string(data), Path(abs))
			if err == nil {
				t.Fatalf("expected %s to be rejected by injection.parseTOMLSubset, but it parsed clean", rel)
			}
		})
	}
}

// TestParseTOMLSubsetAcceptsInjectionPolicyExample confirms the
// canonical injection-policy.toml example shipped in docs/examples
// still parses successfully through the migrated adapter.  This is the
// positive counterpart to TestParseTOMLSubsetShippedPolicyFiles: the
// one TOML in the repo that DOES match the injection schema must
// continue to round-trip through the parser unchanged.
func TestParseTOMLSubsetAcceptsInjectionPolicyExample(t *testing.T) {
	t.Parallel()
	repoRoot := findRepoRoot(t)
	abs := filepath.Join(repoRoot, "docs/examples/injection-policy.toml")
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read %s: %v", abs, err)
	}
	parsed, err := parseTOMLSubset(string(data), Path(abs))
	if err != nil {
		t.Fatalf("expected injection-policy example to parse, got %v", err)
	}
	if parsed["version"] != 1 {
		t.Fatalf("version = %#v, want 1", parsed["version"])
	}
	copies, ok := parsed["copies"].([]any)
	if !ok || len(copies) != 2 {
		t.Fatalf("copies = %#v, want 2 entries", parsed["copies"])
	}
}

// findRepoRoot walks up from the test file's working directory until it
// finds the repo's go.mod, so the shipped-policy walk works regardless
// of where `go test` is invoked from.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod walking up from %s", wd)
		}
		dir = parent
	}
}
