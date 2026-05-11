// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package providerid holds the canonical string identifiers for the
// per-provider adapters Workcell supports.
//
// Before this package existed, "claude" / "codex" / "gemini" were spelled as
// raw string literals in 70+ sites across internal/ and cmd/, with three
// different orderings of AllProviders.  Adding a fourth provider was an
// N-file refactor and silent drift between sites was easy.
//
// Use the named constants instead of raw strings; iterate AllProviders to
// keep the per-provider order stable.
package providerid

const (
	// Claude is the Anthropic Claude Code provider identifier.
	Claude = "claude"
	// Codex is the OpenAI Codex provider identifier.
	Codex = "codex"
	// Gemini is the Google Gemini provider identifier.
	Gemini = "gemini"
)

// AllProviders is the canonical iteration order for Workcell adapters.
// Many call sites (validators, manifests, sort orders) depend on a stable
// order; use this slice instead of declaring a local one.
var AllProviders = []string{Claude, Codex, Gemini}

// IsValid reports whether s names a supported provider.
func IsValid(s string) bool {
	switch s {
	case Claude, Codex, Gemini:
		return true
	}
	return false
}
