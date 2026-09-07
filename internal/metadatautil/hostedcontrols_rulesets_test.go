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

func TestListHostedControlRulesetIDsPreservesOrderAndDuplicates(t *testing.T) {
	path := writeRulesetFixture(t, "summary.json", `[{"id":42},{"id":7},{"id":42}]`)
	var output strings.Builder
	if err := metadatautil.ListHostedControlRulesetIDs(path, &output); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "42\n7\n42\n"; got != want {
		t.Fatalf("ruleset IDs = %q, want %q", got, want)
	}
}

func TestListHostedControlRulesetIDsRejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"empty", ``, "must contain exactly one JSON value"},
		{"multiple values", `[{"id":42}] [{"id":43}]`, "must contain exactly one JSON value"},
		{"top-level object", `{"id":42}`, "must be an array"},
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
			path := writeRulesetFixture(t, "summary.json", test.raw)
			err := metadatautil.ListHostedControlRulesetIDs(path, &strings.Builder{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ListHostedControlRulesetIDs() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestNormalizeHostedControlRulesetRequiresOneMatchingObject(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"multiple", `{"id":42}{"id":42}`},
		{"array", `[{"id":42}]`},
		{"missing ID", `{}`},
		{"wrong ID", `{"id":43}`},
		{"noncanonical ID", `{"id":42.0}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := metadatautil.NormalizeHostedControlRuleset(strings.NewReader(test.raw), &strings.Builder{}, "42"); err == nil {
				t.Fatal("NormalizeHostedControlRuleset unexpectedly succeeded")
			}
		})
	}
	var output strings.Builder
	if err := metadatautil.NormalizeHostedControlRuleset(strings.NewReader(`{"name":"r","id":42}`), &output, "42"); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "{\"id\":42,\"name\":\"r\"}\n"; got != want {
		t.Fatalf("normalized detail = %q, want %q", got, want)
	}
}

func TestAssembleHostedControlRulesetsBindsCountOrderAndIDs(t *testing.T) {
	summary := writeRulesetFixture(t, "summary.json", `[{"id":42},{"id":7},{"id":42}]`)
	details := writeRulesetFixture(t, "details.jsons", "{\"id\":42,\"name\":\"a\"}\n{\"id\":7,\"name\":\"b\"}\n{\"id\":42,\"name\":\"c\"}\n")
	output := filepath.Join(t.TempDir(), "rulesets.json")
	if err := metadatautil.AssembleHostedControlRulesets(summary, details, output); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), "[\n  {\n    \"id\": 42,\n    \"name\": \"a\"\n  },\n  {\n    \"id\": 7,\n    \"name\": \"b\"\n  },\n  {\n    \"id\": 42,\n    \"name\": \"c\"\n  }\n]\n"; got != want {
		t.Fatalf("rulesets.json = %q, want %q", got, want)
	}
}

func TestAssembleHostedControlRulesetsPreservesEmptyArray(t *testing.T) {
	summary := writeRulesetFixture(t, "summary.json", `[]`)
	details := writeRulesetFixture(t, "details.jsons", "")
	output := filepath.Join(t.TempDir(), "rulesets.json")
	if err := metadatautil.AssembleHostedControlRulesets(summary, details, output); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), "[]\n"; got != want {
		t.Fatalf("empty rulesets = %q, want %q", got, want)
	}
}

func TestAssembleHostedControlRulesetsRejectsMalformedDetails(t *testing.T) {
	summary := writeRulesetFixture(t, "summary.json", `[{"id":42},{"id":7}]`)
	for _, raw := range []string{"", `{"id":42}`, `{"id":7}{"id":42}`, `{"id":42}[]`, `{"id":42}{"id":"7"}`} {
		details := writeRulesetFixture(t, "details.jsons", raw)
		if err := metadatautil.AssembleHostedControlRulesets(summary, details, filepath.Join(t.TempDir(), "rulesets.json")); err == nil {
			t.Fatalf("AssembleHostedControlRulesets(%q) unexpectedly succeeded", raw)
		}
	}
}

func writeRulesetFixture(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
