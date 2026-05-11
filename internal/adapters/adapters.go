// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package adapters aggregates per-provider table data so the injection,
// policy, and runtime paths can iterate over them without hard-coding
// provider names. New adapters register themselves here.
package adapters

import (
	"github.com/omkhar/workcell/internal/adapters/claude"
	"github.com/omkhar/workcell/internal/adapters/codex"
	"github.com/omkhar/workcell/internal/adapters/gemini"
	"github.com/omkhar/workcell/internal/adapters/shared"
	"github.com/omkhar/workcell/internal/providerid"
)

// AgentScopedCredentialKeys maps each providerid to the set of credential
// keys scoped exclusively to that adapter (excludes shared keys).
func AgentScopedCredentialKeys() map[string]map[string]struct{} {
	out := map[string]map[string]struct{}{}
	for _, agent := range registry {
		out[agent.id] = setOf(agent.credentialKeys)
	}
	return out
}

// SharedCredentialKeys returns the set of credential keys provisioned for
// every adapter (currently the github_* keys).
func SharedCredentialKeys() map[string]struct{} {
	return setOf(shared.CredentialKeys)
}

// CredentialContainerPaths returns the merged container-side mount paths
// for every adapter-scoped and shared credential key.
func CredentialContainerPaths() map[string]string {
	out := map[string]string{}
	for _, agent := range registry {
		for k, v := range agent.credentialPaths {
			out[k] = v
		}
	}
	for k, v := range shared.CredentialContainerPaths {
		out[k] = v
	}
	return out
}

// ReservedTargets returns the union of in-container paths reserved by
// every adapter, in providerid-stable order.
func ReservedTargets() []string {
	var out []string
	for _, agent := range registry {
		out = append(out, agent.reservedTargets...)
	}
	out = append(out, shared.ReservedTargets...)
	return out
}

type adapter struct {
	id              string
	credentialKeys  []string
	credentialPaths map[string]string
	reservedTargets []string
}

var registry = []adapter{
	{
		id:              providerid.Codex,
		credentialKeys:  codex.CredentialKeys,
		credentialPaths: codex.CredentialContainerPaths,
		reservedTargets: codex.ReservedTargets,
	},
	{
		id:              providerid.Claude,
		credentialKeys:  claude.CredentialKeys,
		credentialPaths: claude.CredentialContainerPaths,
		reservedTargets: claude.ReservedTargets,
	},
	{
		id:              providerid.Gemini,
		credentialKeys:  gemini.CredentialKeys,
		credentialPaths: gemini.CredentialContainerPaths,
		reservedTargets: gemini.ReservedTargets,
	},
}

func setOf(keys []string) map[string]struct{} {
	out := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		out[k] = struct{}{}
	}
	return out
}
