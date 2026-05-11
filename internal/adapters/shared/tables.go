// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package shared holds credential keys and reserved targets that all
// adapters share — currently just GitHub host config / hosts.
package shared

var CredentialKeys = []string{
	"github_hosts",
	"github_config",
}

var CredentialContainerPaths = map[string]string{
	"github_hosts":  "/opt/workcell/host-inputs/credentials/github-hosts.yml",
	"github_config": "/opt/workcell/host-inputs/credentials/github-config.yml",
}

var ReservedTargets = []string{
	"/state/agent-home/.config/gh",
	"/state/agent-home/.config/gh/config.yml",
	"/state/agent-home/.config/gh/hosts.yml",
	"/state/agent-home/.ssh",
}
