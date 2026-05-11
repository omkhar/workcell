// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package codex carries the per-adapter tables consumed by the injection
// and policy paths.
package codex

import "github.com/omkhar/workcell/internal/providerid"

const ProviderID = providerid.Codex

var CredentialKeys = []string{
	"codex_auth",
}

var CredentialContainerPaths = map[string]string{
	"codex_auth": "/opt/workcell/host-inputs/credentials/codex-auth.json",
}

var ReservedTargets = []string{
	"/state/agent-home/.codex",
	"/state/agent-home/.codex/AGENTS.md",
	"/state/agent-home/.codex/auth.json",
	"/state/agent-home/.codex/config.toml",
	"/state/agent-home/.codex/managed_config.toml",
	"/state/agent-home/.codex/requirements.toml",
	"/state/agent-home/.codex/agents",
	"/state/agent-home/.codex/rules",
	"/state/agent-home/.codex/mcp",
}
