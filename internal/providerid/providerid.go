// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package providerid

import "slices"

const (
	// Claude is the Anthropic Claude Code provider identifier.
	Claude = "claude"
	// Codex is the OpenAI Codex provider identifier.
	Codex = "codex"
	// Antigravity is the Google Antigravity CLI provider identifier. It is a
	// planned fail-closed adapter, not a member of AllProviders until
	// runtime support and certification evidence land.
	Antigravity = "antigravity"
	// Copilot is the GitHub Copilot CLI provider identifier. It is a
	// planned fail-closed adapter, not a member of AllProviders until
	// runtime support and certification evidence land.
	Copilot = "copilot"
	// Gemini is the Google Gemini provider identifier.
	Gemini = "gemini"
	// CommonDocument is the provider-neutral managed document key.
	CommonDocument = "common"
)

// AllProviders is the canonical iteration order for Workcell adapters.
// Many call sites (validators, manifests, sort orders) depend on a stable
// order; use this slice instead of declaring a local one.
var AllProviders = []string{Claude, Codex, Gemini}

// CredentialMetadataProviders is the canonical order for provider credential
// metadata tables, including planned providers whose launch path is still
// fail-closed. Membership here is not a support-tier claim.
var CredentialMetadataProviders = []string{Claude, Codex, Copilot, Gemini}

// DocumentKeys is the canonical rendering/validation order for managed
// document injection. Copilot is deliberately absent because managed Copilot
// custom instructions are disabled.
var DocumentKeys = []string{CommonDocument, Codex, Claude, Gemini}

// IsValid reports whether s names a supported provider.
func IsValid(s string) bool {
	return slices.Contains(AllProviders, s)
}

// AllProviderSet returns the supported provider identifiers as a lookup set.
func AllProviderSet() map[string]struct{} {
	out := make(map[string]struct{}, len(AllProviders))
	for _, provider := range AllProviders {
		out[provider] = struct{}{}
	}
	return out
}

// DocumentKeySet returns the supported managed document keys.
func DocumentKeySet() map[string]struct{} {
	out := make(map[string]struct{}, len(DocumentKeys))
	for _, key := range DocumentKeys {
		out[key] = struct{}{}
	}
	return out
}
