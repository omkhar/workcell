// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authpolicy

import (
	"reflect"
	"testing"

	"github.com/omkhar/workcell/internal/adapters"
	"github.com/omkhar/workcell/internal/providerid"
)

func TestCredentialRegistryMatchesAdapters(t *testing.T) {
	wantScoped := adapters.AgentScopedCredentialKeysForProviders(providerid.AllProviders)
	if !reflect.DeepEqual(AgentScopedCredentialKeys, wantScoped) {
		t.Fatalf("AgentScopedCredentialKeys = %#v, want supported adapter registry %#v", AgentScopedCredentialKeys, wantScoped)
	}
	wantShared := adapters.SharedCredentialKeys()
	if !reflect.DeepEqual(SharedCredentialKeys, wantShared) {
		t.Fatalf("SharedCredentialKeys = %#v, want adapter registry %#v", SharedCredentialKeys, wantShared)
	}
	wantAll := map[string]struct{}{}
	for key := range wantShared {
		wantAll[key] = struct{}{}
	}
	for _, keys := range wantScoped {
		for key := range keys {
			wantAll[key] = struct{}{}
		}
	}
	if !reflect.DeepEqual(CredentialKeys, wantAll) {
		t.Fatalf("CredentialKeys = %#v, want supported adapter registry union %#v", CredentialKeys, wantAll)
	}
}
