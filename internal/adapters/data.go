// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package adapters

// providerTables is the per-adapter data shape consumed by the injection
// and policy paths.  The providers rows are generated into data_gen.go from
// adapters/<id>/adapter.toml by scripts/generate-adapters-data.sh.
type providerTables struct {
	credentialKeys           []string
	credentialContainerPaths map[string]string
	reservedTargets          []string
}

type providerDefinition struct {
	id                       string
	sharedCredentialsEnabled bool
	tables                   providerTables
}

// sharedCredentialKeys lists credential keys provisioned for adapters
// whose provider registry row opts into shared credentials. Copilot
// deliberately opts out so GitHub CLI state is not implicit Copilot auth.
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
