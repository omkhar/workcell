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

const fetchHostedControlsRepo = "omkhar/workcell"

func TestFetchRulesetsPinsGitHubAPIVersion(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "gh.args")
	stub := "#!/bin/sh\nprintf '%s\\n' \"$*\" >\"$WORKCELL_TEST_GH_LOG\"\nprintf '{\"name\":\"fixture\"}\\n'\n"
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	writeHostedRulesetSummary(t, root)
	t.Setenv("WORKCELL_TEST_GH_LOG", logPath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := metadatautil.FetchRulesets(root, fetchHostedControlsRepo); err != nil {
		t.Fatal(err)
	}
	requireFetchFileContains(t, logPath, "--hostname github.com -H X-GitHub-Api-Version: 2026-03-10 repos/omkhar/workcell/rulesets/42")
}

func TestFetchRulesetsRejectsMalformedSummaryIDs(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"non-object", `["42"]`, "unexpected ruleset summary entry"},
		{"missing", `[{}]`, "unexpected ruleset summary id"},
		{"string", `[{"id":"42"}]`, "unexpected ruleset summary id"},
		{"boolean", `[{"id":true}]`, "unexpected ruleset summary id"},
		{"zero", `[{"id":0}]`, "unexpected ruleset summary id"},
		{"negative", `[{"id":-1}]`, "unexpected ruleset summary id"},
		{"fractional", `[{"id":42.5}]`, "unexpected ruleset summary id"},
		{"decimal integer", `[{"id":42.0}]`, "unexpected ruleset summary id"},
		{"exponent", `[{"id":42e0}]`, "unexpected ruleset summary id"},
		{"rounded", `[{"id":42.000000000000001}]`, "unexpected ruleset summary id"},
		{"out of range", `[{"id":9223372036854775808}]`, "unexpected ruleset summary id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "rulesets-summary.json"), []byte(test.raw), 0o644); err != nil {
				t.Fatal(err)
			}
			err := metadatautil.FetchRulesets(root, fetchHostedControlsRepo)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("metadatautil.FetchRulesets() error = %v, want malformed-ID rejection", err)
			}
		})
	}
}

func TestFetchRulesetsRejectsMalformedSummaryDocuments(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"empty", ``, "must contain exactly one JSON value"},
		{"multiple values", `[{"id":42}] [{"id":43}]`, "must contain exactly one JSON value"},
		{"top-level object", `{"id":42}`, "must be an array"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "rulesets-summary.json"), []byte(test.raw), 0o644); err != nil {
				t.Fatal(err)
			}
			err := metadatautil.FetchRulesets(root, fetchHostedControlsRepo)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("metadatautil.FetchRulesets() error = %v, want %q", err, test.want)
			}
		})
	}
}

func writeHostedRulesetSummary(tb testing.TB, root string) {
	tb.Helper()
	if err := writeJSONFile(filepath.Join(root, "rulesets-summary.json"), []map[string]any{{"id": float64(42)}}); err != nil {
		tb.Fatal(err)
	}
}

func requireFetchFileContains(tb testing.TB, filePath, want string) {
	tb.Helper()
	content, err := os.ReadFile(filePath)
	if err != nil {
		tb.Fatal(err)
	}
	if !strings.Contains(string(content), want) {
		tb.Fatalf("%s = %q, want %q", filePath, content, want)
	}
}
