// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type mergedSecretPage struct {
	TotalCount int            `json:"total_count"`
	Secrets    []mergedSecret `json:"secrets"`
}

type mergedSecret struct {
	Name string `json:"name"`
}

func TestMergeHostedControlObjectPagesPreservesOrder(t *testing.T) {
	input := strings.NewReader("{\"total_count\":2,\"secrets\":[{\"name\":\"A\"}]}\n{\"total_count\":2,\"secrets\":[{\"name\":\"B\"}]}\n")
	var output bytes.Buffer
	if err := MergeHostedControlObjectPages(input, &output, "secrets"); err != nil {
		t.Fatal(err)
	}
	var merged mergedSecretPage
	if err := json.Unmarshal(output.Bytes(), &merged); err != nil {
		t.Fatal(err)
	}
	expected := mergedSecretPage{TotalCount: 2, Secrets: []mergedSecret{{Name: "A"}, {Name: "B"}}}
	if !reflect.DeepEqual(merged, expected) {
		t.Fatalf("merged page = %#v, want %#v", merged, expected)
	}
}

func TestMergeHostedControlObjectPagesPreservesEmptyArray(t *testing.T) {
	var output bytes.Buffer
	if err := MergeHostedControlObjectPages(strings.NewReader(`{"total_count":0,"secrets":[]}`), &output, "secrets"); err != nil {
		t.Fatal(err)
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(output.Bytes(), &merged); err != nil {
		t.Fatal(err)
	}
	if string(merged["secrets"]) != "[]" {
		t.Fatalf("merged secrets = %s, want []", merged["secrets"])
	}
}

func TestMergeHostedControlObjectPagesRejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name  string
		field string
		input string
		want  string
	}{
		{"unsupported field", "unknown", `{}`, "unsupported hosted-control collection field"},
		{"empty stream", "secrets", ``, "must contain at least one page"},
		{"null page", "secrets", `null`, "must be an object"},
		{"array page", "secrets", `[]`, "must be an object"},
		{"missing total", "secrets", `{"secrets":[]}`, "must declare total_count"},
		{"string total", "secrets", `{"total_count":"0","secrets":[]}`, "must be a nonnegative integer"},
		{"fractional total", "secrets", `{"total_count":0.5,"secrets":[]}`, "must be a nonnegative integer"},
		{"negative total", "secrets", `{"total_count":-1,"secrets":[]}`, "must be a nonnegative integer"},
		{"missing field", "secrets", `{"total_count":0}`, `field "secrets" must be an array`},
		{"wrong field type", "secrets", `{"total_count":0,"secrets":{}}`, `field "secrets" must be an array`},
		{"inconsistent totals", "secrets", "{\"total_count\":1,\"secrets\":[]}\n{\"total_count\":0,\"secrets\":[]}", "total_count is inconsistent"},
		{"count mismatch", "secrets", `{"total_count":1,"secrets":[]}`, "must equal the combined array length"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			err := MergeHostedControlObjectPages(strings.NewReader(test.input), &output, test.field)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("MergeHostedControlObjectPages() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestMergeHostedControlArrayPagesPreservesOrder(t *testing.T) {
	var output bytes.Buffer
	err := MergeHostedControlArrayPages(strings.NewReader("[{\"id\":1}]\n[{\"id\":2}]\n"), &output)
	if err != nil {
		t.Fatal(err)
	}
	var merged []map[string]int
	if err := json.Unmarshal(output.Bytes(), &merged); err != nil {
		t.Fatal(err)
	}
	expected := []map[string]int{{"id": 1}, {"id": 2}}
	if !reflect.DeepEqual(merged, expected) {
		t.Fatalf("merged pages = %#v, want %#v", merged, expected)
	}
}

func TestMergeHostedControlArrayPagesPreservesEmptyArray(t *testing.T) {
	var output bytes.Buffer
	if err := MergeHostedControlArrayPages(strings.NewReader(`[]`), &output); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output.String()) != "[]" {
		t.Fatalf("merged pages = %q, want []", output.String())
	}
}

func TestMergeHostedControlArrayPagesRejectsMalformedInput(t *testing.T) {
	for _, input := range []string{"", "null", `{}`} {
		var output bytes.Buffer
		if err := MergeHostedControlArrayPages(strings.NewReader(input), &output); err == nil {
			t.Fatalf("MergeHostedControlArrayPages(%q) unexpectedly succeeded", input)
		}
	}
}
