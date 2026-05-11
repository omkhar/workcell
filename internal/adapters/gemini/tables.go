// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package gemini carries the per-adapter tables consumed by the injection
// and policy paths.
package gemini

import "github.com/omkhar/workcell/internal/providerid"

const ProviderID = providerid.Gemini

var CredentialKeys = []string{
	"gemini_env",
	"gemini_oauth",
	"gemini_projects",
	"gcloud_adc",
}

var CredentialContainerPaths = map[string]string{
	"gemini_env":      "/opt/workcell/host-inputs/credentials/gemini.env",
	"gemini_oauth":    "/opt/workcell/host-inputs/credentials/gemini-oauth.json",
	"gemini_projects": "/opt/workcell/host-inputs/credentials/gemini-projects.json",
	"gcloud_adc":      "/opt/workcell/host-inputs/credentials/gcloud-adc.json",
}

var ReservedTargets = []string{
	"/state/agent-home/.gemini",
	"/state/agent-home/.config/gcloud",
	"/state/agent-home/.gemini/settings.json",
	"/state/agent-home/.gemini/GEMINI.md",
	"/state/agent-home/.gemini/.env",
	"/state/agent-home/.gemini/oauth_creds.json",
	"/state/agent-home/.gemini/projects.json",
	"/state/agent-home/.gemini/trustedFolders.json",
	"/state/agent-home/.config/gcloud/application_default_credentials.json",
}

// GoogleAuthEndpoints are the extra outbound endpoints Gemini requires for
// Google OAuth / ADC.
var GoogleAuthEndpoints = []string{
	"accounts.google.com:443",
	"oauth2.googleapis.com:443",
	"sts.googleapis.com:443",
}
