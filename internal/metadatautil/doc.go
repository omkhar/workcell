// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package metadatautil carries the validators and contract-shape
// helpers for the repository-owned metadata artifacts that CI and
// release lanes consume.  It groups several related but distinct
// surfaces:
//
//   - provider-bumps:        policy/provider-bumps.toml cool-off contract
//   - workflow-lanes:        per-lane validate/release matrix shape
//   - coverage:              repo coverage metadata
//   - operator-contract:     repo-owned operator-action shape
//   - reproducible-build:    build-input manifest contract
//   - requirements:          adapter requirements shape
//   - validation-tools:      validator-image tooling pins
//   - workflow-validation:   GitHub workflow shape pins
//
// Some related surfaces have already been extracted into sibling
// packages (hostedcontrols/, pinnedinputs/, workflows/); the rest
// remain here for now.  The package's `*util` suffix predates the
// project's "name by role" convention — see CONTRIBUTING.md for the
// guidance on new package names.
package metadatautil
