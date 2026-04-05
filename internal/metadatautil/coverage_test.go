// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCoverageExecutablesParsesJSONLStream(t *testing.T) {
	root := t.TempDir()
	messagePath := filepath.Join(root, "cargo-messages.jsonl")
	if err := os.WriteFile(messagePath, []byte(
		"noise\n"+
			`{"reason":"compiler-artifact","executable":"/tmp/bin-a","target":{"kind":["bin"]}}`+"\n"+
			`{"reason":"compiler-artifact","executable":"/tmp/bin-b","target":{"kind":["lib"]}}`+"\n"+
			`{"reason":"compiler-artifact","executable":"/tmp/bin-a","target":{"kind":["bin"]}}`),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := CoverageExecutables(messagePath)
	if err != nil {
		t.Fatalf("CoverageExecutables() error = %v", err)
	}
	want := []string{"/tmp/bin-a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CoverageExecutables() = %#v, want %#v", got, want)
	}
}
