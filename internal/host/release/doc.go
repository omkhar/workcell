// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package release carries the GitHub release publication helpers
// (create payload, metadata, asset name encoding, bundle manifest)
// that the host-side `workcell-hostutil release` subcommands invoke.
// The release workflow itself lives in .github/workflows/release.yml;
// this package is the Go-side shape of the payloads it produces.
package release
