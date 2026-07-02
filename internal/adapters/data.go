// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package adapters

import "github.com/omkhar/workcell/internal/providerid"

// providerTables is the per-adapter data shape consumed by the injection
// and policy paths.  When a planned provider lands per ROADMAP, it adds a row
// to providers below; no new package is required.
type providerTables struct {
	credentialKeys           []string
	credentialContainerPaths map[string]string
	reservedTargets          []string
}

// providers is the canonical per-adapter registry, ordered to match
// providerid stable ordering.  Order is load-bearing for deterministic
// rendering in the injection bundle (see ReservedTargets()).
var providers = []struct {
	id     string
	tables providerTables
}{
	{
		id: providerid.Claude,
		tables: providerTables{
			credentialKeys: []string{
				"claude_api_key",
				"claude_auth",
				"claude_mcp",
			},
			credentialContainerPaths: map[string]string{
				"claude_auth":    "/opt/workcell/host-inputs/credentials/claude-auth.json",
				"claude_api_key": "/opt/workcell/host-inputs/credentials/claude-api-key.txt",
				"claude_mcp":     "/opt/workcell/host-inputs/credentials/claude-mcp.json",
			},
			reservedTargets: []string{
				"/state/agent-home/.claude",
				"/state/agent-home/.config/claude-code",
				"/state/agent-home/.claude/settings.json",
				"/state/agent-home/.claude/CLAUDE.md",
				"/state/agent-home/.claude/.claude.json",
				"/state/agent-home/.claude.json",
				"/state/agent-home/.claude/.credentials.json",
				"/state/agent-home/.claude/workcell",
				"/state/agent-home/.config/claude-code/auth.json",
				"/state/agent-home/.mcp.json",
			},
		},
	},
	{
		id: providerid.Codex,
		tables: providerTables{
			credentialKeys: []string{
				"codex_auth",
			},
			credentialContainerPaths: map[string]string{
				"codex_auth": "/opt/workcell/host-inputs/credentials/codex-auth.json",
			},
			reservedTargets: []string{
				"/state/agent-home/.codex",
				"/state/agent-home/.codex/AGENTS.md",
				"/state/agent-home/.codex/auth.json",
				"/state/agent-home/.codex/config.toml",
				"/state/agent-home/.codex/managed_config.toml",
				"/state/agent-home/.codex/requirements.toml",
				"/state/agent-home/.codex/agents",
				"/state/agent-home/.codex/rules",
				"/state/agent-home/.codex/mcp",
			},
		},
	},
	{
		id: providerid.Copilot,
		tables: providerTables{
			credentialKeys: []string{
				"copilot_github_token",
			},
			credentialContainerPaths: map[string]string{
				"copilot_github_token": "/opt/workcell/host-inputs/credentials/copilot-github-token.txt",
			},
			reservedTargets: []string{
				"/state/agent-home/.copilot",
				"/state/agent-home/.copilot/AGENTS.md",
				"/state/agent-home/.copilot/logs",
				"/state/agent-home/.cache/github-copilot",
				"/state/agent-home/.config/github-copilot",
			},
		},
	},
	{
		id: providerid.Gemini,
		tables: providerTables{
			credentialKeys: []string{
				"gemini_env",
				"gemini_oauth",
				"gemini_projects",
				"gcloud_adc",
			},
			credentialContainerPaths: map[string]string{
				"gemini_env":      "/opt/workcell/host-inputs/credentials/gemini.env",
				"gemini_oauth":    "/opt/workcell/host-inputs/credentials/gemini-oauth.json",
				"gemini_projects": "/opt/workcell/host-inputs/credentials/gemini-projects.json",
				"gcloud_adc":      "/opt/workcell/host-inputs/credentials/gcloud-adc.json",
			},
			reservedTargets: []string{
				"/state/agent-home/.gemini",
				"/state/agent-home/.config/gcloud",
				"/state/agent-home/.gemini/settings.json",
				"/state/agent-home/.gemini/GEMINI.md",
				"/state/agent-home/.gemini/.env",
				"/state/agent-home/.gemini/oauth_creds.json",
				"/state/agent-home/.gemini/projects.json",
				"/state/agent-home/.gemini/trustedFolders.json",
				"/state/agent-home/.config/gcloud/application_default_credentials.json",
			},
		},
	},
}

// sharedCredentialKeys lists credential keys provisioned for every
// adapter (currently the github_* keys).
var sharedCredentialKeys = []string{
	"github_hosts",
	"github_config",
}

// sharedCredentialContainerPaths maps each shared credential key to its
// in-container mount path.
var sharedCredentialContainerPaths = map[string]string{
	"github_hosts":  "/opt/workcell/host-inputs/credentials/github-hosts.yml",
	"github_config": "/opt/workcell/host-inputs/credentials/github-config.yml",
}

// sharedReservedTargets are container paths reserved across all adapters
// (gh CLI config, .ssh).
var sharedReservedTargets = []string{
	"/state/agent-home/.config/gh",
	"/state/agent-home/.config/gh/config.yml",
	"/state/agent-home/.config/gh/hosts.yml",
	"/state/agent-home/.ssh",
}

// GeminiGoogleAuthEndpoints are the extra outbound endpoints Gemini
// requires for Google OAuth / ADC.  Exposed at the adapters package
// boundary so the injection path does not need to know about per-
// adapter sub-packages.
var GeminiGoogleAuthEndpoints = []string{
	"accounts.google.com:443",
	"oauth2.googleapis.com:443",
	"sts.googleapis.com:443",
}
