// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

import (
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestDefaultContractMatchesPolicyArtifact(t *testing.T) {
	t.Parallel()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	policyPath := filepath.Join(filepath.Dir(file), "..", "..", "policy", "remote-vm-contract.json")
	got, err := LoadContract(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	want := DefaultContract()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadContract(%s) = %#v, want %#v", policyPath, got, want)
	}
}

func TestDefaultContractValidates(t *testing.T) {
	t.Parallel()

	if err := DefaultContract().Validate(); err != nil {
		t.Fatalf("DefaultContract().Validate() error = %v", err)
	}
}
