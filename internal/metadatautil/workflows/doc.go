// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package workflows carries the GitHub Actions workflow validators
// that used to live in internal/metadatautil/validate.go. It exposes
// CheckWorkflows as the public entry point and the per-flow
// ValidateX/EnsureWorkflowTools helpers used by the workflow_validation
// tests in the parent metadatautil package.
package workflows
