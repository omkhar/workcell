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

func TestDefaultContractForProviderPreservesSharedContract(t *testing.T) {
	t.Parallel()

	for _, provider := range []string{AWSEC2SSMProvider, GCPVMProvider} {
		provider := provider
		t.Run(provider, func(t *testing.T) {
			t.Parallel()

			got := DefaultContractForProvider(provider)
			if err := got.Validate(); err != nil {
				t.Fatalf("DefaultContractForProvider(%q).Validate() error = %v", provider, err)
			}
			if got.TargetProvider != provider {
				t.Fatalf("target_provider = %q, want %q", got.TargetProvider, provider)
			}
			if got.TargetKind != TargetKind {
				t.Fatalf("target_kind = %q, want %q", got.TargetKind, TargetKind)
			}
			if got.RuntimeAPI != RuntimeAPI {
				t.Fatalf("runtime_api = %q, want %q", got.RuntimeAPI, RuntimeAPI)
			}
		})
	}
}
