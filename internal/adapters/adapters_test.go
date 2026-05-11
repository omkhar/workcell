// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package adapters

import (
	"sort"
	"testing"

	"github.com/omkhar/workcell/internal/providerid"
)

// Parity assertions for the credential / reserved-target tables: every
// non-empty credential key has a container mount path, every scoped key
// is also present in CredentialContainerPaths, no key is both shared
// and adapter-scoped.

func TestAgentScopedCredentialKeysCoversAllProviders(t *testing.T) {
	got := AgentScopedCredentialKeys()
	for _, want := range []string{providerid.Codex, providerid.Claude, providerid.Gemini} {
		if _, ok := got[want]; !ok {
			t.Errorf("provider %q missing from AgentScopedCredentialKeys", want)
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
