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
)

// AllProviders is the canonical iteration order for Workcell adapters.
// Many call sites (validators, manifests, sort orders) depend on a stable
// order; use this slice instead of declaring a local one.
var AllProviders = []string{Claude, Codex, Gemini}

// IsValid reports whether s names a supported provider.
func IsValid(s string) bool {
	return slices.Contains(AllProviders, s)
}
