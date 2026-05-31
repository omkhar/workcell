// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package providerid is the canonical source of provider id strings. Claude,
// Codex, and Gemini are supported today; Copilot is a planned fail-closed id
// until its runtime adapter and certification evidence land.
//
// Before this package existed, "claude" / "codex" / "gemini" were spelled as
// raw string literals in 70+ sites across internal/ and cmd/, with three
// different orderings of AllProviders.  Adding a provider was an N-file
// refactor and silent drift between sites was easy.  Every other package now
// imports the constants here rather than redeclaring them; use the named
// constants instead of raw strings, and iterate AllProviders to keep the
// per-provider order stable.
package providerid
