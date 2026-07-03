// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authresolve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseTOMLSubsetRejectsForbiddenConstructs is a table-driven check
// that the migrated parseTOMLSubset (which now delegates to the shared
// tomlsubset.ParseDocument API) still rejects the five forbidden subset
// constructs the legacy parser caught.  Each row is the smallest input
// that exercises one rule, plus a substring that must appear in the
// returned error so the diagnostic stays meaningful for operators.
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
			name:      "dotted_key_in_credentials",
			content:   "[credentials]\nfoo.bar = \"baz\"\n",
			wantError: "dotted TOML keys are not supported",
		},
		{
			name:      "duplicate_key_in_documents",
			content:   "[documents]\ncommon = \"/tmp/a\"\ncommon = \"/tmp/b\"\n",
			wantError: "duplicate key: common",
		},
		{
			name:      "credentials_scalar_table_collision",
			content:   "[credentials.codex_auth]\nsource = \"/tmp/a\"\n[credentials]\ncodex_auth = \"/tmp/b\"\n",
			wantError: "duplicate key across table forms: credentials.codex_auth",
		},
		{
			name:      "credentials_table_scalar_collision",
			content:   "credentials = \"/tmp/a\"\n[credentials.codex_auth]\nsource = \"/tmp/b\"\n",
			wantError: "credentials table conflicts with scalar key credentials",
		},
		{
			name:      "duplicate_credentials_entry",
			content:   "[credentials]\ncodex_auth = \"/tmp/a\"\n[credentials.codex_auth]\nsource = \"/tmp/b\"\n",
			wantError: "duplicate credentials entry: codex_auth",
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
		{
			name:      "unsupported_scalar_credential_key",
			content:   "[credentials]\nnot_a_credential = \"/tmp/nope.txt\"\n",
			wantError: "credentials contains unsupported keys: not_a_credential",
		},
	}
	for _, r := range rows {
		r := r
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseTOMLSubset(r.content, "test.toml")
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
// the migrated parser.  The legacy parser tolerated this specifically
// because injection policy fragments use it for file-copy entries.
func TestParseTOMLSubsetAcceptsCopiesArrayOfTables(t *testing.T) {
	t.Parallel()
	content := strings.Join([]string{
		"version = 1",
		"",
		"[[copies]]",
		"source = \"/tmp/a\"",
		"target = \"/state/a\"",
		"classification = \"public\"",
		"",
		"[[copies]]",
		"source = \"/tmp/b\"",
		"target = \"/state/b\"",
		"classification = \"secret\"",
	}, "\n")
	loaded, err := parseTOMLSubset(content, "copies.toml")
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
}

// TestParseTOMLSubsetUnsupportedTopLevelTable exercises the path where
// a top-level [header] that is not `documents`, `ssh`, `credentials`, or
// `credentials.<name>` is rejected.  Shipped non-injection TOMLs in
// policy/ and adapters/ trip this and we want the migrated parser to
// keep producing the same diagnostic.
func TestParseTOMLSubsetUnsupportedTopLevelTable(t *testing.T) {
	t.Parallel()
	_, err := parseTOMLSubset("[forbidden_host_paths]\nkey = \"value\"\n", "test.toml")
	if err == nil {
		t.Fatal("expected unsupported-table error, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported table [forbidden_host_paths]") {
		t.Fatalf("error %q does not name the offending table", err.Error())
	}
}

// TestParseTOMLSubsetShippedPolicyFiles walks every real-world TOML in
// policy/ and adapters/*/requirements.toml and confirms the migrated
// parser rejects each one.  None of these files conform to the injection
// policy schema authresolve enforces, so they MUST all fail to parse —
// either because they use [[rules.prefix_rules]] (rejected as an
// array-of-table other than [[copies]]) or because they declare a
// top-level table that is not in the authresolve whitelist.
//
// The intent of this test is twofold:
//
//  1. Verify the shared tomlsubset.ParseDocument propagates errors
//     through the migrated authresolve.parseTOMLSubset adapter without
//     silently accepting otherwise-malformed input.
//  2. Lock in the negative ground truth: if a future change accidentally
//     widens the authresolve schema (say, by allowing [forbidden_host_paths]
//     or [[rules.prefix_rules]]), this test fails immediately.
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
			_, err = parseTOMLSubset(string(data), abs)
			if err == nil {
				t.Fatalf("expected %s to be rejected by authresolve.parseTOMLSubset, but it parsed clean", rel)
			}
		})
	}
}

func TestLoadPolicyBundleRejectsUnsupportedCopilotDocument(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.toml")
	if err := os.WriteFile(policyPath, []byte(strings.Join([]string{
		"version = 1",
		"",
		"[documents]",
		"copilot = \"copilot.md\"",
		"",
	}, "\n")), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "copilot.md"), []byte("unsupported\n"), 0o600); err != nil {
		t.Fatalf("write copilot doc: %v", err)
	}

	_, _, err := loadPolicyBundle(policyPath)
	if err == nil {
		t.Fatal("expected documents.copilot to be rejected by authresolve")
	}
	if !strings.Contains(err.Error(), "documents contains unsupported keys: copilot") {
		t.Fatalf("err = %v, want unsupported documents.copilot failure", err)
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
