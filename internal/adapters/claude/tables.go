// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package claude carries the per-adapter tables consumed by the injection
// and policy paths. Keeping them here means adding a new credential key,
// reserved home path, or risky directive for one provider is a one-package
// edit instead of touching the central injection package.
package claude

import "github.com/omkhar/workcell/internal/providerid"

const ProviderID = providerid.Claude

// CredentialKeys lists the policy keys that route to this adapter when
// emitting a credentials.<key> table. Order is stable for deterministic
// rendering.
var CredentialKeys = []string{
	"claude_api_key",
	"claude_auth",
	"claude_mcp",
}

// CredentialContainerPaths maps each adapter-scoped credential key to the
// in-container mount path the runtime materializes it at.
var CredentialContainerPaths = map[string]string{
	"claude_auth":    "/opt/workcell/host-inputs/credentials/claude-auth.json",
	"claude_api_key": "/opt/workcell/host-inputs/credentials/claude-api-key.txt",
	"claude_mcp":     "/opt/workcell/host-inputs/credentials/claude-mcp.json",
}

// ReservedTargets are container paths the adapter owns. Injection policy
// MUST NOT permit user copies/symlinks into these — workcell synthesizes
// them on its own.
var ReservedTargets = []string{
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
}
