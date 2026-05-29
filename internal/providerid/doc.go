// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package providerid is the canonical source of the supported provider id
// strings (Claude, Codex, Gemini today; Copilot is a roadmap addition).
//
// Before this package existed, "claude" / "codex" / "gemini" were spelled as
// raw string literals in 70+ sites across internal/ and cmd/, with three
// different orderings of AllProviders.  Adding a provider was an N-file
// refactor and silent drift between sites was easy.  Every other package now
// imports the constants here rather than redeclaring them; use the named
// constants instead of raw strings, and iterate AllProviders to keep the
// per-provider order stable.
package providerid
