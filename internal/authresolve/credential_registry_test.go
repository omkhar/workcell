// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authresolve

import (
	"reflect"
	"testing"

	"github.com/omkhar/workcell/internal/adapters"
	"github.com/omkhar/workcell/internal/providerid"
)

func TestCredentialRegistryMatchesAdapters(t *testing.T) {
	wantScoped := adapters.AgentScopedCredentialKeysForProviders(providerid.AllProviders)
	if !reflect.DeepEqual(agentScopedCredentialKeys, wantScoped) {
		t.Fatalf("agentScopedCredentialKeys = %#v, want supported adapter registry %#v", agentScopedCredentialKeys, wantScoped)
	}
	wantShared := adapters.SharedCredentialKeys()
	if !reflect.DeepEqual(sharedCredentialKeys, wantShared) {
		t.Fatalf("sharedCredentialKeys = %#v, want adapter registry %#v", sharedCredentialKeys, wantShared)
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
	if !reflect.DeepEqual(allCredentialKeys, wantAll) {
		t.Fatalf("allCredentialKeys = %#v, want supported adapter registry union %#v", allCredentialKeys, wantAll)
	}
}
