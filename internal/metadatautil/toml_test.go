// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"reflect"
	"testing"
)

func TestParseTOMLSubsetSupportsMultilineStringArrays(t *testing.T) {
	parsed, err := ParseTOMLSubset(`
[required_status_checks]
contexts = [
  "Validate repository",
  "Container smoke",
  "Reproducible build",
]
`, "policy/github-hosted-controls.toml")
	if err != nil {
		t.Fatalf("ParseTOMLSubset returned error: %v", err)
	}

	required, ok := parsed["required_status_checks"].(map[string]any)
	if !ok {
		t.Fatalf("required_status_checks missing or wrong type: %#v", parsed["required_status_checks"])
	}

	contexts, ok, err := MustStringSlice(required["contexts"])
	if err != nil {
		t.Fatalf("MustStringSlice returned error: %v", err)
	}
	if !ok {
		t.Fatalf("contexts missing or wrong type: %#v", required["contexts"])
	}

	expected := []string{
		"Validate repository",
		"Container smoke",
		"Reproducible build",
	}
	if !reflect.DeepEqual(contexts, expected) {
		t.Fatalf("contexts mismatch: got %#v want %#v", contexts, expected)
	}
}
