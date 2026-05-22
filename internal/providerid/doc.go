// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package providerid is the canonical source of supported provider
// id strings (Codex, Claude, Gemini today; Copilot is a roadmap
// addition).  Every other package that needs a provider name
// imports the constants here rather than redeclaring them, so
// adding or renaming a provider is a single-package edit.
package providerid
