// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package adapters aggregates per-provider table data so the injection,
// policy, and runtime paths can iterate over them without hard-coding
// provider names.  New adapters add a row to providers in data.go.
package adapters

// AgentScopedCredentialKeys maps each providerid to the set of credential
// keys scoped exclusively to that adapter (excludes shared keys).
func AgentScopedCredentialKeys() map[string]map[string]struct{} {
	out := make(map[string]map[string]struct{}, len(providers))
	for _, p := range providers {
		out[p.id] = setOf(p.tables.credentialKeys)
	}
	return out
}

// SharedCredentialKeys returns the set of credential keys provisioned for
// every adapter (currently the github_* keys).
func SharedCredentialKeys() map[string]struct{} {
	return setOf(sharedCredentialKeys)
}

// CredentialContainerPaths returns the merged container-side mount paths
// for every adapter-scoped and shared credential key.
func CredentialContainerPaths() map[string]string {
	out := map[string]string{}
	for _, p := range providers {
		for k, v := range p.tables.credentialContainerPaths {
			out[k] = v
		}
	}
	for k, v := range sharedCredentialContainerPaths {
		out[k] = v
	}
	return out
}

// ReservedTargets returns the union of in-container paths reserved by
// every adapter, in providerid-stable order.
func ReservedTargets() []string {
	var out []string
	for _, p := range providers {
		out = append(out, p.tables.reservedTargets...)
	}
	out = append(out, sharedReservedTargets...)
	return out
}

func setOf(keys []string) map[string]struct{} {
	out := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		out[k] = struct{}{}
	}
	return out
}
