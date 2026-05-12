// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package hostedcontrols carries the GitHub-hosted-controls policy
// parsing helpers that used to live in
// internal/metadatautil/validate.go: repository variable / workflow
// environment policy extraction, canonical-value validation,
// environment-name listing, artifact-name escaping, and the
// gh-api-driven ruleset fetcher. The host-side verifier
// (VerifyGitHubHostedControls) remains in the parent metadatautil
// package for now and calls into this package for the policy helpers
// it needs.
package hostedcontrols
