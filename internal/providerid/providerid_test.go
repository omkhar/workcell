// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package providerid

import (
	"slices"
	"testing"
)

func TestPlannedAntigravityRemainsUnsupportedUntilCertified(t *testing.T) {
	if Antigravity != "antigravity" {
		t.Fatalf("Antigravity = %q, want antigravity", Antigravity)
	}
	if IsValid(Antigravity) {
		t.Fatal("Antigravity must stay out of the supported-provider set until runtime support and certification land")
	}
}

func TestCopilotIsSupportedProvider(t *testing.T) {
	if Copilot != "copilot" {
		t.Fatalf("Copilot = %q, want copilot", Copilot)
	}
	if !IsValid(Copilot) {
		t.Fatal("Copilot must be in the supported-provider set after runtime support and certification evidence land")
	}
}

func TestAllProviderSetMatchesAllProviders(t *testing.T) {
	set := AllProviderSet()
	if len(set) != len(AllProviders) {
		t.Fatalf("AllProviderSet length = %d, want %d", len(set), len(AllProviders))
	}
	for _, provider := range AllProviders {
		if _, ok := set[provider]; !ok {
			t.Fatalf("AllProviderSet missing %q", provider)
		}
	}
	if _, ok := set[Antigravity]; ok {
		t.Fatal("AllProviderSet must not include planned providers before certification")
	}
	if _, ok := set[Copilot]; !ok {
		t.Fatal("AllProviderSet must include supported Copilot")
	}
}

func TestCredentialMetadataProvidersIncludesPlannedCopilot(t *testing.T) {
	want := []string{Claude, Codex, Copilot, Gemini}
	if !slices.Equal(CredentialMetadataProviders, want) {
		t.Fatalf("CredentialMetadataProviders = %v, want %v", CredentialMetadataProviders, want)
	}
}
