// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package adapters

import (
	"slices"
	"sort"
	"testing"

	"github.com/omkhar/workcell/internal/providerid"
)

// Parity assertions for the credential / reserved-target tables: every
// non-empty credential key has a container mount path, every scoped key
// is also present in CredentialContainerPaths, no key is both shared
// and adapter-scoped.

func TestProviderRegistryMatchesProviderIDOrder(t *testing.T) {
	got := make([]string, 0, len(providers))
	for _, provider := range providers {
		got = append(got, provider.id)
	}
	if !slices.Equal(got, providerid.CredentialMetadataProviders) {
		t.Fatalf("provider registry order = %v, want %v", got, providerid.CredentialMetadataProviders)
	}
}

func TestAgentScopedCredentialKeysCoversCredentialMetadataProviders(t *testing.T) {
	got := AgentScopedCredentialKeys()
	for _, want := range providerid.CredentialMetadataProviders {
		if _, ok := got[want]; !ok {
			t.Errorf("provider %q missing from AgentScopedCredentialKeys", want)
		}
	}
}

func TestAgentScopedCredentialKeysForProvidersFiltersPlannedProviders(t *testing.T) {
	got := AgentScopedCredentialKeysForProviders(providerid.AllProviders)
	if _, ok := got[providerid.Copilot]; ok {
		t.Fatal("planned Copilot credentials must not appear in supported-provider credential set")
	}
	for _, want := range providerid.AllProviders {
		if _, ok := got[want]; !ok {
			t.Errorf("supported provider %q missing from filtered credential set", want)
		}
	}
}

func TestScopedCredentialKeysHaveContainerPaths(t *testing.T) {
	paths := CredentialContainerPaths()
	for provider, keys := range AgentScopedCredentialKeys() {
		for key := range keys {
			if _, ok := paths[key]; !ok {
				t.Errorf("provider %q scoped credential key %q has no container path", provider, key)
			}
		}
	}
}

func TestSharedCredentialKeysHaveContainerPaths(t *testing.T) {
	paths := CredentialContainerPaths()
	for key := range SharedCredentialKeys() {
		if _, ok := paths[key]; !ok {
			t.Errorf("shared credential key %q has no container path", key)
		}
	}
}

func TestSharedCredentialsApplyOnlyToExplicitProviders(t *testing.T) {
	for _, provider := range []string{providerid.Claude, providerid.Codex, providerid.Gemini} {
		if !SharedCredentialsApplyToAgent(provider) {
			t.Errorf("shared credentials should apply to %q", provider)
		}
	}
	for _, provider := range []string{providerid.Copilot, providerid.Antigravity, "unknown"} {
		if SharedCredentialsApplyToAgent(provider) {
			t.Errorf("shared credentials should not apply to %q", provider)
		}
	}
}

func TestNoKeyIsBothScopedAndShared(t *testing.T) {
	shared := SharedCredentialKeys()
	for provider, keys := range AgentScopedCredentialKeys() {
		for key := range keys {
			if _, ok := shared[key]; ok {
				t.Errorf("credential key %q is listed as both shared and scoped to %q", key, provider)
			}
		}
	}
}

func TestReservedTargetsAreCleanAndUnique(t *testing.T) {
	seen := map[string]struct{}{}
	for _, target := range ReservedTargets() {
		if target == "" {
			t.Error("empty reserved target")
		}
		if target[0] != '/' {
			t.Errorf("reserved target %q is not absolute", target)
		}
		if _, dup := seen[target]; dup {
			t.Errorf("duplicate reserved target %q", target)
		}
		seen[target] = struct{}{}
	}
}

func TestCredentialContainerPathsRootedAtHostInputs(t *testing.T) {
	const wantPrefix = "/opt/workcell/host-inputs/credentials/"
	paths := CredentialContainerPaths()
	keys := make([]string, 0, len(paths))
	for k := range paths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if got := paths[k]; len(got) <= len(wantPrefix) || got[:len(wantPrefix)] != wantPrefix {
			t.Errorf("container path for %q is %q, want prefix %q", k, got, wantPrefix)
		}
	}
}
