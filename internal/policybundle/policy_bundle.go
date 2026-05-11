// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package policybundle is dormant — every function previously exported
// from here is unimported across the repo (verified by repo-wide grep).
// Only the package-level data tables remain pending the final-deletion
// follow-up PR. New callers should never grow against this surface; use
// internal/adapters/ for per-provider tables and the relevant injection
// / authpolicy entry points for the parser/render code that used to
// live here.
package policybundle

import "github.com/omkhar/workcell/internal/providerid"

var SupportedAgents = map[string]struct{}{
	providerid.Codex:  {},
	providerid.Claude: {},
	providerid.Gemini: {},
}

var SupportedModes = map[string]struct{}{
	"strict":      {},
	"development": {},
	"build":       {},
	"breakglass":  {},
}

var CredentialKeys = map[string]struct{}{
	"codex_auth":      {},
	"claude_auth":     {},
	"claude_api_key":  {},
	"claude_mcp":      {},
	"gemini_env":      {},
	"gemini_oauth":    {},
	"gemini_projects": {},
	"gcloud_adc":      {},
	"github_hosts":    {},
	"github_config":   {},
}

var AgentScopedCredentialKeys = map[string]map[string]struct{}{
	providerid.Codex: {
		"codex_auth": {},
	},
	providerid.Claude: {
		"claude_api_key": {},
		"claude_auth":    {},
		"claude_mcp":     {},
	},
	providerid.Gemini: {
		"gemini_env":      {},
		"gemini_oauth":    {},
		"gemini_projects": {},
		"gcloud_adc":      {},
	},
}

var SharedCredentialKeys = map[string]struct{}{
	"github_hosts":  {},
	"github_config": {},
}

var AllowedRootPolicyKeys = map[string]struct{}{
	"version":     {},
	"includes":    {},
	"documents":   {},
	"ssh":         {},
	"copies":      {},
	"credentials": {},
}

type PolicySource struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
